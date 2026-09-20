package runapi

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

func TestStandingAPIHoldIsNotStreamComplete(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseNeedsHuman)
	run.Spec.Harness.Execution.Runtime = agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor
	run.Labels = map[string]string{agentsv1alpha1.AgentRunChatTurnLabel: "turn-1"}
	run.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: standingHoldStandingClaimed,
		Message: "execution.runtime SubstrateActor standing turn is owned by the API replica",
	}}
	if !standingAPIHold(run) {
		t.Fatal("StandingClaimed substrate run must be an API hold")
	}
	if agentRunStreamComplete(run) {
		t.Fatal("standing API hold must keep the AgentRun stream open")
	}
	view := NewAgentRunView(run, false)
	if view.Phase != agentsv1alpha1.AgentRunPhaseRunning {
		t.Fatalf("standing hold view phase = %q, want Running so Desktop does not paint a failed turn", view.Phase)
	}
}

func TestStandingUnwiredHoldIsNotStreamComplete(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseNeedsHuman)
	run.Spec.Harness.Execution.Runtime = agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor
	run.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: standingHoldSubstrateActorNotWired,
	}}
	if !standingAPIHold(run) || agentRunStreamComplete(run) {
		t.Fatal("SubstrateActorNotWired must stay open for the standing claim to win")
	}
	if phase := standingViewPhase(run); phase != agentsv1alpha1.AgentRunPhaseRunning {
		t.Fatalf("unwired view phase = %q, want Running", phase)
	}
}

func TestJobNeedsHumanRemainsStreamComplete(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseNeedsHuman)
	if standingAPIHold(run) {
		t.Fatal("Job-plane NeedsHuman is not a standing API hold")
	}
	if !agentRunStreamComplete(run) {
		t.Fatal("Job-plane NeedsHuman must still close the run stream")
	}
	if phase := standingViewPhase(run); phase != agentsv1alpha1.AgentRunPhaseNeedsHuman {
		t.Fatalf("Job view phase = %q, want NeedsHuman", phase)
	}
}

func TestStandingLiveClaimIsAPIHold(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseNeedsHuman)
	run.Spec.Harness.Execution.Runtime = agentsv1alpha1.AgentRunExecutionRuntimeInProcess
	run.Labels = map[string]string{agentsv1alpha1.AgentRunChatTurnLabel: "turn-live"}
	value, err := standing.EncodeClaim(standing.Claim{TurnID: "turn-live", Owner: "api-0", AtUnix: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	run.Annotations = map[string]string{agentsv1alpha1.AgentRunStandingClaimAnnotation: value}
	if !standingAPIHold(run) || agentRunStreamComplete(run) {
		t.Fatal("live standing claim must keep the stream open even before Ready/StandingClaimed")
	}
}
