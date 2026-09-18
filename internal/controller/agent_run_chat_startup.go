package controller

import (
	"context"
	"fmt"
	"time"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	agentRunChatStartupBudget  = 5 * time.Minute
	agentRunChatTurnLabel      = controlv1alpha1.AgentRunChatTurnLabel
	agentRunChatStartupReason  = "ChatStartupDeadlineExceeded"
	agentRunChatStartupMessage = "This chat turn did not start within 5 minutes. No runner Job was found. Send a new message to try again."
)

// The budget starts when the append-only AgentRun is created, not when its
// message entered a deferred inbox. Established execution receipts are exempt.
func agentRunChatStartupExpired(run *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, now time.Time) bool {
	if run.Spec.SourceRef.Kind != "ChatThread" || run.CreationTimestamp.IsZero() || agentRunPhaseTerminal(status.Phase) || status.Phase == controlv1alpha1.AgentRunPhaseRunning || status.StartedAt != nil || status.JobRef != nil || status.JobUID != "" || status.JobCreateAttemptedAt != nil || status.RunnerPodRef != nil {
		return false
	}
	thread, turn := run.Labels[controlv1alpha1.AgentRunChatThreadLabel], run.Labels[agentRunChatTurnLabel]
	if thread == "" || turn == "" || thread != run.Spec.SourceRef.Name || len(validation.IsValidLabelValue(thread)) != 0 || len(validation.IsValidLabelValue(turn)) != 0 {
		return false
	}
	if run.Spec.SourceRef.Namespace != "" && run.Spec.SourceRef.Namespace != run.Namespace {
		return false
	}
	return !now.Before(run.CreationTimestamp.Add(agentRunChatStartupBudget))
}

// Called only after normal Job recovery returned no Job. Confirm absence using
// an uncached ownership scan, including deleting Jobs and Jobs whose discovery
// label drifted. Those still represent launched work and must not be expired.
func (r *AgentRunReconciler) reconcileAgentRunChatStartupDeadline(ctx context.Context, original, run *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, now metav1.Time) (bool, ctrl.Result, error) {
	if !agentRunChatStartupExpired(run, status, now.Time) {
		return false, ctrl.Result{}, nil
	}
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	jobs := &batchv1.JobList{}
	if err := reader.List(ctx, jobs, client.InNamespace(run.Namespace)); err != nil {
		return true, ctrl.Result{}, fmt.Errorf("confirm chat startup Job absence: %w", err)
	}
	for i := range jobs.Items {
		if agentRunControllerOwnerMatches(&jobs.Items[i], run) {
			return true, ctrl.Result{RequeueAfter: agentRunPollInterval}, nil
		}
	}
	status.Phase = controlv1alpha1.AgentRunPhaseFailed
	status.CompletedAt = &now
	status.Error = agentRunChatStartupMessage
	status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: agentRunReady, Status: metav1.ConditionFalse, ObservedGeneration: run.Generation, LastTransitionTime: now, Reason: agentRunChatStartupReason, Message: agentRunChatStartupMessage})
	run.Status = *status
	// A concurrent launch must first persist a create-attempt receipt. Reject a
	// stale status patch if that receipt arrived during the authoritative scan.
	if err := r.Status().Patch(ctx, run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return true, ctrl.Result{Requeue: true}, nil
		}
		return true, ctrl.Result{}, err
	}
	return true, ctrl.Result{}, nil
}
