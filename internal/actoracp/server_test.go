package actoracp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// The peer speaks native ACP on an in-memory duplex stream. No process, model,
// credential store, Kubernetes API or external network is involved.
type inertPeer struct {
	conn            net.Conn
	mu              sync.Mutex
	writeMu         sync.Mutex
	newCount        int
	initializeCount int
	prompts         []string
	sessions        []string
	order           []string
	onPrompt        func(*inertPeer, message, string, int)
	onCancel        func(*inertPeer, string)
	onNew           func(*inertPeer, json.RawMessage, string)
	responses       chan message
	closed          chan struct{}
}

func fixture(t *testing.T, cfg Config, onPrompt func(*inertPeer, message, string, int), wrappers ...func(net.Conn) io.ReadWriteCloser) (*Server, *inertPeer, *httptest.Server) {
	t.Helper()
	left, right := net.Pipe()
	peer := &inertPeer{conn: right, onPrompt: onPrompt, responses: make(chan message, 8), closed: make(chan struct{})}
	go peer.run()
	var transport io.ReadWriteCloser = left
	if len(wrappers) > 0 {
		transport = wrappers[0](left)
	}
	client := NewClient(transport)
	t.Cleanup(func() { _ = client.Close(); _ = right.Close() })
	if cfg.Workdir == "" {
		cfg.Workdir = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	server, err := NewServer(ctx, client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, peer, httpServer
}

func (p *inertPeer) send(value any) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = json.NewEncoder(p.conn).Encode(value)
}
func (p *inertPeer) result(id json.RawMessage, result any) {
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func (p *inertPeer) update(id, kind, text string) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": id, "update": map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}}})
}
func (p *inertPeer) run() {
	defer close(p.closed)
	defer p.conn.Close()
	decoder := json.NewDecoder(p.conn)
	for {
		var request message
		if decoder.Decode(&request) != nil {
			return
		}
		switch request.Method {
		case "initialize":
			p.mu.Lock()
			p.initializeCount++
			p.mu.Unlock()
			p.result(request.ID, map[string]any{"protocolVersion": 1})
		case "session/new":
			var params struct {
				Cwd        string `json:"cwd"`
				MCPServers []any  `json:"mcpServers"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Cwd == "" || len(params.MCPServers) != 0 {
				p.conn.Close()
				return
			}
			p.mu.Lock()
			p.newCount++
			id := fmt.Sprintf("native-%d", p.newCount)
			p.mu.Unlock()
			p.mu.Lock()
			callback := p.onNew
			p.mu.Unlock()
			if callback != nil {
				go callback(p, request.ID, id)
			} else {
				p.result(request.ID, map[string]any{"sessionId": id})
			}
		case "session/prompt":
			var params struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(request.Params, &params)
			var prompt strings.Builder
			for _, block := range params.Prompt {
				prompt.WriteString(block.Text)
			}
			p.mu.Lock()
			p.prompts = append(p.prompts, prompt.String())
			p.sessions = append(p.sessions, params.SessionID)
			p.order = append(p.order, "prompt")
			turn := len(p.prompts)
			p.mu.Unlock()
			go p.onPrompt(p, request, params.SessionID, turn)
		case "session/cancel":
			p.mu.Lock()
			p.order = append(p.order, "cancel")
			p.mu.Unlock()
			var params struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(request.Params, &params)
			p.mu.Lock()
			callback := p.onCancel
			p.mu.Unlock()
			if callback != nil {
				callback(p, params.SessionID)
			}
		default:
			select {
			case p.responses <- request:
			case <-p.closed:
				return
			}
		}
	}
}

func post(t *testing.T, server *httptest.Server, id any, method string, params any) *http.Response {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != nil {
		request["id"] = id
	}
	body, _ := json.Marshal(request)
	response, err := server.Client().Post(server.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func promptParams(id, text string) any {
	return map[string]any{"sessionId": id, "prompt": []any{map[string]any{"type": "text", "text": text}}}
}
func readMessage(t *testing.T, reader *bufio.Reader) message {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var msg message
	if err := json.Unmarshal(line, &msg); err != nil {
		t.Fatalf("invalid JSON %s: %v", line, err)
	}
	return msg
}
func generate(t *testing.T, server *httptest.Server, session, prompt string) (substrate.GenerateResult, error) {
	t.Helper()
	client, err := substrate.NewAtenetClientHTTP(substrate.AtenetConfig{Endpoint: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return client.Generate(ctx, substrate.GenerateRequest{ActorName: "actor", Atespace: "namespace", SessionID: session, Prompt: prompt})
}

func TestHTTPStreamsBeforeCompletionAndReusesNativeSession(t *testing.T) {
	t.Parallel()
	proceed := make(chan struct{})
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		if turn == 1 {
			p.update(id, "agent_thought_chunk", "PRIVATE_THOUGHT")
			p.update(id, "tool_call", "PRIVATE_TOOL")
			p.update("another-native-session", "agent_message_chunk", "WRONG_SESSION")
			p.update(id, "agent_message_chunk", "first chunk")
			select {
			case <-proceed:
			case <-p.closed:
				return
			}
			p.update(id, "agent_message_chunk", " then completed")
		} else {
			p.update(id, "agent_message_chunk", "continued in the same native conversation")
		}
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	response := post(t, server, "turn-a", "session/prompt", promptParams("thread-1", "frozen first prompt"))
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	first := readMessage(t, reader)
	if first.Method != "session/update" || !bytes.Contains(first.Params, []byte("first chunk")) || !bytes.Contains(first.Params, []byte("thread-1")) {
		t.Fatalf("first frame %+v", first)
	}
	// Reaching this assertion while the native peer is blocked proves flush.
	close(proceed)
	second := readMessage(t, reader)
	completed := readMessage(t, reader)
	if second.Method != "session/update" || string(completed.ID) != `"turn-a"` || string(completed.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("completion %+v / %+v", second, completed)
	}
	result, err := generate(t, server, "thread-1", "frozen second prompt")
	if err != nil || result.Text != "continued in the same native conversation" || result.StopReason != "end_turn" {
		t.Fatalf("second turn %+v %v", result, err)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.initializeCount != 1 || peer.newCount != 1 || len(peer.sessions) != 2 || peer.sessions[0] != peer.sessions[1] {
		t.Fatalf("setup/identity %d %d %v", peer.initializeCount, peer.newCount, peer.sessions)
	}
	if fmt.Sprint(peer.prompts) != "[frozen first prompt frozen second prompt]" {
		t.Fatalf("prompts %q", peer.prompts)
	}
}

func TestCancelWaitsForNativeCompletionAndReusesSession(t *testing.T) {
	var activeID json.RawMessage
	var activeSession string
	var activeMu sync.Mutex
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		if turn == 1 {
			activeMu.Lock()
			activeID = append(json.RawMessage(nil), request.ID...)
			activeSession = id
			activeMu.Unlock()
			p.update(id, "agent_message_chunk", "partial")
			return
		}
		p.update(id, "agent_message_chunk", "recovered")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onCancel = func(p *inertPeer, id string) {
		activeMu.Lock()
		defer activeMu.Unlock()
		if id == activeSession {
			p.result(activeID, map[string]any{"stopReason": "cancelled"})
		}
	}
	peer.mu.Unlock()
	response := post(t, server, 1, "session/prompt", promptParams("thread-cancel", "first"))
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	_ = readMessage(t, reader)
	busy := post(t, server, 2, "session/prompt", promptParams("thread-cancel", "must not execute"))
	busyBody, _ := io.ReadAll(busy.Body)
	busy.Body.Close()
	if !bytes.Contains(busyBody, []byte("session busy")) {
		t.Fatalf("concurrent turn %s", busyBody)
	}
	cancel := post(t, server, nil, "session/cancel", map[string]any{"sessionId": "thread-cancel"})
	cancel.Body.Close()
	if cancel.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status %d", cancel.StatusCode)
	}
	completed := readMessage(t, reader)
	if !bytes.Contains(completed.Result, []byte("cancelled")) {
		t.Fatalf("cancelled completion %+v", completed)
	}
	result, err := generate(t, server, "thread-cancel", "next")
	if err != nil || result.Text != "recovered" {
		t.Fatalf("next turn %+v %v", result, err)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.newCount != 1 || len(peer.prompts) != 2 || peer.sessions[0] != peer.sessions[1] {
		t.Fatalf("cancel/reuse %d %v", peer.newCount, peer.prompts)
	}
}

func TestTimeoutAndPeerExitCannotCompletePartialReply(t *testing.T) {
	for _, scenario := range []string{"timeout", "exit", "rpc-error", "missing-stop", "unknown-stop", "output-limit"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, server := fixture(t, Config{TurnTimeout: 100 * time.Millisecond}, func(p *inertPeer, request message, id string, turn int) {
				p.update(id, "agent_message_chunk", "partial")
				switch scenario {
				case "timeout":
					return
				case "exit":
					p.conn.Close()
				case "rpc-error":
					p.send(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": rpcError{Code: -32000, Message: "SECRET_DIAGNOSTIC"}})
				case "missing-stop":
					p.result(request.ID, map[string]any{})
				case "unknown-stop":
					p.result(request.ID, map[string]any{"stopReason": "unproven"})
				case "output-limit":
					p.update(id, "agent_message_chunk", strings.Repeat("x", maxStreamBytes))
					p.result(request.ID, map[string]any{"stopReason": "end_turn"})
				}
			})
			result, err := generate(t, server, "thread-failure", "do not complete partial")
			if err == nil || result.Text != "" || strings.Contains(err.Error(), "SECRET_DIAGNOSTIC") {
				t.Fatalf("failure falsely completed %+v %v", result, err)
			}
			if scenario == "missing-stop" || scenario == "unknown-stop" {
				ready, err := server.Client().Get(server.URL + "/readyz")
				if err != nil {
					t.Fatal(err)
				}
				ready.Body.Close()
				if ready.StatusCode != http.StatusServiceUnavailable {
					t.Fatal("invalid terminal result left connection ready")
				}
				if _, err := generate(t, server, "thread-failure", "next"); err == nil {
					t.Fatal("invalid terminal result reused")
				}
			}
		})
	}
}

func TestHTTPDisconnectClosesHarnessWithoutReplaying(t *testing.T) {
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		p.update(id, "agent_message_chunk", "partial")
	})
	ctx, cancel := context.WithCancel(context.Background())
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/prompt", "params": promptParams("thread-drop", "first")})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(body))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = readMessage(t, bufio.NewReader(response.Body))
	cancel()
	response.Body.Close()
	select {
	case <-peer.closed:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not close native connection")
	}
	ready, err := server.Client().Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	ready.Body.Close()
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("dead harness readiness %d", ready.StatusCode)
	}
	if _, err := generate(t, server, "thread-drop", "must not replay"); err == nil {
		t.Fatal("dead harness accepted next turn")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.prompts) != 1 {
		t.Fatalf("unexpected replay %v", peer.prompts)
	}
}

func TestOperatorScopeAndClientCapabilitiesStayClosed(t *testing.T) {
	_, peer, server := fixture(t, Config{MaxSessions: 1}, func(p *inertPeer, request message, id string, turn int) {
		p.send(map[string]any{"jsonrpc": "2.0", "id": "permission", "method": "session/request_permission", "params": map[string]any{"sessionId": id}})
		p.send(map[string]any{"jsonrpc": "2.0", "id": "filesystem", "method": "fs/read_text_file", "params": map[string]any{"path": "/credential"}})
		p.update(id, "agent_message_chunk", "scoped")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	for _, params := range []any{map[string]any{"cwd": "/other", "mcpServers": []any{}}, map[string]any{"mcpServers": []any{map[string]any{"command": "unapproved"}}}} {
		response := post(t, server, 1, "session/new", params)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if !bytes.Contains(body, []byte("operator owned")) {
			t.Fatalf("scope override %s", body)
		}
	}
	result, err := generate(t, server, "thread-scope", "first")
	if err != nil || result.Text != "scoped" {
		t.Fatalf("scoped result %+v %v", result, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case response := <-peer.responses:
			if string(response.ID) == `"permission"` && !bytes.Contains(response.Result, []byte("cancelled")) {
				t.Fatalf("permission response %+v", response)
			}
			if string(response.ID) == `"filesystem"` && response.Error == nil {
				t.Fatal("filesystem authority granted")
			}
		case <-time.After(time.Second):
			t.Fatal("client callback not answered")
		}
	}
	if _, err := generate(t, server, "another-thread", "over capacity"); err == nil {
		t.Fatal("session capacity not enforced")
	}
	unsupported := post(t, server, 5, "authenticate", map[string]any{})
	defer unsupported.Body.Close()
	body, _ := io.ReadAll(unsupported.Body)
	if !bytes.Contains(body, []byte("method unavailable")) {
		t.Fatalf("authenticate response %s", body)
	}
}

func TestNearLimitStreamStillIncludesCompletion(t *testing.T) {
	const externalSession = "near-limit"
	frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": externalSession, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": ""}}}})
	textLength := maxStreamBytes - 4096 - len(frame) - 1
	_, _, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		p.update(id, "agent_message_chunk", strings.Repeat("x", textLength))
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	result, err := generate(t, server, externalSession, "finish near output limit")
	if err != nil || len(result.Text) != textLength || result.StopReason != "end_turn" {
		t.Fatalf("near limit length=%d stop=%q err=%v", len(result.Text), result.StopReason, err)
	}
}

func TestCompletedResultBeforeEOFIsNotDiscarded(t *testing.T) {
	for i := 0; i < 12; i++ {
		_, _, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
			p.update(id, "agent_message_chunk", "complete before EOF")
			p.result(request.ID, map[string]any{"stopReason": "end_turn"})
			p.conn.Close()
		})
		result, err := generate(t, server, "thread-eof", "complete")
		if err != nil || result.Text != "complete before EOF" {
			t.Fatalf("iteration %d result=%+v err=%v", i, result, err)
		}
	}
}

func TestAtenetCancellationRejectsPartialOutputWithoutReplay(t *testing.T) {
	started := make(chan json.RawMessage, 1)
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		if turn == 1 {
			p.update(id, "agent_message_chunk", "cancelled partial text")
			started <- request.ID
			return
		}
		p.update(id, "agent_message_chunk", "next turn in same session")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onCancel = func(p *inertPeer, id string) {
		requestID := <-started
		p.result(requestID, map[string]any{"stopReason": "cancelled"})
	}
	peer.mu.Unlock()
	type outcome struct {
		result substrate.GenerateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := generate(t, server, "thread-native-cancel", "first")
		done <- outcome{result, err}
	}()
	// The peer has accepted the prompt before cancellation is sent.
	var originalID json.RawMessage
	select {
	case originalID = <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	started <- originalID
	response := post(t, server, nil, "session/cancel", map[string]any{"sessionId": "thread-native-cancel"})
	response.Body.Close()
	select {
	case got := <-done:
		if !errors.Is(got.err, substrate.ErrGenerateCancelled) || errors.Is(got.err, substrate.ErrGenerateTransient) || got.result.Text != "" {
			t.Fatalf("cancelled turn %+v %v", got.result, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt did not finish")
	}
	result, err := generate(t, server, "thread-native-cancel", "next")
	if err != nil || result.Text != "next turn in same session" {
		t.Fatalf("same-session recovery %+v %v", result, err)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.newCount != 1 || len(peer.prompts) != 2 {
		t.Fatalf("cancelled prompt replayed/new session %d %v", peer.newCount, peer.prompts)
	}
}

func TestCancellationDuringSessionSetupPreventsPromptExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) {
		p.update(id, "agent_message_chunk", "after setup cancellation")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, requestID json.RawMessage, id string) {
		close(started)
		select {
		case <-release:
		case <-p.closed:
			return
		}
		p.result(requestID, map[string]any{"sessionId": id})
	}
	peer.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := generate(t, server, "thread-setup-cancel", "must not execute"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session setup not started")
	}
	response := post(t, server, nil, "session/cancel", map[string]any{"sessionId": "thread-setup-cancel"})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status %d", response.StatusCode)
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, substrate.ErrGenerateCancelled) {
			t.Fatalf("setup cancellation %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled setup did not finish")
	}
	peer.mu.Lock()
	prompts := len(peer.prompts)
	peer.mu.Unlock()
	if prompts != 0 {
		t.Fatal("cancelled setup executed a prompt")
	}
	result, err := generate(t, server, "thread-setup-cancel", "next")
	if err != nil || result.Text != "after setup cancellation" {
		t.Fatalf("setup reuse %+v %v", result, err)
	}
}

type delayedAcknowledgement struct {
	reader  *bytes.Reader
	written chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (d *delayedAcknowledgement) Read(body []byte) (int, error) {
	<-d.written
	return d.reader.Read(body)
}
func (d *delayedAcknowledgement) Write(body []byte) (int, error) {
	close(d.written)
	<-d.closed
	return 0, io.ErrClosedPipe
}
func (d *delayedAcknowledgement) Close() error { d.once.Do(func() { close(d.closed) }); return nil }

func TestQueuedCompletionSurvivesEOFBeforeWriteAcknowledgement(t *testing.T) {
	transport := &delayedAcknowledgement{reader: bytes.NewReader([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":1}}\n")), written: make(chan struct{}), closed: make(chan struct{})}
	client := NewClient(transport)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := client.call(ctx, "initialize", map[string]any{}, "", nil, nil)
	if err != nil || !bytes.Contains(result, []byte("protocolVersion")) {
		t.Fatalf("queued completed result %s %v", result, err)
	}
}

type gatedPromptWrite struct {
	net.Conn
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedPromptWrite) open()        { g.once.Do(func() { close(g.release) }) }
func (g *gatedPromptWrite) Close() error { g.open(); return g.Conn.Close() }
func (g *gatedPromptWrite) Write(body []byte) (int, error) {
	var request message
	_ = json.Unmarshal(bytes.TrimSpace(body), &request)
	if request.Method == "session/prompt" {
		close(g.started)
		<-g.release
	}
	return g.Conn.Write(body)
}

func TestCancellationBeforePromptWriteIsDeliveredAfterPrompt(t *testing.T) {
	var transport *gatedPromptWrite
	accepted := make(chan json.RawMessage, 1)
	_, peer, server := fixture(t, Config{}, func(p *inertPeer, request message, id string, turn int) { accepted <- request.ID }, func(conn net.Conn) io.ReadWriteCloser {
		transport = &gatedPromptWrite{Conn: conn, started: make(chan struct{}), release: make(chan struct{})}
		return transport
	})
	peer.mu.Lock()
	peer.onCancel = func(p *inertPeer, id string) {
		requestID := <-accepted
		p.result(requestID, map[string]any{"stopReason": "cancelled"})
	}
	peer.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := generate(t, server, "thread-send-cancel", "cancel before write"); done <- err }()
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("prompt send did not reach gate")
	}
	response := post(t, server, nil, "session/cancel", map[string]any{"sessionId": "thread-send-cancel"})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel status %d", response.StatusCode)
	}
	transport.open()
	select {
	case err := <-done:
		if !errors.Is(err, substrate.ErrGenerateCancelled) {
			t.Fatalf("send cancellation %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("send cancellation did not complete")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if fmt.Sprint(peer.order) != "[prompt cancel]" {
		t.Fatalf("native order %v", peer.order)
	}
}

func TestFailedSessionCreationDoesNotConsumeCapacity(t *testing.T) {
	_, peer, server := fixture(t, Config{MaxSessions: 1}, func(p *inertPeer, request message, id string, turn int) {
		p.update(id, "agent_message_chunk", "after setup failure")
		p.result(request.ID, map[string]any{"stopReason": "end_turn"})
	})
	peer.mu.Lock()
	peer.onNew = func(p *inertPeer, requestID json.RawMessage, id string) {
		if id == "native-1" {
			p.send(map[string]any{"jsonrpc": "2.0", "id": requestID, "error": rpcError{Code: -32000, Message: "INERT_SETUP_FAILURE"}})
			return
		}
		p.result(requestID, map[string]any{"sessionId": id})
	}
	peer.mu.Unlock()
	for i := 0; i < 2; i++ {
		response := post(t, server, i+1, "session/new", map[string]any{})
		var result message
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if i == 0 && result.Error == nil {
			t.Fatal("expected inert first setup failure")
		}
		if i == 1 && result.Error != nil {
			t.Fatalf("failed setup consumed session capacity: %+v", result.Error)
		}
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.newCount != 2 {
		t.Fatalf("native setup attempts %d", peer.newCount)
	}
}
