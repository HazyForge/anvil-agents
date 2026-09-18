package v1alpha1

import "testing"

func TestExecutionRuntimeDefaultsToJob(t *testing.T) {
	t.Parallel()

	var nilSpec *AgentRunHarnessExecutionSpec
	if got := nilSpec.EffectiveRuntime(); got != AgentRunExecutionRuntimeJob {
		t.Fatalf("nil execution runtime = %q, want Job", got)
	}
	for _, raw := range []string{"", "  ", "job", "JOB", "UnknownFuturePlane"} {
		spec := &AgentRunHarnessExecutionSpec{Runtime: AgentRunExecutionRuntime(raw)}
		if got := spec.EffectiveRuntime(); got != AgentRunExecutionRuntimeJob {
			t.Fatalf("runtime %q folds to %q, want Job", raw, got)
		}
		if spec.UsesSubstrateActors() {
			t.Fatalf("runtime %q must not select Substrate actors", raw)
		}
	}
	substrate := &AgentRunHarnessExecutionSpec{
		Runtime:   AgentRunExecutionRuntimeSubstrateActor,
		Substrate: &AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
	}
	if got := substrate.EffectiveRuntime(); got != AgentRunExecutionRuntimeSubstrateActor {
		t.Fatalf("substrate runtime = %q, want SubstrateActor", got)
	}
	if !substrate.UsesSubstrateActors() {
		t.Fatal("substrate execution must select Substrate actors")
	}
	inProcess := &AgentRunHarnessExecutionSpec{Runtime: AgentRunExecutionRuntimeInProcess}
	if got := inProcess.EffectiveRuntime(); got != AgentRunExecutionRuntimeInProcess {
		t.Fatalf("in-process runtime = %q, want InProcess", got)
	}
	if !inProcess.UsesInProcess() {
		t.Fatal("in-process execution must select the standing in-process plane")
	}
	if substrate.UsesInProcess() || inProcess.UsesSubstrateActors() {
		t.Fatal("execution planes must not overlap: SubstrateActor and InProcess are distinct")
	}
	job := &AgentRunHarnessExecutionSpec{}
	if job.UsesInProcess() {
		t.Fatal("empty execution must not select the in-process plane")
	}
}

func TestValidateSubstrateExecution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		spec       *AgentRunHarnessExecutionSpec
		wantReason string
	}{
		{name: "nil spec is valid", spec: nil},
		{name: "empty spec is valid", spec: &AgentRunHarnessExecutionSpec{}},
		{
			name: "substrate with section is valid",
			spec: &AgentRunHarnessExecutionSpec{
				Runtime:   AgentRunExecutionRuntimeSubstrateActor,
				Substrate: &AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"},
			},
		},
		{
			name:       "substrate without section is invalid",
			spec:       &AgentRunHarnessExecutionSpec{Runtime: AgentRunExecutionRuntimeSubstrateActor},
			wantReason: "InvalidSubstrateSpec",
		},
		{
			name: "substrate section on Job runtime is invalid",
			spec: &AgentRunHarnessExecutionSpec{
				Substrate: &AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
			},
			wantReason: "InvalidSubstrateSpec",
		},
		{
			name: "in-process without section is valid",
			spec: &AgentRunHarnessExecutionSpec{Runtime: AgentRunExecutionRuntimeInProcess},
		},
		{
			name: "substrate section on InProcess runtime is invalid",
			spec: &AgentRunHarnessExecutionSpec{
				Runtime:   AgentRunExecutionRuntimeInProcess,
				Substrate: &AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
			},
			wantReason: "InvalidSubstrateSpec",
		},
		{
			name: "whitespace actor class is invalid",
			spec: &AgentRunHarnessExecutionSpec{
				Runtime:   AgentRunExecutionRuntimeSubstrateActor,
				Substrate: &AgentRunSubstrateActorSpec{ActorClass: " standing chat "},
			},
			wantReason: "InvalidSubstrateSpec",
		},
		{
			name: "whitespace pool is invalid",
			spec: &AgentRunHarnessExecutionSpec{
				Runtime:   AgentRunExecutionRuntimeSubstrateActor,
				Substrate: &AgentRunSubstrateActorSpec{Pool: "warm pool"},
			},
			wantReason: "InvalidSubstrateSpec",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason, message := ValidateSubstrateExecution(tc.spec)
			if tc.wantReason == "" {
				if reason != "" {
					t.Fatalf("validate = %q/%q, want valid", reason, message)
				}
				return
			}
			if reason != tc.wantReason || message == "" {
				t.Fatalf("validate = %q/%q, want reason %q with a message", reason, message, tc.wantReason)
			}
		})
	}
}
