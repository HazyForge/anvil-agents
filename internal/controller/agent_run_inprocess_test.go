package controller

import (
	"strings"
	"testing"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice 1 of the standing in-process harness plane is API-first: selection,
// the session/stream contract, and a Fake backend (internal/standing). The
// controller holds well-formed InProcess runs without creating a Job until a
// later slice wires a live backend behind an explicit opt-in gate. The
// default Job path and the optional SubstrateActor plane are untouched. See
// docs/standing-inprocess-harness.md.
func TestAgentRunInProcessHoldKeepsJobUncreated(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeInProcess
	phase, reason, message := agentRunBlockingValidation(run)
	if phase != agents.AgentRunPhaseNeedsHuman || reason != "InProcessNotWired" || message == "" {
		t.Fatalf("in-process hold = %q/%q/%q, want NeedsHuman/InProcessNotWired with guidance", phase, reason, message)
	}
	if got := run.Spec.Harness.Execution.EffectiveRuntime(); got != agents.AgentRunExecutionRuntimeInProcess {
		t.Fatalf("runtime = %q, want InProcess", got)
	}
}

func TestAgentRunInProcessShapeFailsClosed(t *testing.T) {
	t.Parallel()

	sectionOnInProcess := substrateSpikeRun()
	sectionOnInProcess.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeInProcess
	sectionOnInProcess.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"}
	if phase, reason, _ := agentRunBlockingValidation(sectionOnInProcess); phase != agents.AgentRunPhaseFailed || reason != "InvalidSubstrateSpec" {
		t.Fatalf("substrate section on InProcess runtime = %q/%q, want Failed/InvalidSubstrateSpec", phase, reason)
	}
}

func TestAgentRunBackendMisconfigurationSurfacesBeforeInProcessHold(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Spec.Harness.Backend.Image = ""
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeInProcess
	phase, reason, _ := agentRunBlockingValidation(run)
	if phase != agents.AgentRunPhaseNeedsHuman || reason != "CustomImageNotConfigured" {
		t.Fatalf("adapter error = %q/%q, want NeedsHuman/CustomImageNotConfigured before the in-process hold", phase, reason)
	}
}

func TestAgentRunMergeExecutionPreservesInProcess(t *testing.T) {
	t.Parallel()

	profile := agents.AgentRunHarnessExecutionSpec{Runtime: agents.AgentRunExecutionRuntimeInProcess}
	merged := agentRunMergeExecution(profile, agents.AgentRunHarnessExecutionSpec{})
	if !merged.UsesInProcess() {
		t.Fatal("merge dropped the profile InProcess runtime")
	}
	overlay := agents.AgentRunHarnessExecutionSpec{Runtime: agents.AgentRunExecutionRuntimeJob}
	merged = agentRunMergeExecution(profile, overlay)
	if merged.UsesInProcess() || merged.EffectiveRuntime() != agents.AgentRunExecutionRuntimeJob {
		t.Fatalf("merge overlay runtime = %q, want explicit Job override to win", merged.EffectiveRuntime())
	}
}

// Slice 4 wires the controller half of the standing-turn claim: a live API
// claim for the run's chat turn yields StandingClaimed (still no Job) instead
// of racing the live stream with InProcessNotWired. Absent, malformed,
// mismatched, or stale claims keep the original hold byte-identical.

func inProcessClaimRun(annotations map[string]string) *agents.AgentRun {
	run := substrateSpikeRun()
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeInProcess
	run.Labels = map[string]string{agents.AgentRunChatTurnLabel: "turn-1"}
	run.Annotations = annotations
	return run
}

func standingClaimValue(t *testing.T, turn, owner string, at time.Time) string {
	t.Helper()
	raw, err := standing.EncodeClaim(standing.Claim{TurnID: turn, Owner: owner, AtUnix: at.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAgentRunInProcessClaimYieldsStandingClaimed(t *testing.T) {
	t.Parallel()

	run := inProcessClaimRun(map[string]string{
		agents.AgentRunStandingClaimAnnotation: standingClaimValue(t, "turn-1", "api-a/123", time.Now()),
	})
	phase, reason, message := agentRunBlockingValidation(run)
	if phase != agents.AgentRunPhaseNeedsHuman || reason != "StandingClaimed" {
		t.Fatalf("claimed hold = %q/%q, want NeedsHuman/StandingClaimed", phase, reason)
	}
	if !strings.Contains(message, "api-a/123") || !strings.Contains(message, "turn-1") {
		t.Fatalf("yield message = %q, want owner and turn carried", message)
	}
}

func TestAgentRunInProcessClaimFallbacksKeepNotWired(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cases := map[string]map[string]string{
		"no annotations": nil,
		"no claim key":   {"example.com/other": "value"},
		"malformed":      {agents.AgentRunStandingClaimAnnotation: "not-json"},
		"other turn": {
			agents.AgentRunStandingClaimAnnotation: standingClaimValue(t, "turn-2", "api-a/123", now),
		},
		"stale": {
			agents.AgentRunStandingClaimAnnotation: standingClaimValue(t, "turn-1", "api-a/123", now.Add(-10*time.Minute)),
		},
	}
	for name, annotations := range cases {
		run := inProcessClaimRun(annotations)
		phase, reason, _ := agentRunBlockingValidation(run)
		if phase != agents.AgentRunPhaseNeedsHuman || reason != "InProcessNotWired" {
			t.Fatalf("%s: hold = %q/%q, want NeedsHuman/InProcessNotWired", name, phase, reason)
		}
	}
}

func TestAgentRunInProcessClaimWithoutTurnLabelKeepsNotWired(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeInProcess
	run.Annotations = map[string]string{
		agents.AgentRunStandingClaimAnnotation: standingClaimValue(t, "turn-1", "api-a/123", time.Now()),
	}
	if phase, reason, _ := agentRunBlockingValidation(run); phase != agents.AgentRunPhaseNeedsHuman || reason != "InProcessNotWired" {
		t.Fatalf("unlabeled hold = %q/%q, want NeedsHuman/InProcessNotWired", phase, reason)
	}
}

func TestAgentRunClaimIgnoredOffInProcess(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Labels = map[string]string{agents.AgentRunChatTurnLabel: "turn-1"}
	run.Annotations = map[string]string{
		agents.AgentRunStandingClaimAnnotation: standingClaimValue(t, "turn-1", "api-a/123", time.Now()),
	}
	if phase, reason, _ := agentRunBlockingValidation(run); phase != "" || reason != "" {
		t.Fatalf("Job-plane hold = %q/%q, want no hold", phase, reason)
	}
}
