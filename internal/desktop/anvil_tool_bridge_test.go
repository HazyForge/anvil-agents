package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func bridgeSessionFixture(t *testing.T, s *Server, ctx context.Context, namespace, token string, emit func(string, any) error) (agentToolSessionFile, string, string, func()) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://localhost:1738/local/v1/chat/stream", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	instructions, connected, closeSession, err := s.prepareAgentTools(ctx, request, Discoverer{}, invokeTarget{Mode: HarnessTargetNative}, namespace, emit)
	if err != nil || !connected {
		t.Fatalf("prepare connected=%v err=%v", connected, err)
	}
	t.Cleanup(closeSession)
	match := regexp.MustCompile(`--session-file '([^']+)'`).FindStringSubmatch(instructions)
	if len(match) != 2 {
		t.Fatal("missing session command")
	}
	path := match[1]
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file agentToolSessionFile
	if err = json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file, path, instructions, closeSession
}

func callBridge(t *testing.T, s *Server, capability, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://localhost:1738/local/v1/agent-tool", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(agentToolCapabilityHeader, capability)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	return response
}

func TestAgentToolBridgeScopesAndRevokesCapability(t *testing.T) {
	const token = "oidc-only-in-wrapper-memory"
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("OIDC missing upstream")
		}
		if r.URL.Path != "/api/v1/namespaces/team-one/agent-run-profiles" {
			t.Errorf("escaped scope: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	s, err := NewServer(Options{APIOrigin: upstream.URL, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []string
	file, path, instructions, closeSession := bridgeSessionFixture(t, s, ctx, "team-one", token, func(event string, value any) error {
		raw, _ := json.Marshal(value)
		events = append(events, event+string(raw))
		return nil
	})
	raw, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	directory, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0600 || directory.Mode().Perm() != 0700 {
		t.Fatal("capability file is not private")
	}
	if !strings.Contains(instructions, "You are the user's Anvil assistant") || !strings.Contains(instructions, "inspect with tools before answering") {
		t.Fatal("connected Anvil persona missing")
	}
	if strings.Contains(instructions, token) || bytes.Contains(raw, []byte(token)) || strings.Contains(instructions, file.Capability) {
		t.Fatal("credential leaked into prompt or capability file")
	}
	if file.URL != "http://127.0.0.1:1738/local/v1/agent-tool" {
		t.Fatalf("URL %q", file.URL)
	}
	for _, body := range []string{`{"action":"list_agents","args":{"namespace":"other"}}`, `{"action":"get_run","args":{"name":"../../secrets"}}`, `{"action":"kubectl","args":{}}`, `{"action":"list_agents","url":"https://evil.example"}`} {
		response := callBridge(t, s, file.Capability, body)
		if response.Code != 400 {
			t.Fatalf("invalid action HTTP%d %s", response.Code, response.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("invalid input contacted API")
	}
	response := callBridge(t, s, file.Capability, `{"action":"list_agents","args":{}}`)
	if response.Code != 200 || response.Body.String() != `{"items":[]}` || calls != 1 {
		t.Fatalf("HTTP%d %s", response.Code, response.Body.String())
	}
	if len(events) == 0 || !strings.Contains(events[len(events)-1], `"status":"succeeded"`) {
		t.Fatal("missing public activity")
	}
	for _, event := range events {
		if strings.Contains(event, token) || strings.Contains(event, "team-one") {
			t.Fatal("activity leaked scope/credentials")
		}
	}
	closeSession()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("session file survived turn")
	}
	response = callBridge(t, s, file.Capability, `{"action":"list_agents","args":{}}`)
	if response.Code != 401 || calls != 1 {
		t.Fatal("revoked capability remained usable")
	}
}

func TestAgentToolBridgeExpiryOriginAndDisconnectedContext(t *testing.T) {
	s, err := NewServer(Options{APIOrigin: "https://anvil.example", ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	file, _, _, _ := bridgeSessionFixture(t, s, ctx, "anvilhub", "test-token", func(string, any) error { return nil })
	request := httptest.NewRequest(http.MethodPost, "http://localhost:1738/local/v1/agent-tool", strings.NewReader(`{"action":"list_agents"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(agentToolCapabilityHeader, file.Capability)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	s.mu.Lock()
	s.agentToolSessions[file.Capability].expiresAt = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if callBridge(t, s, file.Capability, `{"action":"list_agents"}`).Code != 401 {
		t.Fatal("expired capability accepted")
	}
	request = httptest.NewRequest(http.MethodPost, "http://localhost:1738/local/v1/chat/stream", nil)
	instructions, connected, closeSession, err := s.prepareAgentTools(ctx, request, Discoverer{}, invokeTarget{}, "anvilhub", nil)
	closeSession()
	if err != nil || connected || !strings.Contains(instructions, "not connected") {
		t.Fatal("unauthenticated local context incorrect")
	}
	_, _, closeSession, err = s.prepareAgentTools(ctx, request, Discoverer{}, invokeTarget{}, "../other", nil)
	closeSession()
	if err == nil {
		t.Fatal("invalid namespace accepted")
	}
}

func TestAgentToolBridgeCancelsInFlightRequests(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer upstream.Close()
	s, err := NewServer(Options{APIOrigin: upstream.URL, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var eventsMu sync.Mutex
	file, _, _, closeSession := bridgeSessionFixture(t, s, context.Background(), "anvilhub", "test-token", func(string, any) error { eventsMu.Lock(); defer eventsMu.Unlock(); return nil })
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- callBridge(t, s, file.Capability, `{"action":"list_agents"}`) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not start")
	}
	closeSession()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("turn end did not cancel upstream")
	}
	select {
	case response := <-finished:
		if response.Code != 502 {
			t.Fatalf("HTTP%d", response.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("tool handler did not exit")
	}
}

func TestChatBridgeNeverPassesOIDCToLocalHarness(t *testing.T) {
	binDir, configDir := t.TempDir(), t.TempDir()
	promptPath, envPath := filepath.Join(binDir, "prompt"), filepath.Join(binDir, "environment")
	t.Setenv("ANVIL_TEST_PROMPT_CAPTURE", promptPath)
	t.Setenv("ANVIL_TEST_ENV_CAPTURE", envPath)
	writeExec(t, filepath.Join(binDir, "prime-agent"), `#!/bin/sh
cat > "$ANVIL_TEST_PROMPT_CAPTURE"
env > "$ANVIL_TEST_ENV_CAPTURE"
printf '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}\n'
`)
	s := streamTestServer(t, binDir, configDir, HarnessTargetNative)
	prefs := s.currentPrefs()
	prefs.APIOrigin = "https://anvil.example"
	s.setPrefs(prefs)
	body := `{"harness":"prime","prompt":"hello","remoteNamespace":"anvilhub"}`
	request := httptest.NewRequest(http.MethodPost, "http://localhost:1738/local/v1/chat/stream", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer OIDC-NEVER-IN-CHILD")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"anvilConnected":true`) {
		t.Fatalf("HTTP%d %s", response.Code, response.Body.String())
	}
	prompt, _ := os.ReadFile(promptPath)
	env, _ := os.ReadFile(envPath)
	if !bytes.Contains(prompt, []byte("agent-tool --session-file")) {
		t.Fatal("tool command missing from harness prompt")
	}
	if bytes.Contains(prompt, []byte("OIDC-NEVER-IN-CHILD")) || bytes.Contains(env, []byte("OIDC-NEVER-IN-CHILD")) || strings.Contains(response.Body.String(), "OIDC-NEVER-IN-CHILD") {
		t.Fatal("OIDC reached child or response")
	}
	match := regexp.MustCompile(`--session-file '([^']+)'`).FindSubmatch(prompt)
	if len(match) != 2 {
		t.Fatal("missing path")
	}
	if _, err := os.Stat(string(match[1])); !os.IsNotExist(err) {
		t.Fatal("turn completion did not remove capability")
	}
	s.mu.Lock()
	remaining := len(s.agentToolSessions)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("session survived completed harness")
	}
}

// Tool callbacks and subprocess stdout share one SSE response writer. Exercise
// both concurrently over a real HTTP stream, rather than only invoking helpers.
func TestChatBridgeSerializesToolAndHarnessEvents(t *testing.T) {
	binDir, configDir := t.TempDir(), t.TempDir()
	promptPath, gate := filepath.Join(binDir, "prompt"), filepath.Join(binDir, "gate")
	t.Setenv("ANVIL_TEST_PROMPT_CAPTURE", promptPath)
	t.Setenv("ANVIL_TEST_TOOL_GATE", gate)
	writeExec(t, filepath.Join(binDir, "prime-agent"), `#!/bin/sh
cat > "$ANVIL_TEST_PROMPT_CAPTURE"
while [ ! -f "$ANVIL_TEST_TOOL_GATE" ]; do
 printf '{"type":"ready"}\n'
 sleep 0.005
done
printf '{"type":"message_end","message":{"role":"assistant","content":[]}}\n'
`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer upstream.Close()
	s := streamTestServer(t, binDir, configDir, HarnessTargetNative)
	prefs := s.currentPrefs()
	prefs.APIOrigin = upstream.URL
	s.setPrefs(prefs)
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, host.URL+"/local/v1/chat/stream", strings.NewReader(`{"harness":"prime","prompt":"hello","remoteNamespace":"anvilhub"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer only-upstream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var prompt []byte
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		prompt, _ = os.ReadFile(promptPath)
		if bytes.Contains(prompt, []byte("--session-file")) {
			break
		}
	}
	match := regexp.MustCompile(`--session-file '([^']+)'`).FindSubmatch(prompt)
	if len(match) != 2 {
		t.Fatal("missing capability path")
	}
	raw, err := os.ReadFile(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	var session agentToolSessionFile
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	callsDone := make(chan bool, 1)
	go func() {
		good := true
		for i := 0; i < 5; i++ {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, session.URL, strings.NewReader(`{"action":"list_agents","args":{}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(agentToolCapabilityHeader, session.Capability)
			resp, callErr := http.DefaultClient.Do(req)
			if callErr != nil {
				good = false
				break
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				good = false
				break
			}
		}
		_ = os.WriteFile(gate, []byte("done"), 0600)
		callsDone <- good
	}()
	stream, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !<-callsDone {
		t.Fatal("tool call failed")
	}
	var toolEvents, stdoutEvents int
	event := ""
	for _, line := range strings.Split(string(stream), "\n") {
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			if !json.Valid([]byte(payload)) {
				t.Fatalf("interleaved SSE data: %q", payload)
			}
			if event == "anvil_tool" {
				toolEvents++
			}
			if event == "stdout" {
				stdoutEvents++
			}
		}
	}
	if toolEvents != 10 || stdoutEvents < 1 || !bytes.Contains(stream, []byte("event: result")) {
		t.Fatalf("events tools=%d stdout=%d", toolEvents, stdoutEvents)
	}
	if bytes.Contains(stream, []byte("only-upstream")) {
		t.Fatal("OIDC leaked to stream")
	}
}
