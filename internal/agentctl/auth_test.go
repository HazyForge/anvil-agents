package agentctl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestSummarizeCodexAuthBytes(t *testing.T) {
	t.Parallel()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800}`))
	token := header + "." + payload + ".sig"
	body, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  token,
			"refresh_token": "refresh",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := summarizeCodexAuthBytes(body)
	if !summary.ValidJSON || !summary.HasRefreshToken || !summary.HasAccessToken {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.AccessTokenExpired == nil || *summary.AccessTokenExpired {
		t.Fatalf("expected unexpired token, got %#v", summary.AccessTokenExpired)
	}
}

func TestSummarizeGrokAuthBytes(t *testing.T) {
	t.Parallel()
	body := []byte(`{"https://auth.x.ai::client":{"auth_mode":"oauth","refresh_token":"r","key":"k","expires_at":"2099-01-01T00:00:00Z"}}`)
	summary := summarizeProviderAuthBytes(mustAuthProfile("xai"), body)
	if !summary.ValidJSON || !summary.HasRefreshToken || !summary.HasAPIKey {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.EntryCount != 1 {
		t.Fatalf("entryCount = %d", summary.EntryCount)
	}
	if summary.AccessTokenExpired == nil || *summary.AccessTokenExpired {
		t.Fatalf("expected unexpired expires_at, got %#v", summary.AccessTokenExpired)
	}
}

func TestResolveAuthProviderAliases(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"codex", "openai"} {
		profile, err := resolveAuthProvider(name)
		if err != nil || profile.Provider != agentsv1alpha1.AgentAuthSessionProviderCodex {
			t.Fatalf("%s => %#v err=%v", name, profile, err)
		}
	}
	for _, name := range []string{"grok", "xai", "grokBuild"} {
		profile, err := resolveAuthProvider(name)
		if err != nil || profile.Provider != agentsv1alpha1.AgentAuthSessionProviderGrokBuild {
			t.Fatalf("%s => %#v err=%v", name, profile, err)
		}
	}
}

func TestAuthCodexReauthCreatesSession(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	body := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"a","refresh_token":"r"}}`)
	if err := os.WriteFile(authPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{
		dataVolume: &agentsv1alpha1.AgentDataVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "manager-home", Namespace: "agents"},
			Status:     agentsv1alpha1.AgentDataVolumeStatus{Phase: agentsv1alpha1.AgentDataVolumePhaseReady, ClaimUID: "claim"},
		},
	}
	var output strings.Builder
	app := testApp(backend, &output)
	err := app.Run(context.Background(), []string{
		"auth", "codex", "reauth",
		"-n", "agents",
		"--data-volume", "manager-home",
		"--auth-file", authPath,
		"--bootstrap-secret", "codex-credentials-seed",
	})
	if err != nil {
		t.Fatalf("reauth error: %v", err)
	}
	if backend.createdSecret == nil || len(backend.createdSecret.Data[codexAuthJSONKey]) == 0 {
		t.Fatal("expected staging secret")
	}
	if backend.authSession == nil || backend.authSession.Spec.Action != agentsv1alpha1.AgentAuthSessionActionReauth {
		t.Fatalf("session = %#v", backend.authSession)
	}
	if backend.authSession.Spec.BootstrapSecretRef == nil || backend.authSession.Spec.BootstrapSecretRef.Name != "codex-credentials-seed" {
		t.Fatalf("bootstrap ref = %#v", backend.authSession.Spec.BootstrapSecretRef)
	}
	if backend.createdSecret.Data[codexAuthJSONKey] == nil {
		t.Fatal("expected CODEX_AUTH_JSON staging key")
	}
	if !strings.Contains(output.String(), "phase=Succeeded") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestAuthGrokReauthCreatesSession(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	body := []byte(`{"https://auth.x.ai::c":{"auth_mode":"oauth","refresh_token":"r","key":"k"}}`)
	if err := os.WriteFile(authPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{
		dataVolume: &agentsv1alpha1.AgentDataVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "grok-home", Namespace: "agents"},
			Status:     agentsv1alpha1.AgentDataVolumeStatus{Phase: agentsv1alpha1.AgentDataVolumePhaseReady, ClaimUID: "claim"},
		},
	}
	var output strings.Builder
	if err := testApp(backend, &output).Run(context.Background(), []string{
		"auth", "xai", "reauth",
		"-n", "agents",
		"--data-volume", "grok-home",
		"--auth-file", authPath,
	}); err != nil {
		t.Fatalf("reauth error: %v", err)
	}
	if backend.authSession == nil || backend.authSession.Spec.Provider != agentsv1alpha1.AgentAuthSessionProviderGrokBuild {
		t.Fatalf("session = %#v", backend.authSession)
	}
	if len(backend.createdSecret.Data[grokAuthJSONKey]) == 0 {
		t.Fatal("expected GROK_AUTH_JSON staging key")
	}
}

func TestAuthCodexLogoutRequiresConfirm(t *testing.T) {
	backend := &fakeBackend{
		dataVolume: &agentsv1alpha1.AgentDataVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "manager-home", Namespace: "agents"},
			Status:     agentsv1alpha1.AgentDataVolumeStatus{Phase: agentsv1alpha1.AgentDataVolumePhaseReady},
		},
	}
	app := testApp(backend, &strings.Builder{})
	err := app.Run(context.Background(), []string{
		"auth", "codex", "logout",
		"-n", "agents",
		"--data-volume", "manager-home",
		"--confirm-volume", "other",
	})
	if err == nil || !strings.Contains(err.Error(), "confirm-volume") {
		t.Fatalf("expected confirm error, got %v", err)
	}
}

func TestAuthCodexDiagnoseESOFinding(t *testing.T) {
	backend := &fakeBackend{
		dataVolume: &agentsv1alpha1.AgentDataVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "manager-home", Namespace: "agents"},
			Status:     agentsv1alpha1.AgentDataVolumeStatus{Phase: agentsv1alpha1.AgentDataVolumePhaseReady, ClaimUID: "uid"},
		},
		secret: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "codex-credentials",
				Labels: map[string]string{"reconcile.external-secrets.io/managed": "true"},
			},
			Data: map[string][]byte{codexAuthJSONKey: []byte(`{"tokens":{"refresh_token":"r"}}`)},
		},
	}
	var output strings.Builder
	_ = testApp(backend, &output).Run(context.Background(), []string{
		"auth", "codex", "diagnose",
		"-n", "agents",
		"--data-volume", "manager-home",
		"--bootstrap-secret", "codex-credentials",
	})
	if !strings.Contains(output.String(), "ExternalSecret") {
		t.Fatalf("expected ESO finding, got %s", output.String())
	}
}

func TestSelfReportWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	statusFile := filepath.Join(dir, "status.jsonl")
	t.Setenv("ANVIL_AGENT_RUN_STATUS_FILE", statusFile)
	t.Setenv("ANVIL_AGENT_RUN_STATUS_LOG_FD", os.DevNull)
	var output strings.Builder
	app := App{Out: &output, Err: &strings.Builder{}}
	if err := app.Run(context.Background(), []string{"self", "report", "progress", "--stage", "tool-setup", "--summary", "ready"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"stage":"tool-setup"`) {
		t.Fatalf("status body = %s", body)
	}
}
