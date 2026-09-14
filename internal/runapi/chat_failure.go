package runapi

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const chatFailureLogLimit int64 = 2 * 1024 * 1024

const codexChatAuthenticationFailure = "Codex could not authenticate with its model provider. Configure or renew authentication for the selected remote harness, then start a new turn."

var chatAuthentication401 = regexp.MustCompile(`(?i)\b401\s+unauthorized\b`)

// Only interpret a provider's terminal native envelope. Tool results, retry
// diagnostics and arbitrary text are not evidence that provider auth failed.
// Never copy native error text: it may contain credentials or private URLs.
func extractChatFailure(backend agents.AgentRunHarnessBackendKind, output string) string {
	if backend != agents.AgentRunHarnessBackendCodex || int64(len(output)) > chatFailureLogLimit {
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
	backend := agents.AgentRunHarnessBackendKind(run.Status.Backend)
	if failure := extractChatFailure(backend, run.Status.Output); failure != "" {
		return failure
	}
	// Status output may truncate a terminal event. Use the same source as reply
	// recovery, which verifies this run's Job/Pod ownership and agent container.
	if backend == agents.AgentRunHarnessBackendCodex && server.logs != nil {
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
