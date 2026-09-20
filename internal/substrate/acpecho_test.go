package substrate

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeACPEchoPrompt(t *testing.T) {
	t.Parallel()

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": "sess-1",
			"prompt":    []map[string]string{{"type": "text", "text": "echo me"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	ServeACPEcho(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"sessionUpdate":"agent_message_chunk"`) || !strings.Contains(raw, "echo me") {
		t.Fatalf("body = %s", raw)
	}
	if !strings.Contains(raw, `"stopReason":"end_turn"`) {
		t.Fatalf("missing stopReason: %s", raw)
	}
}

func TestServeACPEchoHealthz(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	ServeACPEcho(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}
