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
		{name: "chat reply unchanged", choice: jev.IntentChatReply, confidence: 0.9, wantPlain: true},
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

// TestChatJevNonCreateIntentsCarryNoManagerRequest ensures only the
// create_agent_request path arms the Wrapper/manager request flag.
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
				if strings.Contains(parts[0], "ANVIL_AGENT_RUN_STATUS_JSON=") {
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
			stored := &agentsv1alpha1.AgentRun{}
			if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, stored); err != nil {
				t.Fatal(err)
			}
			if _, ok := stored.Annotations[jevNeedsManagerCreateAnnotation]; ok {
				t.Fatalf("%s run must not carry %q", choice, jevNeedsManagerCreateAnnotation)
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
