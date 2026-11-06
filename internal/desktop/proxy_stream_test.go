package desktop

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func streamProxyServer(t *testing.T, upstream http.Handler) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(upstream)
	t.Cleanup(api.Close)
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		APIOrigin: api.URL,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The real host uses 30s/6m; shrink them so the test proves streams
	// outlive the host's per-request deadlines.
	front := httptest.NewUnstartedServer(server.Handler())
	front.Config.ReadTimeout = 250 * time.Millisecond
	front.Config.WriteTimeout = 250 * time.Millisecond
	front.Start()
	t.Cleanup(front.Close)
	return front
}

func TestAPIProxyKeepsChatEventStreamPastHostDeadlines(t *testing.T) {
	const events = 8
	front := streamProxyServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/namespaces/agents/chat/events" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		for i := 0; i < events; i++ {
			fmt.Fprintf(writer, "event: thread\ndata: {\"threadId\":\"t%d\"}\n\n", i)
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	request, _ := http.NewRequest(http.MethodGet, front.URL+"/api/v1/namespaces/agents/chat/events", nil)
	request.Header.Set("Authorization", "Bearer desktop-token")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	received := 0
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "event: thread") {
			received++
		}
		if err != nil {
			break
		}
	}
	if received != events {
		t.Fatalf("received %d of %d events; the host cut the stream at its request deadline", received, events)
	}
}

func TestAPIProxyPassesChatStreamWebSocketUpgrade(t *testing.T) {
	front := streamProxyServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") || !strings.Contains(request.Header.Get("Sec-WebSocket-Protocol"), "bearer.desktop-token") {
			http.Error(writer, "upgrade required", http.StatusBadRequest)
			return
		}
		conn, buffered, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Protocol: anvil-agents.chat-stream.v1\r\n\r\n")
		_ = buffered.Flush()
		for i := 0; i < 6; i++ {
			fmt.Fprintf(buffered, "tick%d\n", i)
			_ = buffered.Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /api/v1/namespaces/agents/chat/threads/t1/stream HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Protocol: anvil-agents.chat-stream.v1, bearer.desktop-token\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("upgrade status = %q %v", status, err)
	}
	ticks := 0
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "tick") {
			ticks++
		}
		if err != nil {
			break
		}
	}
	if ticks != 6 {
		t.Fatalf("received %d of 6 frames through the proxied WebSocket", ticks)
	}
}
