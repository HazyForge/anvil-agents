package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/jev"
	"k8s.io/apimachinery/pkg/types"
)

// cannedJevIntent returns a no-network FakeBackend answering the intent
// question with one fixed choice/confidence.
func cannedJevIntent(t *testing.T, choice string, confidence float64) jev.Backend {
	t.Helper()
	probs := map[string]float64{
		jev.IntentChatReply:          0.01,
		jev.IntentCreateAgentRequest: 0.01,
		jev.IntentPeerHandoff:        0.01,
		jev.IntentToolRun:            0.01,
		jev.IntentUnclear:            0.01,
	}
	probs[choice] = 0.96
	return &jev.FakeBackend{
		Model: "fake-intent-router",
		Answers: map[string]json.RawMessage{
			jev.IntentQuestionID: jev.MustAnswer(t, jev.ChoiceAnswer{
				Type:          jev.TypeChoice,
				Choice:        choice,
				Probabilities: probs,
				Confidence:    confidence,
			}),
		},
	}
}

func queueJevTurn(t *testing.T, server *Server, content string) (ChatAppendResponse, string) {
	t.Helper()
	ctx := context.Background()
	thread, err := server.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "grok45", Mode: "persona", CreatedBy: "user"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) == 0 {
		t.Fatalf("messages=%#v err=%v", messages, err)
	}
	return result, stored.Spec.Prompt + "\n---USERMETA---\n" + string(messages[0].Metadata)
}

func TestChatJevGateOffKeepsTodayBehavior(t *testing.T) {
	server := chatTestServer(t, true)
	result, promptAndMeta := queueJevTurn(t, server, "please create a helper agent for triage")
	if strings.Contains(promptAndMeta, "ROUTING_HINT") || strings.Contains(promptAndMeta, "jevIntent") {
		t.Fatalf("gate-off turn must not carry intent routing: %s", promptAndMeta)
	}
	if !strings.Contains(promptAndMeta, `"authorKind":"human"`) {
		t.Fatalf("gate-off user metadata changed: %s", promptAndMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	for key := range stored.Annotations {
		if strings.Contains(key, "jev-") {
			t.Fatalf("gate-off run carries intent annotation %q", key)
		}
	}
}

func TestChatJevGateOnWithoutBackendFallsBack(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	// No backend attached: the missing-TYPESAFE_API_KEY path. The turn must
	// still queue with today's behavior, never a hard fail.
	result, promptAndMeta := queueJevTurn(t, server, "please create a helper agent for triage")
	if result.Turn.ID == "" {
		t.Fatal("turn was not queued")
	}
	if strings.Contains(promptAndMeta, "ROUTING_HINT") || strings.Contains(promptAndMeta, "jevIntent") {
		t.Fatalf("backend-less turn must not carry intent routing: %s", promptAndMeta)
	}
}

func TestChatJevDrivesEachIntent(t *testing.T) {
	cases := []struct {
		name       string
		choice     string
		confidence float64
		wantHint   string
		wantPlain  bool
	}{
		{name: "chat reply skips harness ceremony", choice: jev.IntentChatReply, confidence: 0.9, wantHint: "ordinary conversation"},
		{name: "create agent routes toward manager", choice: jev.IntentCreateAgentRequest, confidence: 0.92, wantHint: "manager"},
		{name: "peer handoff stays on existing paths", choice: jev.IntentPeerHandoff, confidence: 0.88, wantHint: "requestPeer"},
		{name: "tool run prefers tools first", choice: jev.IntentToolRun, confidence: 0.85, wantHint: "tool-first"},
		{name: "unclear asks to clarify", choice: jev.IntentUnclear, confidence: 0.9, wantHint: "clarifying question"},
		{name: "low confidence gates to unclear", choice: jev.IntentToolRun, confidence: 0.2, wantHint: "clarifying question"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := chatTestServer(t, true)
			server.config.Chat.JevIntentEnabled = true
			server.SetJevBackend(cannedJevIntent(t, tc.choice, tc.confidence))
			result, promptAndMeta := queueJevTurn(t, server, "route me")
			parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
			prompt, userMeta := parts[0], parts[1]
			if tc.wantPlain {
				if strings.Contains(prompt, "ROUTING_HINT") {
					t.Fatalf("chat_reply must keep today's prompt: %s", prompt)
				}
			} else if !strings.Contains(prompt, "ROUTING_HINT") || !strings.Contains(strings.ToLower(prompt), strings.ToLower(tc.wantHint)) {
				t.Fatalf("prompt missing %q hint: %s", tc.wantHint, prompt)
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
				t.Fatalf("user metadata is not JSON: %v", err)
			}
			wantIntent := tc.choice
			if tc.confidence < jev.DefaultConfidenceThreshold {
				wantIntent = jev.IntentUnclear
			}
			if meta["jevIntent"] != wantIntent {
				t.Fatalf("jevIntent = %v, want %q (meta %s)", meta["jevIntent"], wantIntent, userMeta)
			}
			if _, ok := meta["jevConfidence"].(float64); !ok {
				t.Fatalf("jevConfidence missing from %s", userMeta)
			}
			if meta["jevModel"] != "fake-intent-router" {
				t.Fatalf("jevModel = %v, want fake serving model (meta %s)", meta["jevModel"], userMeta)
			}
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if stored.Annotations[jevIntentAnnotation] != wantIntent {
				t.Fatalf("run annotation %q = %q, want %q", jevIntentAnnotation, stored.Annotations[jevIntentAnnotation], wantIntent)
			}
			if stored.Annotations[jevModelAnnotation] != "fake-intent-router" {
				t.Fatalf("run annotation %q = %q, want serving model", jevModelAnnotation, stored.Annotations[jevModelAnnotation])
			}
		})
	}
}

func TestChatJevCreateAgentHintNeverAuthorizesPeerCreation(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentCreateAgentRequest, 0.92))
	_, promptAndMeta := queueJevTurn(t, server, "spawn a new worker agent")
	prompt := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)[0]
	for _, banned := range []string{"you may create", "you are authorized to create", "go ahead and create", "call create-agent", "post the profile", "post agentrunprofile"} {
		if strings.Contains(strings.ToLower(prompt), banned) {
			t.Fatalf("hint authorizes peer creation: %s", prompt)
		}
	}
	if !strings.Contains(prompt, "Wrapper/manager-only") {
		t.Fatalf("hint must name the manager-only boundary: %s", prompt)
	}
	if !strings.Contains(prompt, "Do NOT create") {
		t.Fatalf("hint must refuse peer creation: %s", prompt)
	}
}

// TestChatJevCreateAgentFulfillmentSlice pins the first concrete fulfillment
// step after classification: a classified, non-unclear create_agent_request
// carries the peer-safe request shape in the prompt plus a structured
// request flag on the user message and the turn's AgentRun — without
// granting peers create authority (enforced by the controller's
// create-agent skill stripping and Desktop isCreateAgentPrincipal; the
// hint only names the requestPeer STATUS_JSON path).
func TestChatJevCreateAgentFulfillmentSlice(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentCreateAgentRequest, 0.92))
	result, promptAndMeta := queueJevTurn(t, server, "please create a helper agent for triage")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	for _, want := range []string{"ROUTING_HINT", "request-only", "requestPeer", "create-agent", "ANVIL_AGENT_RUN_STATUS_JSON=", `"request":"create-agent"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("create_agent_request prompt missing %q: %s", want, prompt)
		}
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentCreateAgentRequest {
		t.Fatalf("jevIntent = %v, want %q (meta %s)", meta["jevIntent"], jev.IntentCreateAgentRequest, userMeta)
	}
	if meta["jevNeedsManagerCreate"] != true {
		t.Fatalf("jevNeedsManagerCreate missing/false on create_agent_request (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Annotations[jevIntentAnnotation] != jev.IntentCreateAgentRequest {
		t.Fatalf("run annotation %q = %q, want %q", jevIntentAnnotation, stored.Annotations[jevIntentAnnotation], jev.IntentCreateAgentRequest)
	}
	if stored.Annotations[jevNeedsManagerCreateAnnotation] != "true" {
		t.Fatalf("run annotation %q = %q, want \"true\"", jevNeedsManagerCreateAnnotation, stored.Annotations[jevNeedsManagerCreateAnnotation])
	}
}

// TestChatJevUnclearCreateCarriesNoFulfillment ensures the confidence gate
// suppresses fulfillment: a create_agent_request choice under the floor
// gates to unclear, asks for clarification, and carries no manager-request
// flag on the message or the run.
func TestChatJevUnclearCreateCarriesNoFulfillment(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentCreateAgentRequest, 0.2))
	result, promptAndMeta := queueJevTurn(t, server, "create")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	if !strings.Contains(strings.ToLower(prompt), "clarifying question") {
		t.Fatalf("unclear turn must ask for clarification: %s", prompt)
	}
	if strings.Contains(prompt, "ANVIL_AGENT_RUN_STATUS_JSON=") {
		t.Fatalf("unclear turn must not carry the create request shape: %s", prompt)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentUnclear {
		t.Fatalf("jevIntent = %v, want unclear (meta %s)", meta["jevIntent"], userMeta)
	}
	if _, ok := meta["jevNeedsManagerCreate"]; ok {
		t.Fatalf("unclear turn must not carry jevNeedsManagerCreate (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.Annotations[jevNeedsManagerCreateAnnotation]; ok {
		t.Fatalf("unclear run must not carry %q", jevNeedsManagerCreateAnnotation)
	}
}

// TestChatJevPeerHandoffFulfillmentSlice pins the handoff fulfillment step
// after classification, mirroring the create-agent slice: a classified,
// non-unclear peer_handoff carries the existing-peer requestPeer
// STATUS_JSON shape in the prompt (the same conventions
// web/desktop/src/wrapper/requestPeer.ts and the controller's requestPeer
// decision parsing already accept — no new protocol) plus a structured
// request flag on the user message and the turn's AgentRun. The flag is
// coordination-only: it never authorizes creation and never sends outside
// existing paths.
func TestChatJevPeerHandoffFulfillmentSlice(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentPeerHandoff, 0.88))
	result, promptAndMeta := queueJevTurn(t, server, "delegate this to the reviewer peer")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	for _, want := range []string{"ROUTING_HINT", "requestPeer", "ANVIL_AGENT_RUN_STATUS_JSON=", `"action":"requestPeer"`, `"peerProfileName":"<existing-profile>"`, `"summary":"<why>"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("peer_handoff prompt missing %q: %s", want, prompt)
		}
	}
	for _, banned := range []string{`"request":"create-agent"`, "create-agent", "go ahead and create", "you may create"} {
		if strings.Contains(strings.ToLower(prompt), strings.ToLower(banned)) {
			t.Fatalf("peer_handoff prompt must not carry the create path %q: %s", banned, prompt)
		}
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentPeerHandoff {
		t.Fatalf("jevIntent = %v, want %q (meta %s)", meta["jevIntent"], jev.IntentPeerHandoff, userMeta)
	}
	if meta["jevNeedsPeerHandoff"] != true {
		t.Fatalf("jevNeedsPeerHandoff missing/false on peer_handoff (meta %s)", userMeta)
	}
	if _, ok := meta["jevNeedsManagerCreate"]; ok {
		t.Fatalf("peer_handoff turn must not carry jevNeedsManagerCreate (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Annotations[jevIntentAnnotation] != jev.IntentPeerHandoff {
		t.Fatalf("run annotation %q = %q, want %q", jevIntentAnnotation, stored.Annotations[jevIntentAnnotation], jev.IntentPeerHandoff)
	}
	if stored.Annotations[jevNeedsPeerHandoffAnnotation] != "true" {
		t.Fatalf("run annotation %q = %q, want \"true\"", jevNeedsPeerHandoffAnnotation, stored.Annotations[jevNeedsPeerHandoffAnnotation])
	}
	if _, ok := stored.Annotations[jevNeedsManagerCreateAnnotation]; ok {
		t.Fatalf("peer_handoff run must not carry %q", jevNeedsManagerCreateAnnotation)
	}
}

// TestChatJevUnclearHandoffCarriesNoFulfillment ensures the confidence gate
// suppresses handoff fulfillment: a peer_handoff choice under the floor
// gates to unclear, asks for clarification, and carries no handoff request
// shape, message flag, or run annotation.
func TestChatJevUnclearHandoffCarriesNoFulfillment(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentPeerHandoff, 0.2))
	result, promptAndMeta := queueJevTurn(t, server, "hand off")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	if !strings.Contains(strings.ToLower(prompt), "clarifying question") {
		t.Fatalf("unclear turn must ask for clarification: %s", prompt)
	}
	if strings.Contains(prompt, "ANVIL_AGENT_RUN_STATUS_JSON=") {
		t.Fatalf("unclear turn must not carry the handoff request shape: %s", prompt)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentUnclear {
		t.Fatalf("jevIntent = %v, want unclear (meta %s)", meta["jevIntent"], userMeta)
	}
	if _, ok := meta["jevNeedsPeerHandoff"]; ok {
		t.Fatalf("unclear turn must not carry jevNeedsPeerHandoff (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.Annotations[jevNeedsPeerHandoffAnnotation]; ok {
		t.Fatalf("unclear run must not carry %q", jevNeedsPeerHandoffAnnotation)
	}
}

// TestChatJevToolRunFulfillmentSlice pins the tool fulfillment step after
// classification, mirroring the create-agent and handoff slices: a
// classified, non-unclear tool_run strengthens the ROUTING_HINT toward
// tool-first behavior through the existing tool surface (the turn's
// resolved AgentToolSet composition plus the harness tool step — no new
// tool API, no new STATUS_JSON shape) plus a structured prompting flag on
// the user message and the turn's AgentRun. The flag never authorizes
// invented results, and the turn carries neither the create nor the
// handoff request shape.
func TestChatJevToolRunFulfillmentSlice(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentToolRun, 0.85))
	result, promptAndMeta := queueJevTurn(t, server, "look up the kb article on refunds")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	for _, want := range []string{"ROUTING_HINT", "tool-first", "AgentToolSet", "status.resolvedComposition.toolSetRefs", "unless a real tool confirms"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("tool_run prompt missing %q: %s", want, prompt)
		}
	}
	for _, banned := range []string{`"request":"create-agent"`, `"peerProfileName":"<existing-profile>"`, "ANVIL_AGENT_RUN_STATUS_JSON="} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("tool_run prompt must not carry the create/handoff request shape %q: %s", banned, prompt)
		}
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentToolRun {
		t.Fatalf("jevIntent = %v, want %q (meta %s)", meta["jevIntent"], jev.IntentToolRun, userMeta)
	}
	if meta["jevNeedsToolRun"] != true {
		t.Fatalf("jevNeedsToolRun missing/false on tool_run (meta %s)", userMeta)
	}
	if _, ok := meta["jevNeedsManagerCreate"]; ok {
		t.Fatalf("tool_run turn must not carry jevNeedsManagerCreate (meta %s)", userMeta)
	}
	if _, ok := meta["jevNeedsPeerHandoff"]; ok {
		t.Fatalf("tool_run turn must not carry jevNeedsPeerHandoff (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Annotations[jevIntentAnnotation] != jev.IntentToolRun {
		t.Fatalf("run annotation %q = %q, want %q", jevIntentAnnotation, stored.Annotations[jevIntentAnnotation], jev.IntentToolRun)
	}
	if stored.Annotations[jevNeedsToolRunAnnotation] != "true" {
		t.Fatalf("run annotation %q = %q, want \"true\"", jevNeedsToolRunAnnotation, stored.Annotations[jevNeedsToolRunAnnotation])
	}
	if _, ok := stored.Annotations[jevNeedsManagerCreateAnnotation]; ok {
		t.Fatalf("tool_run run must not carry %q", jevNeedsManagerCreateAnnotation)
	}
	if _, ok := stored.Annotations[jevNeedsPeerHandoffAnnotation]; ok {
		t.Fatalf("tool_run run must not carry %q", jevNeedsPeerHandoffAnnotation)
	}
}

// TestChatJevUnclearToolRunCarriesNoFulfillment ensures the confidence gate
// suppresses tool fulfillment: a tool_run choice under the floor gates to
// unclear, asks for clarification, and carries no tool-first hint, message
// flag, or run annotation.
func TestChatJevUnclearToolRunCarriesNoFulfillment(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(cannedJevIntent(t, jev.IntentToolRun, 0.2))
	result, promptAndMeta := queueJevTurn(t, server, "run it")
	parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
	prompt, userMeta := parts[0], parts[1]
	if !strings.Contains(strings.ToLower(prompt), "clarifying question") {
		t.Fatalf("unclear turn must ask for clarification: %s", prompt)
	}
	if strings.Contains(prompt, "status.resolvedComposition.toolSetRefs") {
		t.Fatalf("unclear turn must not carry the tool-first hint: %s", prompt)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevIntent"] != jev.IntentUnclear {
		t.Fatalf("jevIntent = %v, want unclear (meta %s)", meta["jevIntent"], userMeta)
	}
	if _, ok := meta["jevNeedsToolRun"]; ok {
		t.Fatalf("unclear turn must not carry jevNeedsToolRun (meta %s)", userMeta)
	}
	stored := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored.Annotations[jevNeedsToolRunAnnotation]; ok {
		t.Fatalf("unclear run must not carry %q", jevNeedsToolRunAnnotation)
	}
}

// TestChatJevNonToolRunIntentsCarryNoToolRunFlag ensures only the tool_run
// path arms the tool-first flag: every other intent carries no
// jevNeedsToolRun message flag, no run annotation, and no tool-first hint
// shape.
func TestChatJevNonToolRunIntentsCarryNoToolRunFlag(t *testing.T) {
	for _, choice := range []string{jev.IntentChatReply, jev.IntentCreateAgentRequest, jev.IntentPeerHandoff, jev.IntentUnclear} {
		t.Run(choice, func(t *testing.T) {
			server := chatTestServer(t, true)
			server.config.Chat.JevIntentEnabled = true
			server.SetJevBackend(cannedJevIntent(t, choice, 0.9))
			result, promptAndMeta := queueJevTurn(t, server, "route me")
			parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
			userMeta := parts[1]
			if choice != jev.IntentChatReply {
				if strings.Contains(parts[0], "status.resolvedComposition.toolSetRefs") {
					t.Fatalf("%s turn must not carry the tool-first hint: %s", choice, parts[0])
				}
			} else {
				return
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
				t.Fatalf("user metadata is not JSON: %v", err)
			}
			if _, ok := meta["jevNeedsToolRun"]; ok {
				t.Fatalf("%s turn must not carry jevNeedsToolRun (meta %s)", choice, userMeta)
			}
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored.Annotations[jevNeedsToolRunAnnotation]; ok {
				t.Fatalf("%s run must not carry %q", choice, jevNeedsToolRunAnnotation)
			}
		})
	}
}

// TestChatJevNonHandoffIntentsCarryNoHandoffFlag ensures only the
// peer_handoff path arms the handoff request flag: every other intent
// carries no jevNeedsPeerHandoff message flag and no run annotation.
func TestChatJevNonHandoffIntentsCarryNoHandoffFlag(t *testing.T) {
	for _, choice := range []string{jev.IntentChatReply, jev.IntentCreateAgentRequest, jev.IntentToolRun, jev.IntentUnclear} {
		t.Run(choice, func(t *testing.T) {
			server := chatTestServer(t, true)
			server.config.Chat.JevIntentEnabled = true
			server.SetJevBackend(cannedJevIntent(t, choice, 0.9))
			result, promptAndMeta := queueJevTurn(t, server, "route me")
			parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
			userMeta := parts[1]
			if choice != jev.IntentChatReply {
				if strings.Contains(parts[0], `"peerProfileName":"<existing-profile>"`) {
					t.Fatalf("%s turn must not carry the handoff request shape: %s", choice, parts[0])
				}
			} else {
				return
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
				t.Fatalf("user metadata is not JSON: %v", err)
			}
			if _, ok := meta["jevNeedsPeerHandoff"]; ok {
				t.Fatalf("%s turn must not carry jevNeedsPeerHandoff (meta %s)", choice, userMeta)
			}
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored.Annotations[jevNeedsPeerHandoffAnnotation]; ok {
				t.Fatalf("%s run must not carry %q", choice, jevNeedsPeerHandoffAnnotation)
			}
		})
	}
}

// TestChatJevNonCreateIntentsCarryNoManagerRequest ensures only the
// create_agent_request path arms the Wrapper/manager request flag. The
// peer_handoff path now carries its own handoff request shape (not the
// create shape), so this pins the absence of the create-agent request line
// plus the manager flag/annotation on every other intent.
func TestChatJevNonCreateIntentsCarryNoManagerRequest(t *testing.T) {
	for _, choice := range []string{jev.IntentChatReply, jev.IntentPeerHandoff, jev.IntentToolRun, jev.IntentUnclear} {
		t.Run(choice, func(t *testing.T) {
			server := chatTestServer(t, true)
			server.config.Chat.JevIntentEnabled = true
			server.SetJevBackend(cannedJevIntent(t, choice, 0.9))
			result, promptAndMeta := queueJevTurn(t, server, "route me")
			parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
			userMeta := parts[1]
			if choice != jev.IntentChatReply {
				if strings.Contains(parts[0], `"request":"create-agent"`) {
					t.Fatalf("%s turn must not carry the create request shape: %s", choice, parts[0])
				}
			}
			var meta map[string]any
			if choice == jev.IntentChatReply {
				return
			}
			if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
				t.Fatalf("user metadata is not JSON: %v", err)
			}
			if _, ok := meta["jevNeedsManagerCreate"]; ok {
				t.Fatalf("%s turn must not carry jevNeedsManagerCreate (meta %s)", choice, userMeta)
			}
			if choice != jev.IntentPeerHandoff {
				if _, ok := meta["jevNeedsPeerHandoff"]; ok {
					t.Fatalf("%s turn must not carry jevNeedsPeerHandoff (meta %s)", choice, userMeta)
				}
			}
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored.Annotations[jevNeedsManagerCreateAnnotation]; ok {
				t.Fatalf("%s run must not carry %q", choice, jevNeedsManagerCreateAnnotation)
			}
			if choice != jev.IntentPeerHandoff {
				if _, ok := stored.Annotations[jevNeedsPeerHandoffAnnotation]; ok {
					t.Fatalf("%s run must not carry %q", choice, jevNeedsPeerHandoffAnnotation)
				}
			}
		})
	}
}

func TestChatJevErrorFallsBackWithoutFailing(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.SetJevBackend(jev.FuncBackend(func(ctx context.Context, req jev.Request) (jev.Response, error) {
		return jev.Response{}, errors.New("systemone overloaded")
	}))
	result, promptAndMeta := queueJevTurn(t, server, "hello")
	if result.Turn.ID == "" {
		t.Fatal("Jev error must not fail the turn")
	}
	if strings.Contains(promptAndMeta, "ROUTING_HINT") || strings.Contains(promptAndMeta, "jevIntent") {
		t.Fatalf("error fallback must keep today's behavior: %s", promptAndMeta)
	}
}

// capturingJevBackend records the request model the turn path sends and
// answers the intent question with one fixed choice/confidence (no network).
func capturingJevBackend(t *testing.T, gotModel *string, servingModel, choice string, confidence float64) jev.Backend {
	t.Helper()
	probs := map[string]float64{
		jev.IntentChatReply:          0.01,
		jev.IntentCreateAgentRequest: 0.01,
		jev.IntentPeerHandoff:        0.01,
		jev.IntentToolRun:            0.01,
		jev.IntentUnclear:            0.01,
	}
	probs[choice] = 0.96
	return jev.FuncBackend(func(ctx context.Context, req jev.Request) (jev.Response, error) {
		*gotModel = req.Model
		return jev.Response{
			Model: servingModel,
			Answers: map[string]json.RawMessage{
				jev.IntentQuestionID: jev.MustAnswer(t, jev.ChoiceAnswer{
					Type:          jev.TypeChoice,
					Choice:        choice,
					Probabilities: probs,
					Confidence:    confidence,
				}),
			},
		}, nil
	})
}

func TestChatJevConfiguredModelIsRequested(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	server.config.Chat.JevModel = "jev-1.13.0"
	var gotModel string
	server.SetJevBackend(capturingJevBackend(t, &gotModel, "jev-1.13.0", jev.IntentCreateAgentRequest, 0.92))
	_, promptAndMeta := queueJevTurn(t, server, "please create a helper agent for triage")
	if gotModel != "jev-1.13.0" {
		t.Fatalf("requested model = %q, want pinned %q", gotModel, "jev-1.13.0")
	}
	userMeta := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)[1]
	var meta map[string]any
	if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
		t.Fatalf("user metadata is not JSON: %v", err)
	}
	if meta["jevModel"] != "jev-1.13.0" {
		t.Fatalf("jevModel = %v, want serving model jev-1.13.0 (meta %s)", meta["jevModel"], userMeta)
	}
}

func TestChatJevDefaultModelTracksLatestAlias(t *testing.T) {
	server := chatTestServer(t, true)
	server.config.Chat.JevIntentEnabled = true
	// JevModel left empty (the default): the turn path must request the
	// jev-latest alias, keeping today's behavior unchanged.
	var gotModel string
	server.SetJevBackend(capturingJevBackend(t, &gotModel, "jev-1.13.0", jev.IntentChatReply, 0.9))
	queueJevTurn(t, server, "hello")
	if gotModel != jev.DefaultModel {
		t.Fatalf("requested model = %q, want default alias %q", gotModel, jev.DefaultModel)
	}
	if server.jevModel() != "" {
		t.Fatalf("jevModel() = %q, want empty default", server.jevModel())
	}
}

func TestChatJevPromptByteIdenticalWhenUnclassified(t *testing.T) {
	server := chatTestServer(t, true)
	thread, err := server.chatStore.CreateThread(context.Background(), chat.Thread{Namespace: "agents", ProfileName: "grok45", Mode: "persona", CreatedBy: "user"})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := server.chatStore.ListMessages(context.Background(), "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := buildChatPrompt(thread, messages, "hello")
	if err != nil {
		t.Fatal(err)
	}
	got, err := buildChatPromptWithIntent(thread, messages, "hello", jev.Decision{Intent: jev.IntentChatReply}, false)
	if err != nil || got != want {
		t.Fatalf("unclassified prompt differs from today's prompt: %q vs %q (%v)", got, want, err)
	}
	base := chatAuthorMetadata(thread, false)
	withIntent := chatAuthorMetadataWithIntent(thread, false, jev.Decision{Intent: jev.IntentChatReply}, false)
	if string(base) != string(withIntent) {
		t.Fatalf("unclassified metadata differs: %q vs %q", base, withIntent)
	}
}

// TestJevDestructiveActionBarAtFulfillmentSite pins the named bar on the
// needs-* helpers and STATUS_JSON hints. Classification (intent) is not
// rewritten here — the router already kept it above the 0.5 floor.
func TestJevDestructiveActionBarAtFulfillmentSite(t *testing.T) {
	const classified = true
	cases := []struct {
		name           string
		intent         string
		confidence     float64
		unclear        bool
		wantCreate     bool
		wantHandoff    bool
		wantTool       bool
		wantStatusJSON bool
	}{
		{
			name:           "create at bar fulfills",
			intent:         jev.IntentCreateAgentRequest,
			confidence:     jev.DestructiveActionConfidenceBar,
			wantCreate:     true,
			wantStatusJSON: true,
		},
		{
			name:           "create above bar fulfills",
			intent:         jev.IntentCreateAgentRequest,
			confidence:     0.92,
			wantCreate:     true,
			wantStatusJSON: true,
		},
		{
			name:       "create between floor and bar keeps class without fulfillment",
			intent:     jev.IntentCreateAgentRequest,
			confidence: 0.6,
		},
		{
			name:       "create just below bar keeps class without fulfillment",
			intent:     jev.IntentCreateAgentRequest,
			confidence: 0.79,
		},
		{
			name:           "handoff at bar fulfills",
			intent:         jev.IntentPeerHandoff,
			confidence:     jev.DestructiveActionConfidenceBar,
			wantHandoff:    true,
			wantStatusJSON: true,
		},
		{
			name:           "handoff above bar fulfills",
			intent:         jev.IntentPeerHandoff,
			confidence:     0.88,
			wantHandoff:    true,
			wantStatusJSON: true,
		},
		{
			name:       "handoff between floor and bar keeps class without fulfillment",
			intent:     jev.IntentPeerHandoff,
			confidence: 0.6,
		},
		{
			name:       "tool_run between floor and bar still fulfills",
			intent:     jev.IntentToolRun,
			confidence: 0.6,
			wantTool:   true,
		},
		{
			name:       "chat_reply between floor and bar has no flags",
			intent:     jev.IntentChatReply,
			confidence: 0.6,
		},
		{
			name:       "unclear create has no flags",
			intent:     jev.IntentUnclear,
			confidence: 0.92,
			unclear:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := jev.Decision{
				Intent:     tc.intent,
				RawChoice:  tc.intent,
				Confidence: tc.confidence,
				Unclear:    tc.unclear,
				Model:      "fake-intent-router",
			}
			if got := jevNeedsManagerCreate(decision, classified); got != tc.wantCreate {
				t.Fatalf("jevNeedsManagerCreate = %v, want %v", got, tc.wantCreate)
			}
			if got := jevNeedsPeerHandoff(decision, classified); got != tc.wantHandoff {
				t.Fatalf("jevNeedsPeerHandoff = %v, want %v", got, tc.wantHandoff)
			}
			if got := jevNeedsToolRun(decision, classified); got != tc.wantTool {
				t.Fatalf("jevNeedsToolRun = %v, want %v", got, tc.wantTool)
			}
			hint := jevIntentPromptHint(decision, classified)
			if tc.wantStatusJSON {
				if !strings.Contains(hint, "ANVIL_AGENT_RUN_STATUS_JSON=") {
					t.Fatalf("fulfillment hint missing STATUS_JSON: %s", hint)
				}
			} else if strings.Contains(hint, "ANVIL_AGENT_RUN_STATUS_JSON=") {
				t.Fatalf("non-fulfillment hint must not encourage STATUS_JSON: %s", hint)
			}
			meta := chatAuthorMetadataWithIntent(chat.Thread{Namespace: "agents", ProfileName: "grok45"}, false, decision, classified)
			var parsed map[string]any
			if err := json.Unmarshal(meta, &parsed); err != nil {
				t.Fatalf("metadata: %v", err)
			}
			if parsed["jevIntent"] != tc.intent {
				t.Fatalf("jevIntent = %v, want classified %q", parsed["jevIntent"], tc.intent)
			}
			_, hasCreate := parsed["jevNeedsManagerCreate"]
			_, hasHandoff := parsed["jevNeedsPeerHandoff"]
			_, hasTool := parsed["jevNeedsToolRun"]
			if hasCreate != tc.wantCreate {
				t.Fatalf("metadata jevNeedsManagerCreate present=%v, want %v (%s)", hasCreate, tc.wantCreate, meta)
			}
			if hasHandoff != tc.wantHandoff {
				t.Fatalf("metadata jevNeedsPeerHandoff present=%v, want %v (%s)", hasHandoff, tc.wantHandoff, meta)
			}
			if hasTool != tc.wantTool {
				t.Fatalf("metadata jevNeedsToolRun present=%v, want %v (%s)", hasTool, tc.wantTool, meta)
			}
			annotations := annotateChatRunWithIntent(nil, decision, classified)
			_, hasCreateAnn := annotations[jevNeedsManagerCreateAnnotation]
			_, hasHandoffAnn := annotations[jevNeedsPeerHandoffAnnotation]
			_, hasToolAnn := annotations[jevNeedsToolRunAnnotation]
			if hasCreateAnn != tc.wantCreate {
				t.Fatalf("annotation manager-create present=%v, want %v", hasCreateAnn, tc.wantCreate)
			}
			if hasHandoffAnn != tc.wantHandoff {
				t.Fatalf("annotation peer-handoff present=%v, want %v", hasHandoffAnn, tc.wantHandoff)
			}
			if hasToolAnn != tc.wantTool {
				t.Fatalf("annotation tool-run present=%v, want %v", hasToolAnn, tc.wantTool)
			}
		})
	}
}

// TestChatJevConfidenceFloorAndDestructiveBarLabeledFixtures drives the
// chat-turn path with FakeBackend labeled traffic: high-confidence
// keep-class, near-floor gate to unclear, truncated/ambiguous to unclear,
// and the destructive bar withholding needs-* / STATUS_JSON while keeping
// the classified intent. tool_run stays on the classify floor.
func TestChatJevConfidenceFloorAndDestructiveBarLabeledFixtures(t *testing.T) {
	type fixture struct {
		name           string
		message        string
		choice         string
		confidence     float64
		wantIntent     string
		wantUnclear    bool
		wantCreate     bool
		wantHandoff    bool
		wantTool       bool
		wantStatusJSON bool
		wantClarify    bool
	}
	fixtures := []fixture{
		{
			name:           "high-confidence create scout fulfills",
			message:        "create an agent named Scout for research",
			choice:         jev.IntentCreateAgentRequest,
			confidence:     1.0,
			wantIntent:     jev.IntentCreateAgentRequest,
			wantCreate:     true,
			wantStatusJSON: true,
		},
		{
			name:       "high-confidence chat reply unchanged",
			message:    "hello, how are you?",
			choice:     jev.IntentChatReply,
			confidence: 0.9,
			wantIntent: jev.IntentChatReply,
		},
		{
			name:           "high-confidence handoff fulfills",
			message:        "delegate this to the reviewer peer",
			choice:         jev.IntentPeerHandoff,
			confidence:     0.88,
			wantIntent:     jev.IntentPeerHandoff,
			wantHandoff:    true,
			wantStatusJSON: true,
		},
		{
			name:       "high-confidence tool run fulfills",
			message:    "look up the kb article on refunds",
			choice:     jev.IntentToolRun,
			confidence: 0.85,
			wantIntent: jev.IntentToolRun,
			wantTool:   true,
		},
		{
			name:        "near-floor create gates to unclear",
			message:     "please create a helper agent for triage",
			choice:      jev.IntentCreateAgentRequest,
			confidence:  0.49,
			wantIntent:  jev.IntentUnclear,
			wantUnclear: true,
			wantClarify: true,
		},
		{
			name:        "truncated create gates to unclear",
			message:     "create",
			choice:      jev.IntentCreateAgentRequest,
			confidence:  0.42,
			wantIntent:  jev.IntentUnclear,
			wantUnclear: true,
			wantClarify: true,
		},
		{
			name:        "ambiguous truncated handoff gates to unclear",
			message:     "hand off",
			choice:      jev.IntentPeerHandoff,
			confidence:  0.42,
			wantIntent:  jev.IntentUnclear,
			wantUnclear: true,
			wantClarify: true,
		},
		{
			name:        "create between floor and bar keeps class without fulfillment",
			message:     "please create a helper agent for triage",
			choice:      jev.IntentCreateAgentRequest,
			confidence:  0.6,
			wantIntent:  jev.IntentCreateAgentRequest,
			wantClarify: true,
		},
		{
			name:        "handoff between floor and bar keeps class without fulfillment",
			message:     "delegate this to the reviewer peer",
			choice:      jev.IntentPeerHandoff,
			confidence:  0.6,
			wantIntent:  jev.IntentPeerHandoff,
			wantClarify: true,
		},
		{
			name:       "tool_run between floor and bar still fulfills",
			message:    "look up the kb article on refunds",
			choice:     jev.IntentToolRun,
			confidence: 0.6,
			wantIntent: jev.IntentToolRun,
			wantTool:   true,
		},
		{
			name:           "create at destructive bar fulfills",
			message:        "create an agent named Scout for research",
			choice:         jev.IntentCreateAgentRequest,
			confidence:     jev.DestructiveActionConfidenceBar,
			wantIntent:     jev.IntentCreateAgentRequest,
			wantCreate:     true,
			wantStatusJSON: true,
		},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			server := chatTestServer(t, true)
			server.config.Chat.JevIntentEnabled = true
			server.SetJevBackend(cannedJevIntent(t, fx.choice, fx.confidence))
			result, promptAndMeta := queueJevTurn(t, server, fx.message)
			parts := strings.SplitN(promptAndMeta, "\n---USERMETA---\n", 2)
			prompt, userMeta := parts[0], parts[1]
			if fx.wantStatusJSON {
				if !strings.Contains(prompt, "ANVIL_AGENT_RUN_STATUS_JSON=") {
					t.Fatalf("prompt missing STATUS_JSON: %s", prompt)
				}
			} else if strings.Contains(prompt, "ANVIL_AGENT_RUN_STATUS_JSON=") {
				t.Fatalf("prompt must not encourage STATUS_JSON: %s", prompt)
			}
			if fx.wantClarify && !strings.Contains(strings.ToLower(prompt), "clarifying question") {
				t.Fatalf("prompt should ask to clarify: %s", prompt)
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(userMeta), &meta); err != nil {
				t.Fatalf("user metadata is not JSON: %v", err)
			}
			if meta["jevIntent"] != fx.wantIntent {
				t.Fatalf("jevIntent = %v, want %q (meta %s)", meta["jevIntent"], fx.wantIntent, userMeta)
			}
			if meta["jevRawChoice"] != fx.choice {
				t.Fatalf("jevRawChoice = %v, want labeled %q", meta["jevRawChoice"], fx.choice)
			}
			if unclear, _ := meta["jevUnclear"].(bool); unclear != fx.wantUnclear {
				t.Fatalf("jevUnclear = %v, want %v", meta["jevUnclear"], fx.wantUnclear)
			}
			_, hasCreate := meta["jevNeedsManagerCreate"]
			_, hasHandoff := meta["jevNeedsPeerHandoff"]
			_, hasTool := meta["jevNeedsToolRun"]
			if hasCreate != fx.wantCreate {
				t.Fatalf("jevNeedsManagerCreate present=%v, want %v (meta %s)", hasCreate, fx.wantCreate, userMeta)
			}
			if hasHandoff != fx.wantHandoff {
				t.Fatalf("jevNeedsPeerHandoff present=%v, want %v (meta %s)", hasHandoff, fx.wantHandoff, userMeta)
			}
			if hasTool != fx.wantTool {
				t.Fatalf("jevNeedsToolRun present=%v, want %v (meta %s)", hasTool, fx.wantTool, userMeta)
			}
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if stored.Annotations[jevIntentAnnotation] != fx.wantIntent {
				t.Fatalf("run annotation %q = %q, want %q", jevIntentAnnotation, stored.Annotations[jevIntentAnnotation], fx.wantIntent)
			}
			_, hasCreateAnn := stored.Annotations[jevNeedsManagerCreateAnnotation]
			_, hasHandoffAnn := stored.Annotations[jevNeedsPeerHandoffAnnotation]
			_, hasToolAnn := stored.Annotations[jevNeedsToolRunAnnotation]
			if hasCreateAnn != fx.wantCreate {
				t.Fatalf("run manager-create present=%v, want %v", hasCreateAnn, fx.wantCreate)
			}
			if hasHandoffAnn != fx.wantHandoff {
				t.Fatalf("run peer-handoff present=%v, want %v", hasHandoffAnn, fx.wantHandoff)
			}
			if hasToolAnn != fx.wantTool {
				t.Fatalf("run tool-run present=%v, want %v", hasToolAnn, fx.wantTool)
			}
		})
	}
}
