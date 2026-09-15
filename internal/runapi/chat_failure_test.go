package runapi

import (
	"context"
	"encoding/json"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

const hermesTerminalAuthFixture = "xAI OAuth state is missing access_token. Re-authenticate with `hermes model`.\nRun `hermes model` to re-authenticate."

func TestHermesChatFailureRequiresExactTerminalDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		backend      agents.AgentRunHarnessBackendKind
		want         string
	}{
		{"native", hermesTerminalAuthFixture, "hermesAgent", hermesChatAuthenticationFailure},
		{"native after startup", "ANVIL_AGENT_RUN_START backend=hermesAgent\n" + hermesTerminalAuthFixture + " \n", "hermesAgent", hermesChatAuthenticationFailure},
		{"wrong backend", hermesTerminalAuthFixture, "grokBuild", ""},
		{"partial", strings.Split(hermesTerminalAuthFixture, "\n")[0], "hermesAgent", ""},
		{"later output", hermesTerminalAuthFixture + "\nCompleted successfully", "hermesAgent", ""},
		{"tool json", `{"tool_result":"xAI OAuth state is missing access_token"}`, "hermesAgent", ""},
		{"arbitrary401", "401 Unauthorized token=private", "hermesAgent", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractChatFailure(tc.backend, tc.output); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	s := chatTestServer(t, true)
	s.logs = staticLogSource{contents: hermesTerminalAuthFixture}
	run := &agents.AgentRun{}
	run.Status.Backend = "hermesAgent"
	run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "runner", Namespace: "agents"}
	run.Status.Error = "Job has reached the specified backoff limit"
	if got := s.chatFailure(context.Background(), run); got != hermesChatAuthenticationFailure {
		t.Fatalf("verified log recovery: %q", got)
	}
}

func TestChatFailurePrelaunchToolConflictAndHistoricalView(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	run := &agents.AgentRun{}
	run.Name = "historical"
	run.Namespace = "agents"
	run.UID = "original-uid"
	run.Labels = map[string]string{chatTurnLabel: "turn", chatThreadLabel: "thread"}
	run.Spec.SourceRef.Kind = "ChatThread"
	run.Spec.SourceRef.Name = "thread"
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Backend = "hermesAgent"
	run.Status.Error = "Job has reached the specified backoff limit"
	run.Status.Output = hermesTerminalAuthFixture
	if err := s.writes.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	turns := []chat.Turn{{ID: "turn", ThreadID: "thread", Status: "failed", RunUID: "original-uid", RunName: run.Name, Error: run.Status.Error}}
	messages := []chat.Message{{Role: chat.RoleSystem, Content: run.Status.Error, Metadata: json.RawMessage(`{"turnId":"turn","runName":"historical","kind":"execution_error"}`)}, {Role: chat.RoleUser, Content: "user message"}}
	s.enrichChatFailureView(ctx, "agents", turns, messages)
	if turns[0].Error != hermesChatAuthenticationFailure || messages[0].Content != hermesChatAuthenticationFailure || messages[1].Content != "user message" {
		t.Fatal("historical view failed to show safe auth guidance")
	}
	turns[0].RunUID = "different-uid"
	turns[0].Error = "unchanged"
	s.enrichChatFailureView(ctx, "agents", turns, messages)
	if turns[0].Error != "unchanged" {
		t.Fatal("followed replaced run")
	}
	run.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ConflictingToolName"}}
	if got := s.chatFailure(ctx, run); got != conflictingToolsChatFailure {
		t.Fatalf("prelaunch wording: %q", got)
	}
	run.Status.JobRef = &agents.NamespacedObjectReference{Name: "already-launched"}
	if got := s.chatFailure(ctx, run); got == conflictingToolsChatFailure {
		t.Fatal("claimed prelaunch failure despite existing job")
	}
}

func TestHermesTypedProviderSetupFailure(t *testing.T) {
	valid := `{"type":"anvil.hermes.error","version":1,"code":"provider_configuration_unavailable"}`
	if got := extractChatFailure(agents.AgentRunHarnessBackendHermesAgent, "ANVIL_AGENT_RUN_START\n"+valid); got != hermesChatProviderSetupFailure {
		t.Fatalf("typed failure not recognized: %q", got)
	}
	for _, raw := range []string{
		`{"type":"anvil.hermes.error","version":2,"code":"provider_configuration_unavailable"}`,
		`{"type":"anvil.hermes.error","version":1,"code":"unknown"}`,
		`{"type":"anvil.hermes.error","version":1,"code":"provider_configuration_unavailable","message":"private"}`,
		`{"type":"tool_result","output":` + valid + `}`,
		valid + "\n" + hermesFinalFixture,
		"provider_configuration_unavailable private-token",
	} {
		if got := extractChatFailure(agents.AgentRunHarnessBackendHermesAgent, raw); got != "" {
			t.Fatalf("untrusted/nonterminal diagnostic accepted: %q", got)
		}
	}
	if got := extractChatFailure(agents.AgentRunHarnessBackendCodex, valid); got != "" {
		t.Fatal("wrong provider accepted Hermes diagnostic")
	}
	s := chatTestServer(t, true)
	s.logs = staticLogSource{contents: valid}
	run := &agents.AgentRun{}
	run.Status.Backend = "hermesAgent"
	run.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "runner", Namespace: "agents"}
	if got := s.chatFailure(context.Background(), run); got != hermesChatProviderSetupFailure {
		t.Fatalf("verified log recovery failed: %q", got)
	}
}
