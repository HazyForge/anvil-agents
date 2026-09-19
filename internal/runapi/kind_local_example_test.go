package runapi

import (
	"testing"
)

// TestKindLocalExampleConfigLoads pins the Kind-local standing-chat latency
// scaffolding: examples/live-api/kind-local-api-config.yaml must stay a
// valid API config with the issuer, gates, and the kind-local-desktop
// binding the live probe documents. See
// docs/standing-inprocess-harness.md ("Desktop chat e2e over the standing
// WebSocket").
func TestKindLocalExampleConfigLoads(t *testing.T) {
	config, err := LoadConfig("../../examples/live-api/kind-local-api-config.yaml")
	if err != nil {
		t.Fatalf("example config failed to load: %v", err)
	}
	if config.OIDC.Issuer != "http://127.0.0.1:18081" {
		t.Fatalf("issuer = %q", config.OIDC.Issuer)
	}
	if !config.OIDC.AllowInsecureIssuer || !config.Runs.CreateEnabled || !config.Chat.Enabled || !config.Standing.LiveEnabled {
		t.Fatalf("example gates not set: %+v", config)
	}
	authorizer := NewAuthorizer(config.Authorization)
	principal := Principal{Roles: []string{"kind-local-desktop"}}
	for _, permission := range []string{PermissionRunsRead, PermissionRunsStream, PermissionRunsCreate, PermissionChatRead, PermissionChatWrite} {
		if !authorizer.Allowed(principal, permission, "agents") {
			t.Fatalf("example binding missing %s", permission)
		}
	}
	if authorizer.Allowed(principal, PermissionChatWrite, "other") {
		t.Fatal("example binding leaks to other namespaces")
	}
}
