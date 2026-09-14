package controller

import (
	"reflect"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrimeBackendBuildsIndependentNativeJob(t *testing.T) {
	run := &agents.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "prime-chat", Namespace: "agents"}, Spec: agents.AgentRunSpec{
		SourceRef: agents.AgentRunSourceRef{Kind: "AgentSchedule", Name: "prime-chat"},
		Harness: agents.AgentRunHarnessSpec{Backend: agents.AgentRunHarnessBackendSpec{
			Kind: agents.AgentRunHarnessBackendPrimeAgent, ModelProvider: agents.AgentRunModelProviderDeepSeek,
			ProviderAuthMode: agents.AgentRunProviderAuthModeAPIKey,
			PrimeAgent:       &agents.AgentRunPrimeBackendSpec{Model: "deepseek-v4-flash", Thinking: "high", Mode: "json", NoSession: true, AdditionalArgs: []string{"--tools", "ipython"}},
		}},
	}}
	if phase, reason, message := agentRunBlockingValidation(run); phase != "" {
		t.Fatalf("Prime rejected: %s %s %s", phase, reason, message)
	}
	job := agentRunJob(run, "harness", "context", nil)
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != agentRunDefaultPrimeAgentImage {
		t.Fatalf("Prime image = %q", container.Image)
	}
	env := map[string]string{}
	for _, entry := range container.Env {
		env[entry.Name] = entry.Value
	}
	for key, want := range map[string]string{
		"ANVIL_PRIME_MODEL_PROVIDER": "deepseek", "ANVIL_PRIME_PROVIDER_AUTH_MODE": "apiKey", "ANVIL_PRIME_PROVIDER": "deepseek",
		"ANVIL_PRIME_MODEL": "deepseek-v4-flash", "ANVIL_PRIME_THINKING": "high", "ANVIL_PRIME_MODE": "json",
		"ANVIL_PRIME_NO_SESSION": "true", "ANVIL_PRIME_ADDITIONAL_ARGS_JSON": `["--tools","ipython"]`,
	} {
		if env[key] != want {
			t.Errorf("env[%s] = %q, want %q", key, env[key], want)
		}
	}
	if _, exists := env["ANVIL_PI_PROVIDER"]; exists {
		t.Fatal("Prime job inherited Pi provider environment")
	}
	if agentRunBackendModel(run) != "deepseek-v4-flash" || agentRunModelFromJob(job) != "deepseek-v4-flash" {
		t.Fatal("Prime model absent from status recovery")
	}
	if mount := agentDataVolumeMountPath(&agents.AgentDataVolume{Spec: agents.AgentDataVolumeSpec{Backend: agents.AgentRunHarnessBackendPrimeAgent}}); mount != "/opt/anvil/prime" {
		t.Fatalf("Prime home = %q", mount)
	}
	run.Spec.Harness.Backend.ModelProvider = ""
	if got := agentRunPrimeProvider(run, nil); got != "" {
		t.Fatalf("Prime must not inherit Pi's xAI default: %q", got)
	}
	if got := agentRunPrimeProvider(run, &agents.AgentRunPrimeBackendSpec{Provider: " custom-provider "}); got != "custom-provider" {
		t.Fatalf("native provider overridden: %q", got)
	}
}

func TestPrimeBackendCompositionPreservesProfileAndOverrides(t *testing.T) {
	profile := agents.AgentRunHarnessBackendSpec{Kind: agents.AgentRunHarnessBackendPrimeAgent, PrimeAgent: &agents.AgentRunPrimeBackendSpec{
		Provider: "deepseek", Model: "profile-model", Thinking: "high", Mode: "json", AdditionalArgs: []string{"--tools", "ipython"},
	}}
	run := agents.AgentRunHarnessBackendSpec{PrimeAgent: &agents.AgentRunPrimeBackendSpec{Model: "override-model", NoSession: true, AdditionalArgs: []string{"--no-extensions"}}}
	merged := agentRunMergeBackend(profile, run)
	if merged.Kind != agents.AgentRunHarnessBackendPrimeAgent || merged.PrimeAgent == nil {
		t.Fatalf("Prime composition lost: %#v", merged)
	}
	if merged.PrimeAgent.Model != "override-model" || merged.PrimeAgent.Provider != "deepseek" || merged.PrimeAgent.Thinking != "high" || merged.PrimeAgent.Mode != "json" || !merged.PrimeAgent.NoSession {
		t.Fatalf("Prime composition = %#v", merged.PrimeAgent)
	}
	if !reflect.DeepEqual(merged.PrimeAgent.AdditionalArgs, []string{"--tools", "ipython", "--no-extensions"}) {
		t.Fatalf("merged argv = %#v", merged.PrimeAgent.AdditionalArgs)
	}
	merged.PrimeAgent.AdditionalArgs[0] = "changed"
	if profile.PrimeAgent.AdditionalArgs[0] != "--tools" {
		t.Fatal("composition mutated profile")
	}
}

func TestPrimeRunnerImageConfiguration(t *testing.T) {
	t.Setenv(primeAgentRunnerImageEnv, "registry.invalid/prime@sha256:example")
	opts := DefaultOptions()
	run := &agents.AgentRun{Spec: agents.AgentRunSpec{Harness: agents.AgentRunHarnessSpec{Backend: agents.AgentRunHarnessBackendSpec{Kind: agents.AgentRunHarnessBackendPrimeAgent}}}}
	if got := agentRunImageWithOptions(run, opts); got != "registry.invalid/prime@sha256:example" {
		t.Fatalf("configured Prime image = %q", got)
	}
	run.Spec.Harness.Backend.Image = "explicit-prime-image"
	if got := agentRunImageWithOptions(run, opts); got != "explicit-prime-image" {
		t.Fatalf("explicit Prime image = %q", got)
	}
}
