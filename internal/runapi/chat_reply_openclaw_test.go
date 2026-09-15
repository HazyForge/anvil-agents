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

const openClawPublicReply = "I am the code-slop auditor. I can inspect this project's source."
const openClawFinalFixture = `{
  "payloads": [
    {"text":"I am the code-slop auditor. I can inspect this project's source.","mediaUrl":null}
  ],
  "meta": {
    "durationMs":20457,
    "agentMeta":{"provider":"xai","model":"grok-4.5"},
    "aborted":false,
    "finalPromptText":"PRIVATE_PROMPT_SENTINEL",
    "finalAssistantVisibleText":"not the public payload"
  }
}`

func TestOpenClawReplyUsesOnlyNativePublicPayload(t *testing.T) {
	compact := strings.ReplaceAll(openClawFinalFixture, "\n", "")
	mentioningMarker := strings.Replace(openClawFinalFixture, openClawPublicReply, "The ANVIL_AGENT_RUN_START marker is a runner event.", 1)
	if reply, err := extractChatReply(agents.AgentRunHarnessBackendOpenClaw, mentioningMarker); err != nil || reply != "The ANVIL_AGENT_RUN_START marker is a runner event." {
		t.Fatal("quoted marker changed framing")
	}

	for _, raw := range []string{
		openClawFinalFixture, compact,
		"private tool setup\n{\"payloads\":[{\"text\":\"setup output\"}]}\nANVIL_AGENT_RUN_START backend=openClaw\n[warn] native diagnostic\n" + openClawFinalFixture + "\nANVIL_AGENT_RUN_COMPLETE",
		strings.Replace(openClawFinalFixture, "PRIVATE_PROMPT_SENTINEL", "private\n[warn] asynchronous log interrupted this JSON string\nmore private", 1),
		strings.Split(openClawFinalFixture, `"finalPromptText"`)[0] + `"finalPromptText":"` + strings.Repeat("private tool schema ", 5000),
	} {
		reply, err := extractChatReply(agents.AgentRunHarnessBackendOpenClaw, raw)
		if err != nil || reply != openClawPublicReply {
			t.Fatalf("native payload extraction: %q %v", reply, err)
		}
	}
	for _, raw := range []string{
		"plain diagnostics must never be a reply", `{"role":"assistant","content":"untrusted"}`,
		`{"type":"tool_result","output":` + compact + `}`,
		"{\n\"type\":\"tool_result\",\n\"output\":\n" + openClawFinalFixture + "\n}",
		openClawFinalFixture + "\n" + strings.Replace(openClawFinalFixture, `"aborted":false`, `"aborted":true`, 1),
		strings.Replace(openClawFinalFixture, `"aborted":false`, `"aborted":true`, 1),
		strings.Replace(openClawFinalFixture, `"aborted":false`, `"aborted":null`, 1),
		strings.Replace(openClawFinalFixture, `"mediaUrl":null`, `"isError":true`, 1),
		strings.Replace(openClawFinalFixture, `"agentMeta"`, `"otherMeta"`, 1),
		strings.Split(openClawFinalFixture, `"aborted"`)[0],
		`"finalPromptText":"PRIVATE_PROMPT_SENTINEL","finalAssistantVisibleText":"do not infer this"}`,
	} {
		if reply, err := extractChatReply(agents.AgentRunHarnessBackendOpenClaw, raw); err == nil || reply != "" {
			t.Fatalf("accepted private/nonterminal output: %q", reply)
		}
	}
}

func TestOpenClawStatusTailRecoversOwnedPayloadAndPersistsFormat(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseSucceeded
	run.Status.Backend = "openClaw"
	run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Namespace: "agents", Name: "owned-runner"}
	run.Status.Output = `"finalPromptText":"PRIVATE_STATUS_TAIL","tools":[],"finalAssistantVisibleText":"do not infer"}`
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	s.logs = staticLogSource{contents: openClawFinalFixture}
	if _, err = s.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
		t.Fatal(err)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
	if messages[1].Content != openClawPublicReply || !strings.Contains(string(messages[1].Metadata), openClawReplyFormat) {
		t.Fatal("native clean reply/provenance missing")
	}
}

func TestOpenClawLegacyViewsRecoverWithoutAnotherRunOrTranscriptRewrite(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	// The fake kube API does not allocate UIDs; bind one as production does.
	run.UID = "original-openclaw-run"
	if err = s.writes.Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = s.chatStore.RecordRun(ctx, accepted.Turn, string(run.UID)); err != nil {
		t.Fatal(err)
	}
	accepted.Turn.RunUID = string(run.UID)
	run.Status.Phase = agents.AgentRunPhaseSucceeded
	run.Status.Backend = "openClaw"
	run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Namespace: "agents", Name: "owned-runner"}
	run.Status.Output = "PRIVATE_STATUS_TAIL"
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"backend": "openClaw", "turnId": accepted.Turn.ID, "runName": run.Name})
	accepted.Turn.Status = "succeeded"
	if err = s.chatStore.CompleteTurn(ctx, accepted.Turn, chat.Message{Role: chat.RoleAssistant, Content: "PRIVATE_LEGACY_PROMPT", Metadata: metadata}); err != nil {
		t.Fatal(err)
	}
	s.logs = staticLogSource{contents: openClawFinalFixture}
	for _, suffix := range []string{"", "/messages"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+suffix, nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		s.routes().ServeHTTP(response, request)
		if response.Code != 200 || strings.Contains(response.Body.String(), "PRIVATE_") || !strings.Contains(response.Body.String(), openClawPublicReply) {
			t.Fatalf("view %s not repaired status=%d body=%s", suffix, response.Code, response.Body.String())
		}
	}
	messages, _ := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if messages[1].Content != "PRIVATE_LEGACY_PROMPT" {
		t.Fatal("stored legacy record changed")
	}
	prompt, err := buildChatPrompt(thread, messages, "continue")
	if err != nil || strings.Contains(prompt, "PRIVATE_") {
		t.Fatal("legacy private output leaked into replay")
	}
	runs := &agents.AgentRunList{}
	if err = s.writes.List(ctx, runs); err != nil || len(runs.Items) != 1 {
		t.Fatal("view repair created another execution")
	}
	turns, _ := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	turns[0].RunUID = "different-run"
	s.enrichOpenClawReplyView(ctx, "agents", turns, messages)
	if safeChatMessages(messages)[1].Content != legacyOpenClawReplyUnavailable {
		t.Fatal("mismatched run UID accepted")
	}
}
