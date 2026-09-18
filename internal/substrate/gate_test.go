package substrate

import (
	"testing"
)

func TestGateDefaultsOff(t *testing.T) {
	t.Setenv(GateEnabledEnvVar, "")
	t.Setenv(GateEndpointEnvVar, "")
	t.Setenv(GateTokenEnvVar, "")
	gate := GateConfigFromEnv()
	if gate.Enabled {
		t.Fatal("gate must default to disabled")
	}
	if gate.LiveEnabled() {
		t.Fatal("gate without endpoint must not enable live dispatch")
	}
	if _, ok, err := NewLiveClientFromEnv(); err != nil || ok {
		t.Fatalf("live client from disabled gate = ok=%v err=%v, want disabled", ok, err)
	}
}

func TestGateRequiresExplicitOptIn(t *testing.T) {
	t.Setenv(GateEnabledEnvVar, "true")
	t.Setenv(GateEndpointEnvVar, "")
	if got := GateConfigFromEnv(); !got.Enabled || got.LiveEnabled() {
		t.Fatalf("gate without endpoint = %+v, want enabled-but-not-live", got)
	}
	if _, ok, err := NewLiveClientFromEnv(); err != nil || ok {
		t.Fatalf("live client without endpoint = ok=%v err=%v, want disabled", ok, err)
	}
}

func TestGateTruthySpellings(t *testing.T) {
	for _, raw := range []string{"1", "true", "TRUE", "yes", "on", " True "} {
		t.Setenv(GateEnabledEnvVar, raw)
		t.Setenv(GateEndpointEnvVar, "http://substrate-gateway.substrate:8080")
		if !GateConfigFromEnv().LiveEnabled() {
			t.Fatalf("gate value %q must enable live dispatch", raw)
		}
	}
}

func TestGateRejectsFalsySpellings(t *testing.T) {
	for _, raw := range []string{"0", "false", "no", "off", "maybe", "enabled"} {
		t.Setenv(GateEnabledEnvVar, raw)
		t.Setenv(GateEndpointEnvVar, "http://substrate-gateway.substrate:8080")
		if GateConfigFromEnv().LiveEnabled() {
			t.Fatalf("gate value %q must not enable live dispatch", raw)
		}
	}
}

func TestThreadIDForRunPrefersChatThread(t *testing.T) {
	t.Parallel()

	if got := ThreadIDForRun("ChatThread", "thread-1", "chat-turn-abc"); got != "thread-1" {
		t.Fatalf("chat thread id = %q, want thread-1", got)
	}
	if got := ThreadIDForRun("Manual", "thread-1", "manual-run"); got != "manual-run" {
		t.Fatalf("non-chat run id = %q, want manual-run", got)
	}
	if got := ThreadIDForRun("ChatThread", "", "chat-turn-abc"); got != "chat-turn-abc" {
		t.Fatalf("chat run without thread = %q, want the run name fallback", got)
	}
}

func TestShouldSuspendOnIdleDefaultsTrue(t *testing.T) {
	t.Parallel()

	if !ShouldSuspendOnIdle(nil) {
		t.Fatal("nil suspend-on-idle must default to true")
	}
	disabled := false
	if ShouldSuspendOnIdle(&disabled) {
		t.Fatal("explicit false must disable suspend-on-idle")
	}
	enabled := true
	if !ShouldSuspendOnIdle(&enabled) {
		t.Fatal("explicit true must keep suspend-on-idle")
	}
}

func TestActorSpecForRunTrimsAndCopiesLabels(t *testing.T) {
	t.Parallel()

	spec := ActorSpecForRun(" agents ", "chat-thread-1", " openCode ", " standing-chat ", " warm ",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "chat-turn-1", "": "dropped"})
	if spec.Namespace != "agents" || spec.Name != "chat-thread-1" || spec.HarnessKind != "openCode" ||
		spec.ActorClass != "standing-chat" || spec.Pool != "warm" {
		t.Fatalf("actor spec not trimmed: %+v", spec)
	}
	if len(spec.Labels) != 1 || spec.Labels["control.anvil.hazyforge.io/agent-run"] != "chat-turn-1" {
		t.Fatalf("actor labels = %+v, want the run label without the empty key", spec.Labels)
	}
}
