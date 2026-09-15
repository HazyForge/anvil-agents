package runapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"k8s.io/apimachinery/pkg/types"
)

const hermesFinalFixture = `{"type":"anvil.hermes.final","version":1,"role":"assistant","text":"Public final answer"}`

func TestHermesReplyRequiresOwnedFinalEnvelope(t *testing.T) {
	for _, raw := range []string{
		"Reasoning: private fixture\nA plain answer", `{"role":"assistant","content":"untrusted generic output"}`,
		`{"type":"assistant","message":{"content":"untrusted generic output"}}`,
		`{"type":"result","result":"untrusted generic output"}`,
		`{"type":"tool_result","output":` + hermesFinalFixture + `}`,
		`{"type":"anvil.hermes.final","version":2,"role":"assistant","text":"wrong version"}`,
		`{"type":"anvil.hermes.final","version":1,"role":"tool","text":"wrong role"}`,
		`{"type":"anvil.hermes.final","version":1,"role":"assistant","text":"answer","reasoning":"private"}`,
		`{"type":"anvil.hermes.final","role":"assistant","text":"missing version"}`,
		`{"type":"anvil.hermes.final","version":1,"role":"assistant","text":"  "}`,
		hermesFinalFixture + "\n" + `{"type":"anvil.hermes.final","version":1,"role":"assistant","text":null}`,
	} {
		if reply, err := extractChatReply(agents.AgentRunHarnessBackendHermesAgent, raw); err == nil || reply != "" {
			t.Fatalf("non-final output accepted: reply=%q", reply)
		}
	}
	got, err := extractChatReply(agents.AgentRunHarnessBackendHermesAgent, "ANVIL_AGENT_RUN_START\nprivate native diagnostics\n"+hermesFinalFixture+"\nANVIL_AGENT_RUN_COMPLETE")
	if err != nil || got != "Public final answer" {
		t.Fatalf("actual final response: %q %v", got, err)
	}
}

func TestLegacyHermesHistoryHiddenFromAllChatViewsAndReplay(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	sentinel := "PRIVATE_LEGACY_REASONING_SENTINEL"
	_, _, err := s.chatStore.AppendMessages(ctx, "agents", thread.ID, []chat.Message{
		{Role: chat.RoleUser, Content: "user context"},
		{Role: chat.RoleAssistant, Content: sentinel, Metadata: json.RawMessage(`{"backend":"hermesAgent","runName":"old","turnId":"old"}`)},
		{Role: chat.RoleAssistant, Content: "Public final answer", Metadata: json.RawMessage(`{"backend":"hermesAgent","replyFormat":"anvil.hermes.final/v1"}`)},
		{Role: chat.RoleAssistant, Content: "Other harness answer", Metadata: json.RawMessage(`{"backend":"codex"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/messages"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+suffix, nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		s.routes().ServeHTTP(response, request)
		if response.Code != 200 || strings.Contains(response.Body.String(), sentinel) || !strings.Contains(response.Body.String(), legacyHermesReplyUnavailable) || !strings.Contains(response.Body.String(), "Public final answer") || !strings.Contains(response.Body.String(), "Other harness answer") {
			t.Fatalf("unsafe/incomplete chat view %s status=%d", suffix, response.Code)
		}
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[1].Content != sentinel || messages[1].Role != chat.RoleAssistant {
		t.Fatal("historical record was mutated")
	}
	prompt, err := buildChatPrompt(thread, messages, "continue")
	if err != nil || strings.Contains(prompt, sentinel) || strings.Contains(prompt, legacyHermesReplyUnavailable) || !strings.Contains(prompt, "Public final answer") {
		t.Fatal("legacy reasoning leaked into replayed prompt")
	}
}

func TestHermesCompletedTurnPersistsTrustedReplyProvenance(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseSucceeded
	run.Status.Backend = "hermesAgent"
	run.Status.Output = hermesFinalFixture
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
		t.Fatal(err)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
	if messages[1].Content != "Public final answer" || !strings.Contains(string(messages[1].Metadata), hermesReplyFormat) || safeChatMessages(messages)[1].Role != chat.RoleAssistant {
		t.Fatal("final answer missing safe format provenance")
	}
}

func TestHermesUnframedSuccessCannotPersistAnAssistantReply(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	s.logs = staticLogSource{err: ErrLogsPending}
	thread := newExecutionThread(t, s, `{}`)
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseSucceeded
	run.Status.Backend = "hermesAgent"
	run.Status.Output = "PRIVATE_UNFRAMED_REASONING\nPossible plain answer"
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].Status != "failed" {
		t.Fatalf("unsafe completion accepted: %v", err)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Role == chat.RoleAssistant || strings.Contains(message.Content, "PRIVATE_UNFRAMED_REASONING") {
			t.Fatal("unframed successful process leaked an assistant reply")
		}
	}
}
