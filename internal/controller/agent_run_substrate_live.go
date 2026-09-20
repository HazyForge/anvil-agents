package controller

import (
	"context"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

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

// reconcileSubstrateActorRun binds one SubstrateActor run to its warm actor
// and records the binding in status. It never creates a Kubernetes Job: the
// Job-launch receipt fields (PlannedJobRef, JobCreateAttemptedAt, JobRef) stay
// empty so the single-execution guards keep treating the run as unlaunched on
// the Job plane. Backend failures return an error so controller-runtime backs
// off and retries; the run stays Running, never Failed, on transient gateway
// errors.
func (r *AgentRunReconciler) reconcileSubstrateActorRun(ctx context.Context, original, obj *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, effective *controlv1alpha1.AgentRun, now metav1.Time) (ctrl.Result, error) {
	if agentRunPhaseTerminal(obj.Status.Phase) || agentRunPhaseTerminal(status.Phase) {
		return ctrl.Result{}, nil
	}
	live := r.substrateLiveClient()
	if live == nil {
		return ctrl.Result{}, nil
	}
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
	status.CompletedAt = nil
	status.Phase = controlv1alpha1.AgentRunPhaseRunning
	status.Error = ""
	binding := "created a cold Substrate actor"
	if warm {
		binding = "resumed a warm Substrate actor"
	}
	message := "Standing-chat turn " + binding + " " + handle.Name + " for thread " + threadID + "; no Kubernetes Job was created."
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentRunReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "SubstrateActorBound",
		Message:            message,
	})
	obj.Status = *status
	return r.patchAgentRunStatus(ctx, original, obj, true)
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
