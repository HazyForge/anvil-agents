package standing

import (
	"strings"
	"testing"
)

// Kind-local local-auth boundary: the harness subprocess must reach the
// CLI's own auth home (HOME survives filtering) while API bearer material,
// the chat database URI, and cluster credentials never cross into the
// model-facing child env. The Kind-local runbook bearer
// (ANVIL_AGENTS_ACCESS_TOKEN / --token-file, minted by cmd/kind-oidc-issuer)
// authenticates the operator/probe to the API only. See
// docs/standing-inprocess-harness.md ("Kind-local harness CLI + local
// auth") and hack/kind-standing-harness.sh.
func TestProcessChildEnvStripsAPIBearerMaterial(t *testing.T) {
	t.Setenv("ANVIL_AGENTS_ACCESS_TOKEN", "header.payload.signature")
	t.Setenv("ANVIL_AGENTS_CHAT_DATABASE_URL", "postgresql://anvil_agents:pw@127.0.0.1:5432/anvil_agents?sslmode=disable")
	t.Setenv("ANVIL_AGENTS_HARNESS_TOKEN", "must-not-leak")
	t.Setenv("OIDC_TOKEN", "must-not-leak")
	t.Setenv("ACCESS_TOKEN", "must-not-leak")
	t.Setenv("AUTHORIZATION", "Bearer must-not-leak")
	t.Setenv("KUBECONFIG", "/tmp/must-not-leak-kubeconfig")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBE_TOKEN", "must-not-leak")
	t.Setenv("HOME", "/home/kind-local")
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")

	env := processChildEnv(nil)
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, leaked := range []string{
		"must-not-leak",
		"header.payload.signature",
		"postgresql://anvil_agents",
	} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("child env leaks %q", leaked)
		}
	}
	for _, prefix := range []string{
		"ANVIL_AGENTS_ACCESS_TOKEN=",
		"ANVIL_AGENTS_CHAT_DATABASE_URL=",
		"ANVIL_AGENTS_HARNESS_TOKEN=",
		"OIDC_TOKEN=",
		"ACCESS_TOKEN=",
		"AUTHORIZATION=",
		"KUBECONFIG=",
		"KUBERNETES_SERVICE_HOST=",
		"KUBE_TOKEN=",
	} {
		for _, kv := range env {
			if strings.HasPrefix(kv, prefix) {
				t.Fatalf("child env carries %q", prefix)
			}
		}
	}
	// The CLI's own auth home (e.g. ~/.codex/auth.json) must stay reachable:
	// HOME and PATH survive the filter.
	found := map[string]bool{}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") {
			found[kv] = true
		}
	}
	if !found["HOME=/home/kind-local"] {
		t.Fatalf("child env lost HOME; harness auth home would be unreachable: %v", env)
	}
	if !found["PATH=/usr/local/bin:/usr/bin:/bin"] {
		t.Fatalf("child env lost PATH; harness CLI resolution would break: %v", env)
	}
}

// The explicit ExecRunner.Env override passes through verbatim: it exists
// for tests only, so a test double can observe the boundary without
// touching the ambient process env.
func TestProcessChildEnvExplicitOverridePassesThrough(t *testing.T) {
	override := []string{"STANDING_TEST_ONLY=1"}
	env := processChildEnv(&ExecRunner{Env: override})
	if len(env) != 1 || env[0] != "STANDING_TEST_ONLY=1" {
		t.Fatalf("override env = %v, want verbatim passthrough", env)
	}
}
