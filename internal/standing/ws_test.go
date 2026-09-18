package standing

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsTestClient performs a raw RFC 6455 client handshake so the test exercises
// the server upgrade without a WebSocket dependency.
func wsTestClient(t *testing.T, url, protocol string) (net.Conn, *bufio.Reader) {
	t.Helper()
	host := strings.TrimPrefix(url, "http://")
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	var request strings.Builder
	request.WriteString("GET /stream HTTP/1.1\r\nHost: " + host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n")
	if protocol != "" {
		request.WriteString("Sec-WebSocket-Protocol: " + protocol + "\r\n")
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
	if !strings.EqualFold(headers["upgrade"], "websocket") {
		t.Fatalf("upgrade header = %q", headers["upgrade"])
	}
	if accept := headers["sec-websocket-accept"]; accept == "" {
		t.Fatal("missing Sec-WebSocket-Accept")
	}
	return conn, reader
}

func wsReadServerFrame(t *testing.T, reader *bufio.Reader) (byte, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		t.Fatalf("frame header: %v", err)
	}
	if header[0]&0x80 == 0 {
		t.Fatal("server frames must be final in this helper")
	}
	if header[1]&0x80 != 0 {
		t.Fatal("server frames must not be masked")
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

func wsSendClientText(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	mask := []byte{0x1, 0x2, 0x3, 0x4}
	frame := []byte{0x80 | wsOpText, 0x80 | byte(len(payload))}
	frame = append(frame, mask...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	frame = append(frame, masked...)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("client write: %v", err)
	}
}

func TestUpgradeRejectsNonWebSocket(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := Upgrade(writer, request, ""); err == nil {
			t.Error("expected upgrade rejection")
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	for _, headers := range []map[string]string{
		{},
		{"Upgrade": "websocket"},
		{"Upgrade": "websocket", "Connection": "keep-alive"},
		{"Upgrade": "websocket", "Connection": "upgrade", "Sec-WebSocket-Version": "8"},
	} {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("headers %v: status = %d, want 400", headers, response.StatusCode)
		}
	}
}

func TestUpgradeStreamsTextBothDirections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := Upgrade(writer, request, "anvil-agents.chat-stream.v1")
		if err != nil {
			http.Error(writer, "upgrade", http.StatusBadRequest)
			return
		}
		defer conn.Close()
		op, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if op != wsOpText {
			return
		}
		_ = conn.WriteText([]byte(fmt.Sprintf(`{"type":"echo","message":%q}`, string(payload))))
	}))
	defer server.Close()

	conn, reader := wsTestClient(t, server.URL, "anvil-agents.chat-stream.v1")
	defer conn.Close()
	wsSendClientText(t, conn, []byte("hello standing"))
	op, payload := wsReadServerFrame(t, reader)
	if op != wsOpText {
		t.Fatalf("opcode = %d, want text", op)
	}
	if !strings.Contains(string(payload), "hello standing") {
		t.Fatalf("echo = %q", payload)
	}
}

func TestUpgradeAnswersPingAndEchoesClose(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := Upgrade(writer, request, "")
		if err != nil {
			http.Error(writer, "upgrade", http.StatusBadRequest)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	conn, reader := wsTestClient(t, server.URL, "")
	defer conn.Close()
	ping := []byte{0x80 | wsOpPing, 0x80 | 4, 0x1, 0x2, 0x3, 0x4}
	for i, b := range []byte("ping") {
		ping = append(ping, b^[]byte{0x1, 0x2, 0x3, 0x4}[i%4])
	}
	if _, err := conn.Write(ping); err != nil {
		t.Fatalf("ping write: %v", err)
	}
	op, _ := wsReadServerFrame(t, reader)
	if op != wsOpPong {
		t.Fatalf("opcode = %d, want pong", op)
	}
	closeFrame := []byte{0x80 | wsOpClose, 0x80 | 2, 0x1, 0x2, 0x3, 0x4, 0x03 ^ 0x1, 0xE8 ^ 0x2}
	if _, err := conn.Write(closeFrame); err != nil {
		t.Fatalf("close write: %v", err)
	}
	op, _ = wsReadServerFrame(t, reader)
	if op != wsOpClose {
		t.Fatalf("opcode = %d, want close echo", op)
	}
}

func TestUpgradeRejectsUnmaskedClientFrames(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := Upgrade(writer, request, "")
		if err != nil {
			http.Error(writer, "upgrade", http.StatusBadRequest)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err == nil {
			_ = conn.WriteText([]byte(`{"type":"unexpected"}`))
		}
	}))
	defer server.Close()

	conn, _ := wsTestClient(t, server.URL, "")
	defer conn.Close()
	if _, err := conn.Write([]byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}); err != nil {
		t.Fatalf("unmasked write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	// The server must terminate the connection on unmasked client frames. A
	// close echo may precede the TCP close (io.ReadAll then reports a clean
	// EOF); either way no data message may follow.
	drained, err := io.ReadAll(conn)
	if err == nil && len(drained) > 4 {
		t.Fatalf("expected connection termination, drained %d bytes", len(drained))
	}
}
