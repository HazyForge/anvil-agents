package runapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createStreamThread(t *testing.T, server *Server, body string) chat.Thread {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create thread: %d %s", response.Code, response.Body.String())
	}
	var thread chat.Thread
	if err := json.Unmarshal(response.Body.Bytes(), &thread); err != nil {
		t.Fatal(err)
	}
	return thread
}

func appendStreamMessage(t *testing.T, server *Server, threadID, content string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads/"+threadID+"/messages", bytes.NewBufferString(`{"content":`+strconv.Quote(content)+`}`))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("append message: %d %s", response.Code, response.Body.String())
	}
}

func sseStreamEvent(t *testing.T, body, event string) json.RawMessage {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "event: "+event {
			for _, next := range lines[i+1:] {
				if strings.HasPrefix(next, "data: ") {
					return json.RawMessage(strings.TrimPrefix(next, "data: "))
				}
				if strings.TrimSpace(next) == "" {
					break
				}
			}
		}
	}
	t.Fatalf("SSE body missing event %q:\n%s", event, body)
	return nil
}

func TestChatThreadStreamSSEReturnsSnapshotAndJobPlaneTerminal(t *testing.T) {
	server := chatTestServer(t, true)
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)
	appendStreamMessage(t, server, thread.ID, "hello standing stream")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	var snapshot struct {
		Type         string                  `json:"type"`
		Thread       chat.Thread             `json:"thread"`
		Messages     []chat.Message          `json:"messages"`
		MessageTotal int                     `json:"messageTotal"`
		Standing     *chatStreamStandingView `json:"standing"`
	}
	if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "snapshot"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Thread.ID != thread.ID || len(snapshot.Messages) == 0 || snapshot.MessageTotal == 0 {
		t.Fatalf("snapshot = %+v, want thread with durable messages", snapshot)
	}
	if snapshot.Standing != nil {
		t.Fatalf("snapshot standing = %+v, want nil without a backend", snapshot.Standing)
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "terminal"), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Code != "job_plane" {
		t.Fatalf("terminal = %+v, want job_plane without a backend", terminal)
	}
}

func TestChatThreadStreamRejectsQueryTokensAndBadTail(t *testing.T) {
	server := chatTestServer(t, true)
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream?access_token=leak", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "query_token_rejected") {
		t.Fatalf("query token: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream?messages=99999", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_messages") {
		t.Fatalf("bad tail: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth: %d %s", response.Code, response.Body.String())
	}
}

func TestChatThreadStreamResumesStandingSession(t *testing.T) {
	server := chatTestServer(t, true)
	harness := &agentsv1alpha1.AgentHarnessProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "standing-harness", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Backend:   agentsv1alpha1.AgentRunHarnessBackendSpec{Kind: agentsv1alpha1.AgentRunHarnessBackendOpenCode},
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{Runtime: agentsv1alpha1.AgentRunExecutionRuntimeInProcess},
		},
	}
	if err := server.writes.Create(t.Context(), harness); err != nil {
		t.Fatal(err)
	}
	server.SetStandingBackend(standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"harnessProfileName":"standing-harness","mode":"persona"}`)

	stream := func() (snapshot struct {
		Standing *chatStreamStandingView `json:"standing"`
	}, terminal chatStreamTerminal) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("stream: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "snapshot"), &snapshot); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "terminal"), &terminal); err != nil {
			t.Fatal(err)
		}
		return snapshot, terminal
	}

	snapshot, terminal := stream()
	if terminal.Code != "standing_ready" {
		t.Fatalf("terminal = %+v, want standing_ready", terminal)
	}
	if snapshot.Standing == nil || snapshot.Standing.SessionName != standing.SessionNameForThread(thread.ID) {
		t.Fatalf("snapshot standing = %+v, want session for the thread", snapshot.Standing)
	}
	if snapshot.Standing.Warm {
		t.Fatal("first stream must cold-create the thread session")
	}
	snapshot, _ = stream()
	if snapshot.Standing == nil || !snapshot.Standing.Warm || snapshot.Standing.Resumes < 1 {
		t.Fatalf("second snapshot standing = %+v, want warm resume across turns", snapshot.Standing)
	}
}

func TestChatThreadStreamJobPlaneWithBackendConfigured(t *testing.T) {
	server := chatTestServer(t, true)
	server.SetStandingBackend(standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", response.Code, response.Body.String())
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "terminal"), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Code != "job_plane" {
		t.Fatalf("terminal = %+v, want Job-plane threads to keep the existing run events path", terminal)
	}
}

// wsStreamDial performs a raw client handshake against a live test server so
// the WebSocket upgrade path is verified without a WebSocket dependency.
func wsStreamDial(t *testing.T, serverURL, path, token, protocols string) (net.Conn, *bufio.Reader, map[string]string) {
	t.Helper()
	host := strings.TrimPrefix(serverURL, "http://")
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	var request strings.Builder
	request.WriteString("GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(keyBytes) + "\r\n")
	if token != "" {
		request.WriteString("Authorization: Bearer " + token + "\r\n")
	}
	if protocols != "" {
		request.WriteString("Sec-WebSocket-Protocol: " + protocols + "\r\n")
	}
	request.WriteString("\r\n")
	if _, err := io.WriteString(conn, request.String()); err != nil {
		t.Fatalf("handshake write: %v", err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("handshake status: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status = %q, want 101", status)
	}
	headers := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("handshake headers: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		headers[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
	}
	return conn, reader, headers
}

func wsReadStreamFrame(t *testing.T, reader *bufio.Reader) (byte, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("frame header: %v", err)
	}
	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			t.Fatalf("extended length: %v", err)
		}
		length = int64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(reader, extended); err != nil {
			t.Fatalf("extended length: %v", err)
		}
		length = int64(binary.BigEndian.Uint64(extended))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("frame payload: %v", err)
	}
	return header[0] & 0x0F, payload
}

func TestChatThreadStreamWebSocketUpgrade(t *testing.T) {
	server := chatTestServer(t, true)
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)
	live := httptest.NewServer(server.routes())
	defer live.Close()

	conn, reader, _ := wsStreamDial(t, live.URL, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", "valid", "")
	defer conn.Close()
	op, snapshotRaw := wsReadStreamFrame(t, reader)
	if op != 0x1 {
		t.Fatalf("opcode = %d, want text", op)
	}
	var snapshot struct {
		Type   string      `json:"type"`
		Thread chat.Thread `json:"thread"`
	}
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		t.Fatalf("snapshot: %v in %s", err, snapshotRaw)
	}
	if snapshot.Type != "snapshot" || snapshot.Thread.ID != thread.ID {
		t.Fatalf("snapshot = %s", snapshotRaw)
	}
	op, terminalRaw := wsReadStreamFrame(t, reader)
	if op != 0x1 {
		t.Fatalf("opcode = %d, want text", op)
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(terminalRaw, &terminal); err != nil {
		t.Fatalf("terminal: %v in %s", err, terminalRaw)
	}
	if terminal.Type != "terminal" || terminal.Code != "job_plane" {
		t.Fatalf("terminal = %s", terminalRaw)
	}
	// Slice 1 closes after the terminal frame: no long-lived subscription yet.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadAll(conn); err == nil {
		t.Log("server closed the stream cleanly after terminal")
	}
}

func TestChatThreadStreamWebSocketSubprotocolAuth(t *testing.T) {
	server := chatTestServer(t, true)
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)
	live := httptest.NewServer(server.routes())
	defer live.Close()

	conn, reader, headers := wsStreamDial(t, live.URL,
		"/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", "",
		chatStreamProtocol+", bearer.valid")
	defer conn.Close()
	if headers["sec-websocket-protocol"] != chatStreamProtocol {
		t.Fatalf("selected protocol = %q, want %q", headers["sec-websocket-protocol"], chatStreamProtocol)
	}
	_, snapshotRaw := wsReadStreamFrame(t, reader)
	if !strings.Contains(string(snapshotRaw), thread.ID) {
		t.Fatalf("snapshot = %s", snapshotRaw)
	}
}

func TestChatThreadStreamWebSocketRejectsBadAuth(t *testing.T) {
	server := chatTestServer(t, true)
	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)
	live := httptest.NewServer(server.routes())
	defer live.Close()

	dialRaw := func(protocols string) string {
		host := strings.TrimPrefix(live.URL, "http://")
		conn, err := net.DialTimeout("tcp", host, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		keyBytes := make([]byte, 16)
		if _, err := rand.Read(keyBytes); err != nil {
			t.Fatalf("rand: %v", err)
		}
		request := "GET /api/v1/namespaces/agents/chat/threads/" + thread.ID + "/stream HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(keyBytes) + "\r\n"
		if protocols != "" {
			request += "Sec-WebSocket-Protocol: " + protocols + "\r\n"
		}
		request += "\r\n"
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatalf("handshake write: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		raw, err := io.ReadAll(conn)
		if err != nil && len(raw) == 0 {
			t.Fatalf("handshake read: %v", err)
		}
		return string(raw)
	}

	// No credentials at all: no 101.
	if raw := dialRaw(""); !strings.Contains(raw, "401") {
		t.Fatalf("anonymous upgrade: %q, want 401", raw)
	}
	// Bearer carrier without the versioned protocol: no 101.
	if raw := dialRaw("bearer.valid"); !strings.Contains(raw, "401") {
		t.Fatalf("protocol-less bearer: %q, want 401", raw)
	}
	// Versioned protocol without a bearer: no 101.
	if raw := dialRaw(chatStreamProtocol); !strings.Contains(raw, "401") {
		t.Fatalf("token-less protocol: %q, want 401", raw)
	}
	// Query-string tokens are never accepted, even on the stream route.
	host := strings.TrimPrefix(live.URL, "http://")
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	request := "GET /api/v1/namespaces/agents/chat/threads/" + thread.ID + "/stream?access_token=leak HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(keyBytes) + "\r\nAuthorization: Bearer valid\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, _ := io.ReadAll(conn)
	if !strings.Contains(string(raw), "400") || !strings.Contains(string(raw), "query_token_rejected") {
		t.Fatalf("query token upgrade: %q, want 400 query_token_rejected", raw)
	}
}
