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

func newChatLatencyTestServer(t *testing.T, chatLatencyPath string) *Server {
	t.Helper()
	server, err := NewServer(Options{
		Listen:           "127.0.0.1:0",
		ConfigDir:        t.TempDir(),
		ChatLatencyJSONL: chatLatencyPath,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func TestChatLatencyUnsetIsNoOp204(t *testing.T) {
	t.Setenv(chatLatencyEnv, "")
	server := newChatLatencyTestServer(t, "")
	if server.chatLatencyEnabled() {
		t.Fatal("expected chat-latency sink to be disabled")
	}
	body := strings.NewReader(`{"waitingMs":2,"firstTokenMs":40,"source":"desktop-chat"}`)
	req := httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unset sink HTTP %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/local/v1/snapshot", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot HTTP %d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.ChatLatencyJSONLEnabled {
		t.Fatalf("snapshot should report chatLatencyJsonlEnabled=false, got %#v", snap)
	}
	if strings.Contains(rec.Body.String(), "chat-latency.jsonl") {
		t.Fatalf("snapshot must not leak the sink path: %s", rec.Body.String())
	}
}

func TestChatLatencySetAbsoluteAppendsOneLine(t *testing.T) {
	dir := t.TempDir()
	sink := filepath.Join(dir, "nested", "chat-latency.jsonl")
	server := newChatLatencyTestServer(t, sink)
	if !server.chatLatencyEnabled() {
		t.Fatal("expected chat-latency sink to be enabled")
	}

	body := strings.NewReader(`{"waitingMs":2,"firstTokenMs":40,"runningMs":42,"replyReadyMs":210,"source":"desktop-chat"}`)
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
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one JSONL line, got %d: %q", len(lines), string(raw))
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("decode line: %v", err)
	}
	if parsed["waitingMs"] != 2.0 || parsed["firstTokenMs"] != 40.0 || parsed["replyReadyMs"] != 210.0 {
		t.Fatalf("durations missing: %#v", parsed)
	}
	if parsed["source"] != "desktop-chat" {
		t.Fatalf("source missing: %#v", parsed)
	}
	ts, _ := parsed["ts"].(string)
	if strings.TrimSpace(ts) == "" {
		t.Fatalf("host should stamp ts when present or missing; got %#v", parsed)
	}

	// Second turn appends a second line; missing ts is stamped by the host.
	body = strings.NewReader(`{"failedMs":99}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", body)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second write HTTP %d %s", rec.Code, rec.Body.String())
	}
	raw, err = os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two JSONL lines, got %d: %q", len(lines), string(raw))
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["failedMs"] != 99.0 {
		t.Fatalf("failedMs missing: %#v", second)
	}
	if _, ok := second["source"]; ok {
		t.Fatalf("source should be absent when not sent: %#v", second)
	}
	if strings.TrimSpace(second["ts"].(string)) == "" {
		t.Fatalf("host should stamp ts when missing: %#v", second)
	}

	req = httptest.NewRequest(http.MethodGet, "/local/v1/snapshot", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.ChatLatencyJSONLEnabled {
		t.Fatalf("snapshot should report chatLatencyJsonlEnabled=true, got %#v", snap)
	}
}

func TestChatLatencyRelativeEnvIgnored(t *testing.T) {
	t.Setenv(chatLatencyEnv, "relative/chat-latency.jsonl")
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if server.chatLatencyEnabled() {
		t.Fatal("relative env path must resolve to disabled")
	}
	body := strings.NewReader(`{"waitingMs":1}`)
	req := httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", body)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("relative sink HTTP %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat("relative/chat-latency.jsonl"); !os.IsNotExist(err) {
		t.Fatal("relative sink must never create a file")
	}
}

func TestChatLatencyRelativeFlagIgnored(t *testing.T) {
	t.Setenv(chatLatencyEnv, "")
	server := newChatLatencyTestServer(t, "relative/chat-latency.jsonl")
	if server.chatLatencyEnabled() {
		t.Fatal("relative flag path must resolve to disabled")
	}
}

func TestChatLatencyBadBody400(t *testing.T) {
	dir := t.TempDir()
	sink := filepath.Join(dir, "chat-latency.jsonl")
	server := newChatLatencyTestServer(t, sink)
	for _, body := range []string{"", "not-json{", "[1,2]", `"waitingMs"`, "42"} {
		req := httptest.NewRequest(http.MethodPost, "/local/v1/chat-latency", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: HTTP %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(sink); !os.IsNotExist(err) {
		t.Fatal("bad bodies must not create the sink file")
	}
}

func TestResolveChatLatencySinkPrefersFlag(t *testing.T) {
	t.Setenv(chatLatencyEnv, filepath.Join(t.TempDir(), "env.jsonl"))
	flagPath := filepath.Join(t.TempDir(), "flag.jsonl")
	if got := resolveChatLatencySink(flagPath); got != flagPath {
		t.Fatalf("flag should win, got %q", got)
	}
	if got := resolveChatLatencySink(""); got == "" {
		t.Fatal("empty flag should fall back to the env path")
	}
	if got := resolveChatLatencySink("relative.jsonl"); got != "" {
		t.Fatalf("relative flag must disable, got %q", got)
	}
}
