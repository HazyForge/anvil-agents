package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// chatLatencyEnv is the host-process env var holding the absolute JSONL sink
// for live Desktop chat-latency reports. The browser/Electron renderer cannot
// read process.env or Node fs, so it POSTs to /local/v1/chat-latency and the
// loopback host appends one JSON line.
const chatLatencyEnv = "ANVIL_CHAT_LATENCY_JSONL"

const maxChatLatencyBytes = 16 << 10

// chatLatencyLine is the accepted POST body shape. It mirrors the fields
// formatChatLatencyJsonLine emits in web/desktop/src/wrapper/chatLatency.ts:
// durations plus optional source/ts. Unknown fields are ignored.
type chatLatencyLine struct {
	WaitingMs    *float64 `json:"waitingMs"`
	FirstTokenMs *float64 `json:"firstTokenMs"`
	RunningMs    *float64 `json:"runningMs"`
	ReplyReadyMs *float64 `json:"replyReadyMs"`
	FailedMs     *float64 `json:"failedMs"`
	Source       string   `json:"source"`
	Ts           string   `json:"ts"`
}

func isAbsoluteLatencyPath(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "/") {
		return true
	}
	if len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && (path[2] == '/' || path[2] == '\\') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

func isASCIILetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// resolveChatLatencyPath returns the trimmed absolute path or "" when
// disabled. Relative and empty values are ignored so the UI never breaks.
func resolveChatLatencyPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !isAbsoluteLatencyPath(trimmed) {
		return ""
	}
	return trimmed
}

// resolveChatLatencySink prefers the explicit flag value, falling back to the
// host-process environment. Empty/relative resolves to disabled ("").
func resolveChatLatencySink(flagValue string) string {
	if trimmed := strings.TrimSpace(flagValue); trimmed != "" {
		return resolveChatLatencyPath(trimmed)
	}
	return resolveChatLatencyPath(strings.TrimSpace(os.Getenv(chatLatencyEnv)))
}

func (s *Server) chatLatencyEnabled() bool {
	return s.chatLatencyPath != ""
}

func (s *Server) handleChatLatency(writer http.ResponseWriter, request *http.Request) {
	// Same loopback-only posture as the other /local/v1/* handlers: the
	// listener itself is loopback-restricted (requireLoopback in NewServer).
	defer request.Body.Close()
	if s.chatLatencyPath == "" {
		// No-op so the SPA never breaks chat when the sink is unset.
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	limited := io.LimitReader(request.Body, maxChatLatencyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_body", "unable to read chat-latency report")
		return
	}
	if len(raw) == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_json", "chat-latency report must be JSON")
		return
	}
	if len(raw) > maxChatLatencyBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "too_large", "chat-latency report is too large")
		return
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		writeError(writer, http.StatusBadRequest, "invalid_json", "chat-latency report must be JSON")
		return
	}
	// Must decode to a JSON object; arrays/scalars/strings are rejected.
	var probe any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&probe); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "chat-latency report must be JSON")
		return
	}
	obj, ok := probe.(map[string]any)
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_json", "chat-latency report must be a JSON object")
		return
	}
	var line chatLatencyLine
	if err := json.Unmarshal(raw, &line); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "chat-latency report must be JSON")
		return
	}
	_ = obj // decoded above to enforce the object shape; line carries the fields.
	payload := map[string]any{}
	if line.WaitingMs != nil {
		payload["waitingMs"] = *line.WaitingMs
	}
	if line.FirstTokenMs != nil {
		payload["firstTokenMs"] = *line.FirstTokenMs
	}
	if line.RunningMs != nil {
		payload["runningMs"] = *line.RunningMs
	}
	if line.ReplyReadyMs != nil {
		payload["replyReadyMs"] = *line.ReplyReadyMs
	}
	if line.FailedMs != nil {
		payload["failedMs"] = *line.FailedMs
	}
	if source := strings.TrimSpace(line.Source); source != "" {
		payload["source"] = source
	}
	if ts := strings.TrimSpace(line.Ts); ts != "" {
		payload["ts"] = ts
	} else {
		payload["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "encode_failed", "unable to encode chat-latency report")
		return
	}
	encoded = append(encoded, '\n')
	parent := filepath.Dir(s.chatLatencyPath)
	if parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			writeError(writer, http.StatusInternalServerError, "write_failed", "unable to persist chat-latency report")
			return
		}
	}
	file, err := os.OpenFile(s.chatLatencyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "write_failed", "unable to persist chat-latency report")
		return
	}
	defer file.Close()
	if _, err := file.Write(encoded); err != nil {
		writeError(writer, http.StatusInternalServerError, "write_failed", "unable to persist chat-latency report")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
