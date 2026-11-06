package desktop

import (
	"encoding/json"
	"strings"
)

// Only public native assistant envelopes are eligible as durable replies.
// Tool results, thinking, malformed events, and a failed final turn never are.
func loopPublicReply(output, harness string) string {
	var parts []string
	final := ""
	failed := false
	for _, line := range strings.Split(output, "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) != nil || e == nil {
			continue
		}
		typ := loopString(e["type"])
		switch harness {
		case "prime", "pi":
			m := loopObject(e["message"])
			if typ == "message_end" && loopString(m["role"]) == "assistant" {
				reason := loopString(m["stopReason"])
				failed = reason == "error" || reason == "aborted"
				final = ""
				if reason == "stop" || reason == "length" {
					final = loopBlocks(m["content"], false)
				}
			}
		case "codex":
			if typ == "turn.failed" {
				failed = true
			}
			item := loopObject(e["item"])
			if typ == "item.completed" && loopString(item["type"]) == "agent_message" {
				parts = append(parts, loopString(item["text"]))
			}
		case "opencode":
			if typ == "error" {
				failed = true
			}
			if typ == "text" {
				parts = append(parts, loopString(loopObject(e["part"])["text"]))
			}
		case "agy":
			if loopString(e["event"]) == "result" {
				result := loopObject(e["result"])
				failed = loopString(result["status"]) != "SUCCESS"
				if !failed {
					final = loopString(result["response"])
				}
			}
		default:
			if typ == "assistant" {
				parts = append(parts, loopBlocks(loopObject(e["message"])["content"], true))
			}
			if typ == "result" {
				if e["is_error"] == true {
					failed = true
				} else {
					final = loopString(e["result"])
				}
			}
			if typ == "" && loopString(e["stopReason"]) == "end_turn" {
				final = loopString(e["text"])
			}
		}
	}
	if failed {
		return ""
	}
	reply := strings.TrimSpace(final)
	if reply == "" {
		reply = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if len(reply) > 128<<10 {
		return ""
	}
	return reply
}
func loopObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func loopString(v any) string         { s, _ := v.(string); return s }
func loopBlocks(v any, allowString bool) string {
	if s, ok := v.(string); ok && allowString {
		return s
	}
	blocks, ok := v.([]any)
	if !ok {
		return ""
	}
	var text []string
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			return ""
		}
		if _, ok = m["type"].(string); !ok {
			return ""
		}
		if value, present := m["text"]; present {
			if _, ok = value.(string); !ok {
				return ""
			}
		}
		if m["type"] == "text" {
			text = append(text, loopString(m["text"]))
		}
	}
	return strings.Join(text, "\n")
}
