package runapi

import (
	"bytes"
	"io"
	"os"
	"testing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"
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
	// Jev intent routing stays deny-by-default in the example: the gate flag
	// is off (enabled per-process via ANVIL_AGENTS_JEV_INTENT=1), while the
	// validated serving model is pinned for local measurement. See
	// docs/jev-intent-routing.md ("Kind-local runbook").
	if config.Chat.JevIntentEnabled {
		t.Fatal("example config must leave chat.jevIntentEnabled off (enable via ANVIL_AGENTS_JEV_INTENT=1)")
	}
	if config.Chat.JevModel != "jev-1.13.0" {
		t.Fatalf("example chat.jevModel = %q, want jev-1.13.0", config.Chat.JevModel)
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

// TestKindLocalStandingManagerManifestLoads pins the standing-enabled manager
// composition: examples/live-api/kind-local-standing-manager.yaml must carry
// an InProcess harness profile (no Substrate section — Substrate stays
// optional and untouched) bound to the manager AgentRunProfile via
// harnessProfileRef, in the `agents` namespace the example API config
// authorizes, with standing.liveEnabled on in that config. See
// docs/standing-inprocess-harness.md ("Kind-local signed-in samples").
func TestKindLocalStandingManagerManifestLoads(t *testing.T) {
	config, err := LoadConfig("../../examples/live-api/kind-local-api-config.yaml")
	if err != nil {
		t.Fatalf("example config failed to load: %v", err)
	}
	if !config.Standing.LiveEnabled {
		t.Fatal("example config must set standing.liveEnabled for the manager thread")
	}
	contents, err := os.ReadFile("../../examples/live-api/kind-local-standing-manager.yaml")
	if err != nil {
		t.Fatalf("read standing-manager manifest: %v", err)
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096)
	var harnessSeen, profileSeen bool
	for document := 1; ; document++ {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode standing-manager document %d: %v", document, err)
		}
		if len(object.Object) == 0 {
			continue
		}
		if object.GetNamespace() != "agents" {
			t.Fatalf("document %d (%s %s) namespace = %q, want agents", document, object.GetKind(), object.GetName(), object.GetNamespace())
		}
		raw, err := object.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal standing-manager document %d: %v", document, err)
		}
		switch object.GetKind() {
		case "AgentHarnessProfile":
			if object.GetName() != "kind-local-standing" {
				t.Fatalf("harness profile name = %q, want kind-local-standing", object.GetName())
			}
			var harness agentsv1alpha1.AgentHarnessProfile
			if err := sigsyaml.Unmarshal(raw, &harness); err != nil {
				t.Fatalf("unmarshal harness profile: %v", err)
			}
			if !harness.Spec.Execution.UsesInProcess() {
				t.Fatalf("harness execution runtime = %q, want InProcess", harness.Spec.Execution.Runtime)
			}
			if harness.Spec.Execution.Substrate != nil {
				t.Fatal("standing-manager harness must not carry a substrate section")
			}
			// codex is live behind ProcessBackend and supports native resume,
			// so cold-first + resumed-second turns are measurable.
			if harness.Spec.Backend.Kind != agentsv1alpha1.AgentRunHarnessBackendCodex {
				t.Fatalf("harness backend kind = %q, want codex", harness.Spec.Backend.Kind)
			}
			harnessSeen = true
		case "AgentRunProfile":
			if object.GetName() != "kind-local-manager" {
				t.Fatalf("manager profile name = %q, want kind-local-manager", object.GetName())
			}
			var profile agentsv1alpha1.AgentRunProfile
			if err := sigsyaml.Unmarshal(raw, &profile); err != nil {
				t.Fatalf("unmarshal manager profile: %v", err)
			}
			if profile.Spec.HarnessProfileRef == nil || profile.Spec.HarnessProfileRef.Name != "kind-local-standing" {
				t.Fatalf("manager harnessProfileRef = %+v, want kind-local-standing", profile.Spec.HarnessProfileRef)
			}
			profileSeen = true
		default:
			t.Fatalf("document %d kind = %q, want AgentHarnessProfile or AgentRunProfile", document, object.GetKind())
		}
	}
	if !harnessSeen {
		t.Fatal("standing-manager manifest must contain AgentHarnessProfile kind-local-standing")
	}
	if !profileSeen {
		t.Fatal("standing-manager manifest must contain AgentRunProfile kind-local-manager")
	}
}
