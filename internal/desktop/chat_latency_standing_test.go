package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The standing Desktop e2e (see docs/standing-inprocess-harness.md) records
// send → first-token timing plus the thread/session id, the standing vs Job
// path tag, and any error into .runtime/chat-latency.jsonl through this
// endpoint. New fields must persist; absent ones stay absent.
func TestChatLatencyStandingFieldsPersisted(t *testing.T) {
	dir := t.TempDir()
	sink := filepath.Join(dir, "chat-latency.jsonl")
	server := newChatLatencyTestServer(t, sink)

	body := strings.NewReader(`{"waitingMs":2,"firstTokenMs":40,"sendToFirstTokenMs":40,` +
		`"replyReadyMs":210,"source":"desktop-chat","threadId":"thread-1",` +
		`"sessionId":"standing-thread-1","path":"standing"}`)
	req := httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("chat-latency HTTP %d %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("read sink: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &parsed); err != nil {
		t.Fatalf("decode line: %v", err)
	}
	for key, want := range map[string]any{
		"sendToFirstTokenMs": 40.0,
		"firstTokenMs":       40.0,
		"source":             "desktop-chat",
		"threadId":           "thread-1",
		"sessionId":          "standing-thread-1",
		"path":               "standing",
	} {
		if parsed[key] != want {
			t.Fatalf("%s = %#v, want %#v (line %#v)", key, parsed[key], want, parsed)
		}
	}
	if _, ok := parsed["error"]; ok {
		t.Fatalf("error should be absent when not sent: %#v", parsed)
	}

	// A failed Job-plane turn records the error tag with no session.
	body = strings.NewReader(`{"failedMs":99,"source":"desktop-chat","threadId":"thread-2","path":"job","error":"open stream failed: unauthorized"}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second write HTTP %d %s", rec.Code, rec.Body.String())
	}
	raw, err = os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two JSONL lines, got %d: %q", len(lines), string(raw))
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["error"] != "open stream failed: unauthorized" || second["path"] != "job" {
		t.Fatalf("error/path missing: %#v", second)
	}
	if _, ok := second["sessionId"]; ok {
		t.Fatalf("sessionId should be absent when not sent: %#v", second)
	}
}
