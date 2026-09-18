package controller

import (
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
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
