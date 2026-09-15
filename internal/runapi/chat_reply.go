package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"regexp"
	"strings"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

var errChatOutputPending = errors.New("completed harness output is not available yet")

var chatANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// extractChatReply reads assistant output from native event envelopes. Tool
// output, reasoning and runner diagnostics never become an assistant reply.
func extractChatReply(backend agents.AgentRunHarnessBackendKind, output string) (string, error) {
	output = chatANSI.ReplaceAllString(output, "")
	if backend == agents.AgentRunHarnessBackendOpenClaw {
		return extractOpenClawReply(output)
	}
	var parts []string
	var plain []string
	var final string
	structured := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "ANVIL_AGENT_RUN_START") {
			plain = nil
			continue
		}
		if strings.HasPrefix(line, "ANVIL_") {
			continue
		}
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &event) != nil {
			plain = append(plain, line)
			continue
		}
		structured = true
		typ, name := replyString(event["type"]), replyString(event["event"])
		switch backend {
		case agents.AgentRunHarnessBackendHermesAgent:
			// The owned Hermes adapter emits this only from its successful native
			// final_response. Generic stdout can contain reasoning and tool output.
			if typ == "anvil.hermes.final" {
				final = ""
				var version int
				if len(event) == 4 && json.Unmarshal(event["version"], &version) == nil && version == 1 && replyString(event["role"]) == "assistant" {
					final = strings.TrimSpace(replyString(event["text"]))
				}
			}
		case agents.AgentRunHarnessBackendPrimeAgent:
			// Prime v0.9.4 JSON mode emits complete native message_end events.
			// The last assistant completion owns the reply; tool-use preambles,
			// thinking blocks, retries, and agent_end message copies do not.
			if typ == "message_end" {
				var message struct {
					Role       string `json:"role"`
					StopReason string `json:"stopReason"`
					Content    []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				if json.Unmarshal(event["message"], &message) == nil && message.Role == "assistant" {
					final = ""
					if message.StopReason == "stop" || message.StopReason == "length" {
						var blocks []string
						for _, block := range message.Content {
							if block.Type == "text" {
								blocks = append(blocks, block.Text)
							}
						}
						final = strings.TrimSpace(strings.Join(blocks, "\n"))
					}
				}
			}
		case agents.AgentRunHarnessBackendCodex:
			if typ == "item.completed" {
				var item struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(event["item"], &item) == nil && item.Type == "agent_message" {
					parts = append(parts, item.Text)
				}
			}
		case agents.AgentRunHarnessBackendOpenCode:
			if typ == "text" {
				var part struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(event["part"], &part) == nil {
					parts = append(parts, part.Text)
				}
			}
		case agents.AgentRunHarnessBackendAgy:
			if name == "result" {
				var result struct {
					Status   string `json:"status"`
					Response string `json:"response"`
				}
				if json.Unmarshal(event["result"], &result) == nil && result.Status == "SUCCESS" {
					final = result.Response
				}
			}
		default:
			if typ == "assistant" || name == "assistant" {
				parts = append(parts, replyMessage(event["message"]))
			}
			if typ == "result" || name == "result" {
				if text := replyString(event["result"]); text != "" {
					final = text
				}
			}
			// Grok's one-shot JSON output contains a final response, separately from
			// its tool trace. Accept only that explicit final field.
			if backend == agents.AgentRunHarnessBackendGrokBuild && typ == "" && name == "" && replyString(event["stopReason"]) == "end_turn" {
				final = replyString(event["text"])
			}
			if replyString(event["role"]) == "assistant" {
				parts = append(parts, replyContent(event["content"]))
			}
		}
	}
	if final != "" {
		return strings.TrimSpace(final), nil
	}
	if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
		return text, nil
	}
	if !structured && backend != agents.AgentRunHarnessBackendHermesAgent && backend != agents.AgentRunHarnessBackendCodex && backend != agents.AgentRunHarnessBackendAgy && backend != agents.AgentRunHarnessBackendPrimeAgent {
		if text := strings.TrimSpace(strings.Join(plain, "\n")); text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("the harness completed without an identifiable assistant reply; inspect runner activity")
}
func replyString(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func replyMessage(raw json.RawMessage) string {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return replyContent(m.Content)
}
func replyContent(raw json.RawMessage) string {
	if s := replyString(raw); s != "" {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// A CR status holds only the last 32KiB of runner logs. If that truncates a
// native JSON envelope, recover it through the existing ownership-checked log
// source instead of saving a malformed or fabricated assistant response.
func (server *Server) chatReply(ctx context.Context, run *agents.AgentRun) (string, error) {
	backend := agents.AgentRunHarnessBackendKind(run.Status.Backend)
	reply, err := extractChatReply(backend, run.Status.Output)
	if err == nil || server.logs == nil {
		return reply, err
	}
	limit := int64(2 * 1024 * 1024)
	tail := int64(5000)
	pending := func() error {
		if run.Status.CompletedAt != nil && time.Since(run.Status.CompletedAt.Time) < 2*time.Minute {
			return errChatOutputPending
		}
		return err
	}
	stream, _, openErr := server.logs.Open(ctx, run, corev1.PodLogOptions{LimitBytes: &limit, TailLines: &tail})
	if openErr != nil {
		return "", pending()
	}
	defer stream.Close()
	raw, readErr := io.ReadAll(io.LimitReader(stream, limit+1))
	if readErr != nil || int64(len(raw)) > limit || len(raw) == 0 {
		return "", pending()
	}
	return extractChatReply(backend, string(raw))
}

const hermesReplyFormat = "anvil.hermes.final/v1"
const legacyHermesReplyUnavailable = "This older Hermes reply is unavailable because its runner did not separate the final answer from internal output. Send a new message after the updated Hermes runner is deployed."

// Legacy Hermes messages were derived from undifferentiated stdout. Never
// guess which text was public: hide the entire legacy answer in read views and
// replayed prompts. The original database record remains untouched.
func safeChatMessages(messages []chat.Message) []chat.Message {
	safe := append([]chat.Message(nil), messages...)
	for i := range safe {
		if safe[i].Role != chat.RoleAssistant {
			continue
		}
		var metadata map[string]any
		if json.Unmarshal(safe[i].Metadata, &metadata) != nil {
			continue
		}
		if metadata["backend"] == string(agents.AgentRunHarnessBackendOpenClaw) {
			if metadata["replyFormat"] == openClawReplyFormat {
				continue
			}
			if reply, err := extractChatReply(agents.AgentRunHarnessBackendOpenClaw, safe[i].Content); err == nil {
				safe[i].Content = reply
				metadata["replyFormat"] = openClawReplyFormat
			} else {
				safe[i].Role = chat.RoleSystem
				safe[i].Content = legacyOpenClawReplyUnavailable
				metadata["kind"] = "legacy_output_unavailable"
			}
			safe[i].Metadata, _ = json.Marshal(metadata)
			continue
		}
		if metadata["backend"] != string(agents.AgentRunHarnessBackendHermesAgent) || metadata["replyFormat"] == hermesReplyFormat {
			continue
		}
		safe[i].Role = chat.RoleSystem
		safe[i].Content = legacyHermesReplyUnavailable
		metadata["kind"] = "legacy_output_unavailable"
		safe[i].Metadata, _ = json.Marshal(metadata)
	}
	return safe
}
