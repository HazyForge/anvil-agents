package runapi

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// TestKindLocalStandingManifestKindIsProcessLive ties the Kind-local
// standing-enabled manager composition to the live ProcessBackend recipe
// table: examples/live-api/kind-local-standing-manager.yaml must name a
// harness kind ProcessBackend can execute as a local subprocess. A Fake-only
// kind (hermesAgent, piAgent, custom, empty) would hold every turn as
// NeedsHuman/InProcessNotWired by design — no subprocess, no sample. See
// docs/standing-inprocess-harness.md ("Kind-local harness CLI + local
// auth").
func TestKindLocalStandingManifestKindIsProcessLive(t *testing.T) {
	contents, err := os.ReadFile("../../examples/live-api/kind-local-standing-manager.yaml")
	if err != nil {
		t.Fatalf("read standing-manager manifest: %v", err)
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096)
	supported := map[string]bool{}
	for _, kind := range standing.SupportedProcessKinds() {
		supported[kind] = true
	}
	seen := false
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode standing-manager manifest: %v", err)
		}
		if len(object.Object) == 0 || object.GetKind() != "AgentHarnessProfile" {
			continue
		}
		raw, err := object.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal harness profile: %v", err)
		}
		var harness agentsv1alpha1.AgentHarnessProfile
		if err := sigsyaml.Unmarshal(raw, &harness); err != nil {
			t.Fatalf("unmarshal harness profile: %v", err)
		}
		seen = true
		kind := string(harness.Spec.Backend.Kind)
		if !supported[kind] {
			t.Fatalf("kind-local-standing backend kind = %q, want one of the live ProcessBackend kinds %v (Fake-only kinds hold as InProcessNotWired)", kind, standing.SupportedProcessKinds())
		}
	}
	if !seen {
		t.Fatal("standing-manager manifest must contain AgentHarnessProfile kind-local-standing")
	}
}

// TestKindLocalHarnessWiredTurnClearsHold proves the wired path clears the
// hold: with the live gate on and a ProcessBackend attached (stub Runner
// returning the kind's native envelope — no model call, no subprocess, no
// credentials), a turn on a kind-local-standing-shaped InProcess codex
// harness succeeds instead of staying on the active hold that surfaces as
// NeedsHuman/InProcessNotWired on the controller pass. The gate-off and
// failing-backend hold shapes stay pinned by
// TestStandingTurnGateOffKeepsHoldBehavior,
// TestStandingProcessTurnFailureKeepsHoldBehavior, and
// TestStandingProcessTurnFakeOnlyKindKeepsHoldBehavior; no live latency
// numbers are captured here.
func TestKindLocalHarnessWiredTurnClearsHold(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	// Same shape as examples/live-api/kind-local-standing-manager.yaml:
	// InProcess runtime, codex backend.
	standingHarness(t, server, "kind-local-standing", agentsv1alpha1.AgentRunHarnessBackendCodex)
	native := `{"type":"item.completed","item":{"type":"agent_message","text":"kind-local wired reply"}}`
	enableStandingLive(t, server, standing.NewProcessBackend(stubProcessRunner(t, native)))
	thread, err := server.chatStore.CreateThread(ctx, chat.Thread{
		Namespace: "agents",
		Mode:      "persona",
		CreatedBy: "user",
		Metadata:  []byte(`{"harnessProfileName":"kind-local-standing"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello kind-local"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("wired kind-local turn status = %q, want succeeded (hold cleared)", accepted.Turn.Status)
	}
	turns, err := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || chat.Active(turns[0]) {
		t.Fatalf("wired kind-local turns = %#v %v, want one completed turn, not an active hold", turns, err)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[1].Content != "kind-local wired reply" {
		t.Fatalf("messages = %#v %v, want the native reply extracted verbatim", messages, err)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded || run.Status.Backend != "codex" {
		t.Fatalf("run status = %+v, want Succeeded/codex", run.Status)
	}
	if !strings.Contains(run.Status.Output, "kind-local wired reply") {
		t.Fatalf("run output = %q, want the native harness output", run.Status.Output)
	}
}
