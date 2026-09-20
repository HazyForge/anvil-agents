package substrate

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const maxACPEchoBodyBytes = 1 << 20

// ServeACPEcho is the spike ACP-over-HTTP handler for ActorTemplate/acp-spike.
// It implements initialize, session/new, and session/prompt (echo the frozen
// prompt as agent_message_chunk NDJSON plus a session/prompt result). GET
// /healthz is liveness. Token headers are ignored and never written back.
func ServeACPEcho(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet && (request.URL.Path == "/healthz" || request.URL.Path == "/readyz") {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
		return
	}
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxACPEchoBodyBytes+1))
	if err != nil {
		http.Error(writer, "read body", http.StatusBadRequest)
		return
	}
	if len(raw) > maxACPEchoBodyBytes {
		http.Error(writer, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		http.Error(writer, "empty body", http.StatusBadRequest)
		return
	}
	var rpc struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &rpc) != nil || strings.TrimSpace(rpc.Method) == "" {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(trimmed))
		return
	}
	id := rpc.ID
	if len(id) == 0 {
		id = json.RawMessage("1")
	}
	switch strings.TrimSpace(rpc.Method) {
	case "initialize":
		writeJSON(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result": map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"promptCapabilities": map[string]any{},
				},
				"agentInfo": map[string]any{
					"name":    "anvil-actor-acp",
					"version": "spike",
				},
			},
		})
	case "session/new":
		writeJSON(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result":  map[string]any{"sessionId": "echo-1"},
		})
	case "session/prompt":
		sessionID, prompt := parseACPEchoPrompt(rpc.Params)
		writer.Header().Set("Content-Type", "application/x-ndjson")
		writer.WriteHeader(http.StatusOK)
		update, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params": map[string]any{
				"sessionId": sessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]any{
						"type": "text",
						"text": prompt,
					},
				},
			},
		})
		result, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result":  map[string]any{"stopReason": "end_turn"},
		})
		_, _ = writer.Write(update)
		_, _ = writer.Write([]byte("\n"))
		_, _ = writer.Write(result)
		_, _ = writer.Write([]byte("\n"))
	default:
		writeJSON(writer, map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		})
	}
}

func parseACPEchoPrompt(params json.RawMessage) (sessionID, prompt string) {
	var envelope struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &envelope)
	sessionID = strings.TrimSpace(envelope.SessionID)
	if sessionID == "" {
		sessionID = "echo-1"
	}
	var out strings.Builder
	for _, block := range envelope.Prompt {
		if strings.EqualFold(strings.TrimSpace(block.Type), "text") {
			out.WriteString(block.Text)
		}
	}
	return sessionID, out.String()
}

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(payload)
}
