package runapi

import (
	"context"
	"strings"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

const codexTerminalAuthFixture = `{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://provider.invalid/private?token=fixture-private-value"}}`

func TestExtractChatFailureTrustsOnlyTerminalProviderEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		backend      agents.AgentRunHarnessBackendKind
		want         string
	}{
		{"native terminal", codexTerminalAuthFixture, "codex", codexChatAuthenticationFailure},
		{"tool stdout", `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"401 Unauthorized"}}`, "codex", ""},
		{"tool nested native envelope", `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"{\"type\":\"turn.failed\",\"error\":{\"message\":\"401 Unauthorized\"}}"}}`, "codex", ""},
		{"unrelated text", "curl: HTTP 401 Unauthorized", "codex", ""},
		{"retry diagnostic", `{"type":"error","message":"Reconnecting: 401 Unauthorized"}`, "codex", ""},
		{"unknown native failure", `{"type":"turn.failed","error":{"message":"connection timeout"}}`, "codex", ""},
		{"other provider", codexTerminalAuthFixture, "agy", ""},
		{"unspecified provider", codexTerminalAuthFixture, "", ""},
		{"later different failure", codexTerminalAuthFixture + "\n" + `{"type":"turn.failed","error":{"message":"timeout"}}`, "codex", ""},
		{"later successful turn", codexTerminalAuthFixture + "\n" + `{"type":"turn.completed"}`, "codex", ""},
		{"malformed envelope", `{"type":"turn.failed","error":{"message":"401 Unauthorized`, "codex", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractChatFailure(tc.backend, tc.output); got != tc.want {
				t.Fatalf("failure = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChatFailureLogRecoveryAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		logs   staticLogSource
		output string
		want   string
	}{
		{"status native error", staticLogSource{err: ErrLogsPending}, codexTerminalAuthFixture, codexChatAuthenticationFailure},
		{"verified log recovery", staticLogSource{contents: codexTerminalAuthFixture}, "truncated native envelope", codexChatAuthenticationFailure},
		{"logs unavailable", staticLogSource{err: ErrLogsPending}, "", "Job has reached the specified backoff limit"},
		{"unrecognized output", staticLogSource{contents: "tool says 401 Unauthorized"}, "", "Job has reached the specified backoff limit"},
		{"bounded logs", staticLogSource{contents: strings.Repeat(" ", int(chatFailureLogLimit)) + codexTerminalAuthFixture}, "", "Job has reached the specified backoff limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := chatTestServer(t, true)
			s.logs = tc.logs
			run := &agents.AgentRun{}
			run.Status.Backend = "codex"
			run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "runner", Namespace: "agents"}
			run.Status.Output = tc.output
			run.Status.Error = "Job has reached the specified backoff limit"
			if got := s.chatFailure(context.Background(), run); got != tc.want {
				t.Fatalf("failure = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFailedChatTurnPersistsSafeNativeAuthGuidance(t *testing.T) {
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
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Backend = "codex"
	run.Status.Error = "Job has reached the specified backoff limit"
	run.Status.Output = codexTerminalAuthFixture
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].Status != "failed" || turns[0].Error != codexChatAuthenticationFailure {
		t.Fatalf("failed turn did not persist canned provider authentication guidance: %v", err)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "system" || messages[1].Content != codexChatAuthenticationFailure {
		t.Fatalf("failed run must persist only user input and safe system guidance, without an assistant reply: %v", err)
	}
}
