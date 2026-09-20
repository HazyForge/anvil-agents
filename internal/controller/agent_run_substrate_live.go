package controller

import (
	"context"
	"errors"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// substrateLiveClient returns the live actor client when the explicit opt-in
// gate is on and a backend is configured. Nil means the controller keeps the
// API-first hold (NeedsHuman/SubstrateActorNotWired) and never creates a Job
// for SubstrateActor runs. The default Job path never consults this client.
func (r *AgentRunReconciler) substrateLiveClient() substrate.Client {
	if r == nil || r.CommonReconcilerOptions.Options == nil {
		return nil
	}
	if !r.CommonReconcilerOptions.Options.SubstrateActorsEnabled {
		return nil
	}
	if strings.TrimSpace(r.CommonReconcilerOptions.Options.SubstrateEndpoint) == "" {
		return nil
	}
	return r.SubstrateClient
}

func (r *AgentRunReconciler) substrateActorClass(obj *controlv1alpha1.AgentRun) string {
	if obj == nil || obj.Spec.Harness.Execution.Substrate == nil {
		return ""
	}
	return strings.TrimSpace(obj.Spec.Harness.Execution.Substrate.ActorClass)
}

// shouldGenerateOnActor reports whether this run is opted into atenet
// generate. Desktop standing-chat (actorClass standing-chat) stays off the
// allowlist so ProcessBackend keeps owning those turns.
func (r *AgentRunReconciler) shouldGenerateOnActor(obj *controlv1alpha1.AgentRun) bool {
	if obj == nil || !obj.Spec.Harness.Execution.UsesSubstrateActors() {
		return false
	}
	if r.substrateLiveClient() == nil || r.SubstrateGenerate == nil || r.Options == nil {
		return false
	}
	return substrate.GenerateEnabled(r.Options.SubstrateGenerateOnActor, r.Options.SubstrateGenerateActorClasses, r.substrateActorClass(obj))
}

// substrateThreadID resolves the stable chat thread backing one effective run.
// Direct turns use their own thread; peer child runs use the recipient child
// thread, so both resume through the same ActorNameForThread convention.
func substrateThreadID(effective *controlv1alpha1.AgentRun) string {
	if effective == nil {
		return ""
	}
	return substrate.ThreadIDForRun(
		effective.Spec.SourceRef.Kind,
		effective.Spec.SourceRef.Name,
		effective.Name,
	)
}

// substrateActorSpecForRun builds the lifecycle spec for one effective run
// without importing Anvil API types into the substrate package.
func substrateActorSpecForRun(effective *controlv1alpha1.AgentRun, actorName string) substrate.ActorSpec {
	execution := effective.Spec.Harness.Execution
	var actorClass, pool string
	if execution.Substrate != nil {
		actorClass = execution.Substrate.ActorClass
		pool = execution.Substrate.Pool
	}
	threadID := substrateThreadID(effective)
	return substrate.ActorSpecForRun(
		effective.Namespace,
		actorName,
		string(agentRunBackendKind(effective)),
		actorClass,
		pool,
		map[string]string{
			agentRunLabel:                           sanitizeLabelValue(effective.Name),
			controlv1alpha1.AgentRunChatThreadLabel: sanitizeLabelValue(threadID),
		},
	)
}

func substrateGenerateAtespace(r *AgentRunReconciler, handle substrate.ActorHandle) string {
	if r != nil && r.Options != nil {
		if forced := strings.TrimSpace(r.Options.SubstrateAtespace); forced != "" {
			return forced
		}
	}
	return strings.TrimSpace(handle.Namespace)
}

// reconcileSubstrateActorRun binds one opted-in SubstrateActor run to its
// warm actor, streams the frozen prompt through atenet, and marks the run
// Succeeded or Failed from the actor reply. It never creates a Kubernetes
// Job. Transient atenet failures requeue while Running; permanent failures
// fail the run so it cannot sit at SubstrateActorBound forever.
func (r *AgentRunReconciler) reconcileSubstrateActorRun(ctx context.Context, original, obj *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, effective *controlv1alpha1.AgentRun, now metav1.Time) (ctrl.Result, error) {
	if agentRunPhaseTerminal(obj.Status.Phase) || agentRunPhaseTerminal(status.Phase) {
		return ctrl.Result{}, nil
	}
	if !r.shouldGenerateOnActor(effective) {
		return ctrl.Result{}, nil
	}
	live := r.substrateLiveClient()
	threadID := substrateThreadID(effective)
	actorName := substrate.ActorNameForThread(threadID)
	handle, warm, err := substrate.EnsureTurnActor(ctx, live, substrateActorSpecForRun(effective, actorName))
	if err != nil {
		return ctrl.Result{}, err
	}
	status.ExecutionRuntime = string(controlv1alpha1.AgentRunExecutionRuntimeSubstrateActor)
	status.SubstrateActor = &controlv1alpha1.AgentRunSubstrateActorStatus{
		ActorName: handle.Name,
		ActorID:   handle.ID,
		State:     string(handle.State),
	}
	if status.StartedAt == nil {
		status.StartedAt = &now
	}
	status.Backend = string(agentRunBackendKind(effective))
	prompt := strings.TrimSpace(effective.Spec.Prompt)
	if prompt == "" {
		return r.failSubstrateGenerate(ctx, original, obj, status, now, "AgentRun spec.prompt is empty; generate-on-actor cannot complete this turn.")
	}
	result, err := r.SubstrateGenerate.Generate(ctx, substrate.GenerateRequest{
		ActorName: handle.Name,
		Atespace:  substrateGenerateAtespace(r, handle),
		SessionID: threadID,
		Prompt:    prompt,
	})
	if err != nil {
		if errors.Is(err, substrate.ErrGenerateTransient) {
			status.CompletedAt = nil
			status.Phase = controlv1alpha1.AgentRunPhaseRunning
			status.Error = ""
			binding := "created a cold Substrate actor"
			if warm {
				binding = "resumed a warm Substrate actor"
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "SubstrateActorBound",
				Message:            "Generate-on-actor " + binding + " " + handle.Name + "; retrying atenet stream. No Kubernetes Job was created.",
			})
			obj.Status = *status
			if patchErr := r.Status().Patch(ctx, obj, client.MergeFrom(original)); patchErr != nil {
				return r.patchAgentRunStatus(ctx, original, obj, true)
			}
			return ctrl.Result{}, err
		}
		return r.failSubstrateGenerate(ctx, original, obj, status, now, "generate-on-actor failed; no Kubernetes Job was created.")
	}
	output := agentRunTrimOutput(result.Text)
	if strings.TrimSpace(output) == "" {
		return r.failSubstrateGenerate(ctx, original, obj, status, now, "generate-on-actor returned an empty reply; no Kubernetes Job was created.")
	}
	status.Output = output
	status.PromptHash = shortHash(prompt)
	status.CompletedAt = &now
	status.Error = ""
	status.Decision = &controlv1alpha1.AgentRunDecisionStatus{
		Classification: "completed",
		Action:         firstNonEmpty(strings.TrimSpace(status.Intent), string(agentRunIntent(effective))),
		Summary:        agentRunOutputSummary(status.Output),
	}
	status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
	status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentRunReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "SubstrateActorGenerated",
		Message:            "Generate-on-actor streamed the frozen prompt through atenet; no Kubernetes Job was created.",
	})
	obj.Status = *status
	patched, patchErr := r.patchAgentRunStatus(ctx, original, obj, false)
	if patchErr == nil {
		r.suspendSubstrateActorOnTerminal(ctx, obj)
	}
	return patched, patchErr
}

func (r *AgentRunReconciler) failSubstrateGenerate(ctx context.Context, original, obj *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, now metav1.Time, message string) (ctrl.Result, error) {
	status.Phase = controlv1alpha1.AgentRunPhaseFailed
	status.CompletedAt = &now
	status.Error = message
	status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentRunReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "SubstrateActorGenerateFailed",
		Message:            message,
	})
	obj.Status = *status
	patched, err := r.patchAgentRunStatus(ctx, original, obj, false)
	if err == nil {
		r.suspendSubstrateActorOnTerminal(ctx, obj)
	}
	return patched, err
}

// suspendSubstrateActorOnTerminal releases the actor worker once the run goes
// idle (terminal). It is best-effort: suspend only multiplexes warm workers,
// so a failure must not block terminal bookkeeping or requeue terminal
// reconciliation. Missing actors are already gone and report success.
func (r *AgentRunReconciler) suspendSubstrateActorOnTerminal(ctx context.Context, run *controlv1alpha1.AgentRun) {
	if run == nil || !run.Spec.Harness.Execution.UsesSubstrateActors() {
		return
	}
	live := r.substrateLiveClient()
	if live == nil {
		return
	}
	threadID := substrateThreadID(run)
	var suspendOnIdle *bool
	if run.Spec.Harness.Execution.Substrate != nil {
		suspendOnIdle = run.Spec.Harness.Execution.Substrate.SuspendOnIdle
	}
	// Best-effort by design; see the function comment.
	_, _ = substrate.SuspendIdleActor(ctx, live, run.Namespace, substrate.ActorNameForThread(threadID), suspendOnIdle)
}
