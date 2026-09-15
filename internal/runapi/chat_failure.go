package runapi

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const chatFailureLogLimit int64 = 2 * 1024 * 1024

const codexChatAuthenticationFailure = "Codex could not authenticate with its model provider. Configure or renew authentication for the selected remote harness, then start a new turn."

const hermesChatAuthenticationFailure = "Hermes cannot authenticate with xAI. Renew authentication for the selected remote Hermes harness with `hermes model`, then start a new turn."
const hermesChatProviderSetupFailure = "Hermes provider authentication or configuration is unavailable. Check the selected remote harness provider setup, then start a new turn."
const conflictingToolsChatFailure = "The harness could not start because its selected skill sets or tool sets define conflicting tools. Correct the agent's tool configuration, then start a new turn."
const chatWritePolicySetupFailure = "The agent's write-credential setup was blocked by the project's repository policy. Chat should remain available with read-only tools; the runner setup needs repair."

var chatAuthentication401 = regexp.MustCompile(`(?i)\b401\s+unauthorized\b`)

// Only interpret a provider's terminal native envelope. Tool results, retry
// diagnostics and arbitrary text are not evidence that provider auth failed.
// Never copy native error text: it may contain credentials or private URLs.
func extractChatFailure(backend agents.AgentRunHarnessBackendKind, output string) string {
	if int64(len(output)) > chatFailureLogLimit {
		return ""
	}
	if backend == agents.AgentRunHarnessBackendHermesAgent {
		// Hermes's adapter exits before inference with this exact two-line native
		// authentication diagnostic. Require the complete terminal suffix; never
		// infer auth from arbitrary 401 text or copy any provider error content.
		lines := strings.Split(strings.TrimSpace(chatANSI.ReplaceAllString(output, "")), "\n")
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(lines[len(lines)-1]), &envelope) == nil && len(envelope) == 3 {
			var version int
			if replyString(envelope["type"]) == "anvil.hermes.error" && json.Unmarshal(envelope["version"], &version) == nil && version == 1 && replyString(envelope["code"]) == "provider_configuration_unavailable" {
				return hermesChatProviderSetupFailure
			}
		}
		if len(lines) >= 2 && strings.TrimSpace(lines[len(lines)-2]) == "xAI OAuth state is missing access_token. Re-authenticate with `hermes model`." && strings.TrimSpace(lines[len(lines)-1]) == "Run `hermes model` to re-authenticate." {
			return hermesChatAuthenticationFailure
		}
		return ""
	}
	if backend != agents.AgentRunHarnessBackendCodex {
		return ""
	}
	var failure string
	for _, line := range strings.Split(output, "\n") {
		var event struct {
			Type  string `json:"type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "turn.failed":
			failure = ""
			if chatAuthentication401.MatchString(event.Error.Message) {
				failure = codexChatAuthenticationFailure
			}
		case "turn.started", "turn.completed":
			failure = ""
		}
	}
	return failure
}

func (server *Server) chatFailure(ctx context.Context, run *agents.AgentRun) string {
	// A display-only classification, never evidence that replay is safe. Native
	// log text cannot authorize a retry or change the project's write policy.
	const policyBootstrapError = "Hazy Trade agent credential bootstrap failed: the Hazy Trade default branch lacks the reviewed approval-integrity or restricted-update rules"
	if strings.TrimSpace(run.Status.Error) == policyBootstrapError || (strings.TrimSpace(run.Status.Output) == policyBootstrapError || strings.HasSuffix(strings.TrimSpace(run.Status.Output), "\n"+policyBootstrapError)) {
		return chatWritePolicySetupFailure
	}
	if run.Status.JobRef == nil && run.Status.RunnerPodRef == nil {
		for _, condition := range run.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "False" && condition.Reason == "ConflictingToolName" {
				return conflictingToolsChatFailure
			}
		}
	}
	backend := agents.AgentRunHarnessBackendKind(run.Status.Backend)
	if failure := extractChatFailure(backend, run.Status.Output); failure != "" {
		return failure
	}
	// Status output may truncate a terminal event. Use the same source as reply
	// recovery, which verifies this run's Job/Pod ownership and agent container.
	if (backend == agents.AgentRunHarnessBackendCodex || backend == agents.AgentRunHarnessBackendHermesAgent) && server.logs != nil {
		logCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		limit, tail := chatFailureLogLimit, int64(5000)
		stream, _, err := server.logs.Open(logCtx, run, corev1.PodLogOptions{LimitBytes: &limit, TailLines: &tail})
		if err == nil {
			defer stream.Close()
			raw, readErr := io.ReadAll(io.LimitReader(stream, limit+1))
			if readErr == nil {
				if failure := extractChatFailure(backend, string(raw)); failure != "" {
					return failure
				}
			}
		}
	}
	if reason := strings.TrimSpace(run.Status.Error); reason != "" {
		return reason
	}
	return "The harness run failed"
}

// Enrich only the most recent historical failed turn in this read response.
// The persisted transcript stays unchanged. Identity checks prevent following
// a reused name to unrelated logs; callers must already have run-read access.
func (server *Server) enrichChatFailureView(ctx context.Context, namespace string, turns []chat.Turn, messages []chat.Message) {
	if len(turns) == 0 || server.runs == nil {
		return
	}
	turn := &turns[len(turns)-1]
	if turn.Status != "failed" || turn.RunUID == "" || turn.RunName == "" {
		return
	}
	run := &agents.AgentRun{}
	if server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: turn.RunName}, run) != nil || string(run.UID) != turn.RunUID || run.Labels[chatTurnLabel] != turn.ID || run.Labels[chatThreadLabel] != turn.ThreadID || run.Spec.SourceRef.Kind != "ChatThread" || run.Spec.SourceRef.Name != turn.ThreadID || run.Status.Phase != agents.AgentRunPhaseFailed {
		return
	}
	reason := server.chatFailure(ctx, run)
	if reason != hermesChatProviderSetupFailure && reason != hermesChatAuthenticationFailure && reason != codexChatAuthenticationFailure && reason != conflictingToolsChatFailure && reason != chatWritePolicySetupFailure {
		return
	}
	turn.Error = reason
	for i := range messages {
		if messages[i].Role != chat.RoleSystem {
			continue
		}
		var metadata map[string]string
		if json.Unmarshal(messages[i].Metadata, &metadata) == nil && metadata["turnId"] == turn.ID && metadata["runName"] == turn.RunName && metadata["kind"] == "execution_error" {
			messages[i].Content = reason
		}
	}
}
