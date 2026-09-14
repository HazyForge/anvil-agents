package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func toolClientSessionFile(t *testing.T, endpoint string, expires time.Time) string {
	t.Helper()
	raw, err := json.Marshal(agentToolClientSession{URL: endpoint, Capability: strings.Repeat("a", 43), ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestAgentToolClientUsesScopedCapabilityAndPreservesWriteID(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/local/v1/agent-tool" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Anvil-Agent-Capability") != strings.Repeat("a", 43) {
			t.Error("wrong request scope or credential")
		}
		var body struct {
			Action string
			Args   map[string]string
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Action != "send_message" || body.Args["requestId"] != "same-turn-retry-id" || body.Args["text"] != "hello\nworld" {
			t.Error("tool arguments changed")
		}
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()
	file := toolClientSessionFile(t, server.URL+"/local/v1/agent-tool", time.Now().Add(time.Minute))
	var stdout, stderr bytes.Buffer
	code := RunAgentTool(context.Background(), []string{"--session-file", file, "send_message", "-"}, strings.NewReader(`{"profileName":"reviewer","text":"hello\nworld","requestId":"same-turn-retry-id"}`), &stdout, &stderr)
	if code != 0 || calls != 1 || stdout.String() != "{\"accepted\":true}\n" || stderr.Len() != 0 {
		t.Fatalf("request failed: %d %d %q %q", code, calls, stdout.String(), stderr.String())
	}
}

func TestAgentToolClientRefusesExpiredExternalAndRedirectedSessions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "https://example.invalid/stolen")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		endpoint string
		expires  time.Time
	}{
		{server.URL + "/local/v1/agent-tool", time.Now().Add(-time.Minute)},
		{"https://example.invalid/local/v1/agent-tool", time.Now().Add(time.Minute)},
		{server.URL + "/other", time.Now().Add(time.Minute)},
		{server.URL + "/local/v1/agent-tool?token=x", time.Now().Add(time.Minute)},
	} {
		var out, errOut bytes.Buffer
		if code := RunAgentTool(context.Background(), []string{"--session-file", toolClientSessionFile(t, tc.endpoint, tc.expires), "list_agents"}, strings.NewReader(""), &out, &errOut); code == 0 || out.Len() != 0 || strings.Contains(errOut.String(), strings.Repeat("a", 43)) {
			t.Error("invalid session accepted or capability leaked")
		}
	}
	if calls != 0 {
		t.Fatal("invalid session contacted a server")
	}
	var out, errOut bytes.Buffer
	if code := RunAgentTool(context.Background(), []string{"--session-file", toolClientSessionFile(t, server.URL+"/local/v1/agent-tool", time.Now().Add(time.Minute)), "list_agents"}, strings.NewReader(""), &out, &errOut); code == 0 || calls != 1 || out.Len() != 0 {
		t.Fatal("redirect accepted or retried")
	}
}

func TestAgentToolClientDoesNotRetryFailedWrite(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	code := RunAgentTool(context.Background(), []string{"--session-file", toolClientSessionFile(t, server.URL+"/local/v1/agent-tool", time.Now().Add(time.Minute)), "start_run", `{"profileName":"worker","prompt":"work","requestId":"same-id"}`}, strings.NewReader(""), &out, &errOut)
	if code == 0 || calls != 1 || !strings.Contains(out.String(), "unavailable") {
		t.Fatal("failed write was hidden or retried")
	}
}
