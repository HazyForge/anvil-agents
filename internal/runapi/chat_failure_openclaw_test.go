package runapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"k8s.io/apimachinery/pkg/types"
)

const openClawNativeRefreshFailure = "xAI OAuth refresh failed (400): Invalid or unknown refresh token"
const openClawWrappedRefreshFailure = "OAuth token refresh failed for xai: " + openClawNativeRefreshFailure
const openClawCompleteRefreshFailure = openClawWrappedRefreshFailure + ". Please try again or re-authenticate."
const openClawTerminalAuthFixture = "FailoverError: " + openClawCompleteRefreshFailure + " | " + openClawCompleteRefreshFailure + " | " + openClawWrappedRefreshFailure + " | " + openClawNativeRefreshFailure

func TestOpenClawChatFailureRequiresExactTerminalNativeDiagnostic(t *testing.T) {
	toolJSON, _ := json.Marshal(map[string]any{"type": "tool_result", "output": openClawTerminalAuthFixture})
	for _, tc := range []struct {
		name, output string
		backend      agents.AgentRunHarnessBackendKind
		want         string
	}{
		{"live duplicate wrappers", openClawTerminalAuthFixture, "openClaw", openClawChatAuthenticationFailure},
		{"single terminal wrapper", "FailoverError: " + openClawCompleteRefreshFailure, "openClaw", openClawChatAuthenticationFailure},
		{"startup and ANSI", "ANVIL_AGENT_RUN_START backend=openClaw\n\x1b[31m" + openClawTerminalAuthFixture + "\x1b[0m\n", "openClaw", openClawChatAuthenticationFailure},
		{"wrong backend", openClawTerminalAuthFixture, "hermesAgent", ""},
		{"unspecified backend", openClawTerminalAuthFixture, "", ""},
		{"nested tool JSON", string(toolJSON), "openClaw", ""},
		{"quoted diagnostic", `"` + openClawTerminalAuthFixture + `"`, "openClaw", ""},
		{"earlier retry recovered", openClawTerminalAuthFixture + "\nCompleted successfully", "openClaw", ""},
		{"later different failure", openClawTerminalAuthFixture + "\nFailoverError: request timed out", "openClaw", ""},
		{"incomplete native failure", "FailoverError: " + openClawWrappedRefreshFailure, "openClaw", ""},
		{"provider message alone", openClawCompleteRefreshFailure, "openClaw", ""},
		{"retry logger", "[diagnostic] error=" + openClawTerminalAuthFixture, "openClaw", ""},
		{"different provider", strings.ReplaceAll(openClawTerminalAuthFixture, "for xai:", "for other:"), "openClaw", ""},
		{"private trailing URL", openClawTerminalAuthFixture + " | https://private.invalid/?token=private", "openClaw", ""},
		{"private suffix", openClawTerminalAuthFixture + " token=private", "openClaw", ""},
		{"bounded status", strings.Repeat(" ", int(chatFailureLogLimit)) + openClawTerminalAuthFixture, "openClaw", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractChatFailure(tc.backend, tc.output); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOpenClawChatFailureOwnedLogRecovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		logs staticLogSource
		want string
	}{
		{"verified tail", staticLogSource{contents: openClawTerminalAuthFixture}, openClawChatAuthenticationFailure},
		{"unavailable logs", staticLogSource{err: ErrLogsPending}, "Job has reached the specified backoff limit"},
		{"bounded logs", staticLogSource{contents: strings.Repeat(" ", int(chatFailureLogLimit)) + openClawTerminalAuthFixture}, "Job has reached the specified backoff limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := chatTestServer(t, true)
			s.logs = tc.logs
			run := &agents.AgentRun{}
			run.Status.Backend = "openClaw"
			run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "runner", Namespace: "agents"}
			run.Status.Output = "truncated native failure"
			run.Status.Error = "Job has reached the specified backoff limit"
			if got := s.chatFailure(context.Background(), run); got != tc.want {
				t.Fatalf("owned log recovery got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOpenClawAuthenticationFailurePersistsWithoutReplay(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Backend = "openClaw"
	run.Status.Output = "private debug URL: https://private.invalid/?token=private\n" + openClawTerminalAuthFixture
	if err := s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
		if err != nil || len(turns) != 1 || turns[0].Status != "failed" || turns[0].RetryCount != 0 || turns[0].Error != openClawChatAuthenticationFailure {
			t.Fatalf("authentication failure must not replay: %#v %v", turns, err)
		}
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[1].Role != chat.RoleSystem || messages[1].Content != openClawChatAuthenticationFailure {
		t.Fatalf("safe failed transcript: %#v %v", messages, err)
	}
	runs := &agents.AgentRunList{}
	if err := s.writes.List(ctx, runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("authentication failure created another execution: %d %v", len(runs.Items), err)
	}
}

func TestOpenClawHistoricalFailureEnrichmentChecksRunIdentity(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	run := &agents.AgentRun{}
	run.Name, run.Namespace, run.UID = "openclaw-historical", "agents", "original-uid"
	run.Labels = map[string]string{chatTurnLabel: "turn", chatThreadLabel: "thread"}
	run.Spec.SourceRef.Kind, run.Spec.SourceRef.Name = "ChatThread", "thread"
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Backend = "openClaw"
	run.Status.Error = "Job has reached the specified backoff limit"
	// The old status truncated the terminal diagnostic; enrich from owned logs.
	run.Status.Output = "truncated diagnostic"
	run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "historical-runner", Namespace: "agents"}
	s.logs = staticLogSource{contents: openClawTerminalAuthFixture}
	if err := s.writes.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	turns := []chat.Turn{{ID: "turn", ThreadID: "thread", Status: "failed", RunUID: "original-uid", RunName: run.Name, Error: run.Status.Error}}
	messages := []chat.Message{{Role: chat.RoleSystem, Content: run.Status.Error, Metadata: json.RawMessage(`{"turnId":"turn","runName":"openclaw-historical","kind":"execution_error"}`)}, {Role: chat.RoleUser, Content: "hello"}}
	s.enrichChatFailureView(ctx, "agents", turns, messages)
	if turns[0].Error != openClawChatAuthenticationFailure || messages[0].Content != openClawChatAuthenticationFailure || messages[1].Content != "hello" {
		t.Fatal("historical native failure not enriched safely")
	}
	turns[0].RunUID, turns[0].Error = "replacement-uid", "unchanged"
	s.enrichChatFailureView(ctx, "agents", turns, messages)
	if turns[0].Error != "unchanged" {
		t.Fatal("historical enrichment followed a different run UID")
	}
}
