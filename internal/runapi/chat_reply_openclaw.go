package runapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"k8s.io/apimachinery/pkg/types"
)

const openClawReplyFormat = "openclaw.payloads/v1"
const legacyOpenClawReplyUnavailable = "This older OpenClaw reply could not be recovered from its original runner output. Internal diagnostics have been hidden."

// OpenClaw --json writes a pretty-printed native envelope. Its payloads are
// public assistant messages; meta contains the complete private prompt and tool
// inventory. Native asynchronous diagnostics can even split strings inside that
// debug metadata. Decode only the public prefix and its early completion facts,
// never try to repair or reinterpret the private remainder as assistant output.
func extractOpenClawReply(output string) (string, error) {
	if len(output) > int(chatFailureLogLimit) {
		return "", fmt.Errorf("OpenClaw output exceeds the reply limit")
	}
	// Only real runner marker lines reset framing. The token can also occur
	// inside an assistant payload or the escaped private prompt.
	start := 0
	for _, line := range strings.SplitAfter(output, "\n") {
		if strings.TrimSuffix(line, "\n") == "ANVIL_AGENT_RUN_START" || strings.HasPrefix(line, "ANVIL_AGENT_RUN_START ") {
			output = output[start+len(line):]
			start = 0
			break
		}
		start += len(line)
	}

	var final string
	for offset := 0; offset < len(output); {
		end := strings.IndexByte(output[offset:], '\n')
		if end < 0 {
			end = len(output) - offset
		}
		line := output[offset : offset+end]
		// Native top-level envelopes start at column zero. Indented nested tool
		// results and quoted JSON strings cannot supply an assistant response.
		if strings.HasPrefix(line, "{") {
			decoder := json.NewDecoder(strings.NewReader(output[offset:]))
			opening, openErr := decoder.Token()
			key, keyErr := decoder.Token()
			native := openErr == nil && keyErr == nil && opening == json.Delim('{') && key == "payloads"
			reply, valid := openClawEnvelopePrefix(output[offset:])
			if native {
				final = reply
			}
			decoder = json.NewDecoder(strings.NewReader(output[offset:]))
			var ignored json.RawMessage
			if decoder.Decode(&ignored) == nil {
				offset += int(decoder.InputOffset())
				if offset < len(output) && output[offset] == '\n' {
					offset++
				}
				continue
			}
			if native {
				// Debug metadata may be malformed or truncated after the
				// verified public prefix. Never scan inside that private tail.
				if valid {
					return reply, nil
				}
				return "", fmt.Errorf("OpenClaw native result is incomplete")
			}
		}
		offset += end + 1
	}
	if final != "" {
		return final, nil
	}
	return "", fmt.Errorf("OpenClaw completed without an identifiable assistant payload")
}

func openClawEnvelopePrefix(output string) (string, bool) {
	decoder := json.NewDecoder(strings.NewReader(output))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false
	}
	token, err = decoder.Token()
	if err != nil || token != "payloads" {
		return "", false
	}
	var payloads []struct {
		Text    string `json:"text"`
		IsError bool   `json:"isError"`
	}
	if decoder.Decode(&payloads) != nil || len(payloads) == 0 {
		return "", false
	}
	var parts []string
	for _, payload := range payloads {
		if payload.IsError {
			return "", false
		}
		if text := strings.TrimSpace(payload.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	token, err = decoder.Token()
	if err != nil || token != "meta" {
		return "", false
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false
	}
	agent, completed := false, false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", false
		}
		switch key {
		case "agentMeta":
			var metadata struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			}
			if decoder.Decode(&metadata) != nil || metadata.Provider == "" || metadata.Model == "" {
				return "", false
			}
			agent = true
		case "aborted":
			var aborted *bool
			if decoder.Decode(&aborted) != nil || aborted == nil || *aborted {
				return "", false
			}
			completed = true
		default:
			var ignored json.RawMessage
			if decoder.Decode(&ignored) != nil {
				return "", false
			}
		}
		if agent && completed {
			return strings.Join(parts, "\n\n"), true
		}
	}
	return "", false
}

// Recover at most one historical reply per read, within a single three-second
// budget. No transcript or AgentRun writes occur. If original logs have expired,
// safeChatMessages hides the contaminated legacy content instead.
func (server *Server) enrichOpenClawReplyView(ctx context.Context, namespace string, turns []chat.Turn, messages []chat.Message) {
	if server.runs == nil {
		return
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := &messages[i]
		var metadata map[string]string
		if message.Role != chat.RoleAssistant || json.Unmarshal(message.Metadata, &metadata) != nil || metadata["backend"] != "openClaw" || metadata["replyFormat"] == openClawReplyFormat {
			continue
		}
		for _, turn := range turns {
			if turn.ID != metadata["turnId"] || turn.RunName != metadata["runName"] || turn.Status != "succeeded" || turn.RunUID == "" {
				continue
			}
			recoveryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			run := &agents.AgentRun{}
			if server.runs.Get(recoveryCtx, types.NamespacedName{Namespace: namespace, Name: turn.RunName}, run) != nil || string(run.UID) != turn.RunUID || run.Labels[chatTurnLabel] != turn.ID || run.Labels[chatThreadLabel] != turn.ThreadID || run.Spec.SourceRef.Kind != "ChatThread" || run.Spec.SourceRef.Name != turn.ThreadID || run.Status.Phase != agents.AgentRunPhaseSucceeded || run.Status.Backend != "openClaw" {
				return
			}
			if reply, err := server.chatReply(recoveryCtx, run); err == nil && reply != "" && len(reply) <= 64*1024 {
				message.Content = reply
				metadata["replyFormat"] = openClawReplyFormat
				message.Metadata, _ = json.Marshal(metadata)
			}
			return
		}
		return
	}
}
