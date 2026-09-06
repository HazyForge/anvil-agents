package runapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigParsesDurationsAndBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
bindAddress: ":9090"
oidc:
  issuer: https://issuer.example
  audiences: [anvil-agents]
authorization:
  bindings:
    - name: viewers
      roles: [viewer]
      permissions: [anvil-agents:runs:read]
      namespaces: [agents]
stream:
  heartbeatInterval: 5s
  maxDuration: 2m
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.BindAddress != ":9090" || config.Stream.HeartbeatInterval.Duration != 5*time.Second || config.Stream.MaxDuration.Duration != 2*time.Minute {
		t.Fatalf("unexpected config: %#v", config)
	}
	if config.Stream.MaxTailLines != 10_000 || len(config.Authorization.RoleClaims) != 1 || len(config.Authorization.RoleObjectClaims) != 0 {
		t.Fatalf("expected defaults to survive partial config: %#v", config)
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := `
bindAddress: ":9090"
unknown: true
oidc:
  issuer: https://issuer.example
  audiences: [anvil-agents]
authorization:
  bindings:
    - roles: [viewer]
      permissions: [anvil-agents:runs:read]
      namespaces: [agents]
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected strict decode failure, got %v", err)
	}
}

func TestConfigRejectsImplicitAuthorization(t *testing.T) {
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "explicit grant") {
		t.Fatalf("expected missing binding rejection, got %v", err)
	}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: []string{PermissionRunsRead},
		Namespaces:  []string{"*"},
	}}
	config.CORS.AllowedOrigins = []string{"*"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("expected wildcard CORS rejection, got %v", err)
	}
}

func TestConfigControlsWriteRequiresRead(t *testing.T) {
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents"}
	config.Controls.WriteEnabled = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"operator"},
		Permissions: []string{PermissionRunsRead},
		Namespaces:  []string{"hazy-trade"},
	}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "controls.writeEnabled requires controls.readEnabled") {
		t.Fatalf("expected controls write-requires-read rejection, got %v", err)
	}
}

func TestConfigRejectsChatPermissionWithoutGate(t *testing.T) {
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents"}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"operator"},
		Permissions: []string{PermissionChatWrite},
		Namespaces:  []string{"hazy-trade"},
	}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "chat.enabled is false") {
		t.Fatalf("expected chat gate rejection, got %v", err)
	}
	config.Chat.Enabled = true
	if err := config.Validate(); err != nil {
		t.Fatalf("expected chat permission to be accepted when enabled, got %v", err)
	}
}

func TestConfigRejectsControlsPermissionWithoutGate(t *testing.T) {
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents"}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"operator"},
		Permissions: []string{PermissionControlsWrite},
		Namespaces:  []string{"hazy-trade"},
	}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "controls.writeEnabled is false") {
		t.Fatalf("expected controls gate rejection, got %v", err)
	}
}
