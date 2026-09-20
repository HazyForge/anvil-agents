package runapi

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

const (
	standingHoldStandingClaimed        = "StandingClaimed"
	standingHoldInProcessNotWired      = "InProcessNotWired"
	standingHoldSubstrateActorNotWired = "SubstrateActorNotWired"
)

// standingExecution reports whether the run is on the standing (no-Job)
// execution plane. Spec is the source of truth; status.executionRuntime
// covers runs that already recorded the standing path.
func standingExecution(run *agentsv1alpha1.AgentRun) bool {
	if run == nil {
		return false
	}
	if run.Spec.Harness.Execution.UsesInProcess() || run.Spec.Harness.Execution.UsesSubstrateActors() {
		return true
	}
	switch agentsv1alpha1.AgentRunExecutionRuntime(strings.TrimSpace(run.Status.ExecutionRuntime)) {
	case agentsv1alpha1.AgentRunExecutionRuntimeInProcess, agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor:
		return true
	default:
		return false
	}
}

func standingReadyHoldReason(run *agentsv1alpha1.AgentRun) string {
	if run == nil {
		return ""
	}
	for i := len(run.Status.Conditions) - 1; i >= 0; i-- {
		condition := run.Status.Conditions[i]
		if condition.Type == "Ready" && condition.Status == metav1.ConditionFalse {
			return strings.TrimSpace(condition.Reason)
		}
	}
	return ""
}

// standingAPIHold reports that NeedsHuman is the controller yielding to the
// API standing path (StandingClaimed / not-wired), not a human-attention
// failure. Desktop must keep waiting; the AgentRun event stream must stay
// open until Succeeded or a real failure.
func standingAPIHold(run *agentsv1alpha1.AgentRun) bool {
	if !standingExecution(run) {
		return false
	}
	switch standingReadyHoldReason(run) {
	case standingHoldStandingClaimed, standingHoldInProcessNotWired, standingHoldSubstrateActorNotWired:
		return true
	}
	turnID := strings.TrimSpace(run.Labels[agentsv1alpha1.AgentRunChatTurnLabel])
	if turnID == "" {
		return false
	}
	_, live := standing.ClaimForTurn(run.Annotations, turnID, time.Now(), standing.ClaimTTL)
	return live
}

func agentRunStreamComplete(run *agentsv1alpha1.AgentRun) bool {
	if run == nil {
		return false
	}
	switch run.Status.Phase {
	case agentsv1alpha1.AgentRunPhaseSucceeded, agentsv1alpha1.AgentRunPhaseFailed:
		return true
	case agentsv1alpha1.AgentRunPhaseNeedsHuman:
		return !standingAPIHold(run)
	default:
		return false
	}
}

func standingViewPhase(run *agentsv1alpha1.AgentRun) agentsv1alpha1.AgentRunPhase {
	if run == nil {
		return ""
	}
	if run.Status.Phase == agentsv1alpha1.AgentRunPhaseNeedsHuman && standingAPIHold(run) {
		return agentsv1alpha1.AgentRunPhaseRunning
	}
	return run.Status.Phase
}
