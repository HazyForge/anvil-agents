package controller

import (
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func substrateSpikeRun() *agents.AgentRun {
	return &agents.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "standing-chat-turn", Namespace: "agents"},
		Spec: agents.AgentRunSpec{
			SourceRef: agents.AgentRunSourceRef{Kind: "ChatThread", Name: "thread-1"},
			Harness: agents.AgentRunHarnessSpec{
				Backend: agents.AgentRunHarnessBackendSpec{
					Kind:  agents.AgentRunHarnessBackendCustom,
					Image: "registry.invalid/standing-chat:latest",
				},
			},
		},
	}
}

func TestAgentRunSubstrateHoldKeepsJobUncreated(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeSubstrateActor
	run.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"}
	phase, reason, message := agentRunBlockingValidation(run)
	if phase != agents.AgentRunPhaseNeedsHuman || reason != "SubstrateActorNotWired" || message == "" {
		t.Fatalf("substrate hold = %q/%q/%q, want NeedsHuman/SubstrateActorNotWired with guidance", phase, reason, message)
	}
}

func TestAgentRunDefaultRuntimeKeepsJobPath(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	if phase, reason, message := agentRunBlockingValidation(run); phase != "" {
		t.Fatalf("default Job runtime rejected: %s %s %s", phase, reason, message)
	}
	if got := run.Spec.Harness.Execution.EffectiveRuntime(); got != agents.AgentRunExecutionRuntimeJob {
		t.Fatalf("default runtime = %q, want Job", got)
	}
}

func TestAgentRunSubstrateShapeFailsClosed(t *testing.T) {
	t.Parallel()

	missingSection := substrateSpikeRun()
	missingSection.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeSubstrateActor
	if phase, reason, _ := agentRunBlockingValidation(missingSection); phase != agents.AgentRunPhaseFailed || reason != "InvalidSubstrateSpec" {
		t.Fatalf("missing substrate section = %q/%q, want Failed/InvalidSubstrateSpec", phase, reason)
	}

	sectionOnJob := substrateSpikeRun()
	sectionOnJob.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"}
	if phase, reason, _ := agentRunBlockingValidation(sectionOnJob); phase != agents.AgentRunPhaseFailed || reason != "InvalidSubstrateSpec" {
		t.Fatalf("substrate section on Job runtime = %q/%q, want Failed/InvalidSubstrateSpec", phase, reason)
	}
}

func TestAgentRunBackendMisconfigurationSurfacesBeforeSubstrateHold(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	run.Spec.Harness.Backend.Image = ""
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeSubstrateActor
	run.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"}
	phase, reason, _ := agentRunBlockingValidation(run)
	if phase != agents.AgentRunPhaseNeedsHuman || reason != "CustomImageNotConfigured" {
		t.Fatalf("adapter error = %q/%q, want NeedsHuman/CustomImageNotConfigured before the substrate hold", phase, reason)
	}
}

func TestAgentRunMergeExecutionPreservesSubstrate(t *testing.T) {
	t.Parallel()

	profile := agents.AgentRunHarnessExecutionSpec{
		Runtime:   agents.AgentRunExecutionRuntimeSubstrateActor,
		Substrate: &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"},
	}
	merged := agentRunMergeExecution(profile, agents.AgentRunHarnessExecutionSpec{})
	if !merged.UsesSubstrateActors() {
		t.Fatal("merge dropped the profile SubstrateActor runtime")
	}
	if merged.Substrate == nil || merged.Substrate.ActorClass != "standing-chat" || merged.Substrate.Pool != "warm" {
		t.Fatalf("merge dropped substrate tuning: %+v", merged.Substrate)
	}

	overlay := agents.AgentRunHarnessExecutionSpec{
		Substrate: &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "overflow"},
	}
	merged = agentRunMergeExecution(profile, overlay)
	if merged.Substrate.Pool != "overflow" || merged.Substrate.ActorClass != "standing-chat" {
		t.Fatalf("merge overlay = %+v, want pool override with class preserved via explicit overlay", merged.Substrate)
	}
	merged.Substrate.Pool = "mutated"
	if profile.Substrate.Pool != "warm" {
		t.Fatal("merge aliased the profile substrate section")
	}
}

func TestAgentRunHarnessProfileSwapSelectsSubstrate(t *testing.T) {
	t.Parallel()

	profile := &agents.AgentRunProfile{Spec: agents.AgentRunProfileSpec{
		Harness: agents.AgentRunHarnessSpec{
			Backend:   agents.AgentRunHarnessBackendSpec{Kind: agents.AgentRunHarnessBackendCodex},
			Execution: agents.AgentRunHarnessExecutionSpec{ServiceAccountName: "profile-runner"},
		},
	}}
	harnessProfile := &agents.AgentHarnessProfile{Spec: agents.AgentHarnessProfileSpec{
		Backend: agents.AgentRunHarnessBackendSpec{Kind: agents.AgentRunHarnessBackendOpenCode},
		Execution: agents.AgentRunHarnessExecutionSpec{
			Runtime:            agents.AgentRunExecutionRuntimeSubstrateActor,
			Substrate:          &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
			ServiceAccountName: "substrate-runner",
		},
	}}
	run := &agents.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "standing-chat-turn", Namespace: "agents"},
		Spec: agents.AgentRunSpec{
			HarnessProfileRef: &agents.NamespacedObjectReference{Name: "substrate-chat"},
		},
	}
	effective := agentRunHarnessWithProfile(profile, run, harnessProfile)
	if !effective.Execution.UsesSubstrateActors() {
		t.Fatal("run-local harness profile swap must select the SubstrateActor runtime")
	}
	if effective.Execution.ServiceAccountName != "substrate-runner" {
		t.Fatalf("service account = %q, want the swapped harness profile envelope", effective.Execution.ServiceAccountName)
	}
	if effective.Backend.Kind != agents.AgentRunHarnessBackendOpenCode {
		t.Fatalf("backend = %q, want the swapped harness profile adapter", effective.Backend.Kind)
	}
}
