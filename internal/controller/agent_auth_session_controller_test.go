package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestValidateAgentAuthSessionSpec(t *testing.T) {
	t.Parallel()
	valid := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderCodex,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: "codex-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-1",
		},
	}
	if err := validateAgentAuthSessionSpec(valid); err != nil {
		t.Fatalf("valid reauth rejected: %v", err)
	}
	logout := valid.DeepCopy()
	logout.Spec.Action = controlv1alpha1.AgentAuthSessionActionLogout
	logout.Spec.StagingSecretRef = nil
	logout.Spec.SeedID = ""
	if err := validateAgentAuthSessionSpec(logout); err != nil {
		t.Fatalf("valid logout rejected: %v", err)
	}
	missingSeed := valid.DeepCopy()
	missingSeed.Spec.SeedID = ""
	if err := validateAgentAuthSessionSpec(missingSeed); err == nil {
		t.Fatal("expected missing seedID error")
	}
}

func TestAgentAuthSessionScriptNeverEchoesCredentials(t *testing.T) {
	t.Parallel()
	codex, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	script := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionReauth, "/codex-home", codex)
	for _, banned := range []string{"echo \"$CODEX_AUTH_JSON\"", "cat \"$auth\"", "printenv"} {
		if strings.Contains(script, banned) {
			t.Fatalf("script contains banned credential leak pattern %q", banned)
		}
	}
	for _, required := range []string{"auth.json", ".anvil-codex-auth-seed-id", ".anvil-codex-auth-logged-out", "mktemp", "mv ", "CODEX_AUTH_JSON"} {
		if !strings.Contains(script, required) {
			t.Fatalf("reauth script missing %q", required)
		}
	}
	logout := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionLogout, "/codex-home", codex)
	if !strings.Contains(logout, "logged-out") {
		t.Fatal("logout script missing tombstone write")
	}

	grok, err := agentAuthLayout(controlv1alpha1.AgentAuthSessionProviderGrokBuild)
	if err != nil {
		t.Fatal(err)
	}
	grokScript := agentAuthSessionScript(controlv1alpha1.AgentAuthSessionActionReauth, "/opt/anvil/grok-build", grok)
	for _, required := range []string{".grok/auth.json", "GROK_AUTH_JSON", "ANVIL_GROK_AUTH_SEED_ID", ".anvil-grok-auth-seed-id"} {
		if !strings.Contains(grokScript, required) {
			t.Fatalf("grok reauth script missing %q\n%s", required, grokScript)
		}
	}
}

func TestValidateAgentAuthSessionSpecAcceptsGrokBuild(t *testing.T) {
	t.Parallel()
	session := &controlv1alpha1.AgentAuthSession{
		Spec: controlv1alpha1.AgentAuthSessionSpec{
			Provider:         controlv1alpha1.AgentAuthSessionProviderGrokBuild,
			Action:           controlv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: "grok-home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-xai",
		},
	}
	if err := validateAgentAuthSessionSpec(session); err != nil {
		t.Fatalf("grokBuild reauth rejected: %v", err)
	}
}

func TestSecretManagedByExternalSecret(t *testing.T) {
	t.Parallel()
	plain := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "seed"}}
	if secretManagedByExternalSecret(plain) {
		t.Fatal("plain secret should be writable")
	}
	eso := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "seed",
		Labels: map[string]string{
			"reconcile.external-secrets.io/managed": "true",
		},
	}}
	if !secretManagedByExternalSecret(eso) {
		t.Fatal("eso-labeled secret should be detected")
	}
}

func TestAgentRunIsTerminalForAuthIdle(t *testing.T) {
	t.Parallel()
	if !agentRunIsTerminal(controlv1alpha1.AgentRunPhaseSucceeded) || !agentRunIsTerminal(controlv1alpha1.AgentRunPhaseFailed) {
		t.Fatal("expected terminal phases")
	}
	if agentRunIsTerminal(controlv1alpha1.AgentRunPhaseRunning) || agentRunIsTerminal(controlv1alpha1.AgentRunPhasePending) {
		t.Fatal("expected non-terminal phases")
	}
}
