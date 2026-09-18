package runapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

// Slice-3 long-lived WebSocket token subscription tests. Open standing stream
// clients receive live TokenEvents (with the run name stamped by
// standingRunSink) during an InProcess turn, not only the post-turn snapshot.
// Every test uses FakeBackend: no model calls, no harness subprocess.

func wsReadFrameDeadline(reader interface {
	Read([]byte) (int, error)
}, conn net.Conn, timeout time.Duration) (byte, []byte, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, nil, err
	}
	defer conn.SetReadDeadline(time.Time{})
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return header[0] & 0x0F, payload, nil
}

func TestChatThreadStreamWebSocketLiveTokensDuringTurn(t *testing.T) {
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	enableStandingLive(t, server, standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"harnessProfileName":"standing-harness","mode":"persona"}`)
	live := httptest.NewServer(server.routes())
	defer live.Close()

	conn, reader, _ := wsStreamDial(t, live.URL, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", "valid", "")
	defer conn.Close()

	op, snapshotRaw := wsReadStreamFrame(t, reader)
	if op != 0x1 {
		t.Fatalf("opcode = %d, want text", op)
	}
	var snapshot struct {
		Type     string                  `json:"type"`
		Thread   chat.Thread             `json:"thread"`
		Standing *chatStreamStandingView `json:"standing"`
	}
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		t.Fatalf("snapshot: %v in %s", err, snapshotRaw)
	}
	if snapshot.Type != "snapshot" || snapshot.Thread.ID != thread.ID || snapshot.Standing == nil {
		t.Fatalf("snapshot = %s, want the resumed standing session", snapshotRaw)
	}

	// The snapshot read proves the server subscribed before the upgrade
	// completed, so a turn queued now streams its tokens into this stream
	// deterministically: no race between publish and subscribe.
	accepted, err := server.queueChatTurn(context.Background(), "agents", thread.ID, AppendChatMessageRequest{Content: "hello live tokens"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("turn status = %q, want succeeded", accepted.Turn.Status)
	}

	var tokens []chatStreamTokenFrame
	var terminal chatStreamTerminal
	for {
		op, raw, err := wsReadFrameDeadline(reader, conn, 5*time.Second)
		if err != nil {
			t.Fatalf("stream frame: %v after %d token frames", err, len(tokens))
		}
		if op != 0x1 {
			continue
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("frame: %v in %s", err, raw)
		}
		switch envelope.Type {
		case "token":
			var frame chatStreamTokenFrame
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatalf("token frame: %v in %s", err, raw)
			}
			tokens = append(tokens, frame)
		case "terminal":
			if err := json.Unmarshal(raw, &terminal); err != nil {
				t.Fatalf("terminal: %v in %s", err, raw)
			}
			goto drained
		default:
			t.Fatalf("frame type = %q in %s, want token or terminal", envelope.Type, raw)
		}
	}
drained:
	if len(tokens) == 0 {
		t.Fatal("open stream received no token frames during the turn")
	}
	for i, frame := range tokens {
		if frame.ThreadID != thread.ID || frame.TurnID != accepted.Turn.ID || frame.RunName != accepted.Turn.RunName {
			t.Fatalf("token %d = %+v, want the full turn identity stamped", i, frame)
		}
		if frame.Seq != i {
			t.Fatalf("token %d seq = %d, want ordered delivery", i, frame.Seq)
		}
		if i < len(tokens)-1 && frame.Done {
			t.Fatalf("token %d carries Done before the final frame", i)
		}
	}
	if !tokens[len(tokens)-1].Done {
		t.Fatal("final token frame must carry the Done marker")
	}
	if terminal.Type != "terminal" || terminal.Code != "standing_ready" {
		t.Fatalf("terminal = %+v, want standing_ready after the live tokens", terminal)
	}
	// The server closes after the terminal frame: no half-open subscription.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rest, err := io.ReadAll(conn)
	if err == nil && len(rest) > 8 {
		t.Fatalf("expected close after terminal, drained %d bytes", len(rest))
	}
}

func TestChatThreadStreamWebSocketStandingStaysOpenWhenIdle(t *testing.T) {
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	enableStandingLive(t, server, standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"harnessProfileName":"standing-harness","mode":"persona"}`)
	live := httptest.NewServer(server.routes())
	defer live.Close()

	conn, reader, _ := wsStreamDial(t, live.URL, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", "valid", "")
	defer conn.Close()

	op, snapshotRaw := wsReadStreamFrame(t, reader)
	if op != 0x1 || !strings.Contains(string(snapshotRaw), thread.ID) {
		t.Fatalf("snapshot = %s, want the thread snapshot first", snapshotRaw)
	}
	// Idle standing streams stay open past the snapshot: no immediate
	// terminal while no turn streams. A client that only wants the snapshot
	// uses SSE or closes; a live client waits for the next turn's tokens.
	if _, _, err := wsReadFrameDeadline(reader, conn, 400*time.Millisecond); err == nil {
		t.Fatal("idle standing stream closed immediately; want the long-lived subscription")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("idle read error = %v, want a read timeout proving the stream stays open", err)
	}
}

func TestChatThreadStreamWebSocketJobPlaneSendsNoTokens(t *testing.T) {
	server := chatTestServer(t, true)
	server.SetStandingBackend(standing.NewFakeBackend())
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
		Type     string                  `json:"type"`
		Standing *chatStreamStandingView `json:"standing"`
	}
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Standing != nil {
		t.Fatalf("snapshot standing = %+v, want nil on the Job plane", snapshot.Standing)
	}
	op, terminalRaw := wsReadStreamFrame(t, reader)
	if op != 0x1 {
		t.Fatalf("opcode = %d, want text", op)
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(terminalRaw, &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Type != "terminal" || terminal.Code != "job_plane" {
		t.Fatalf("terminal = %s, want immediate job_plane with no token frames", terminalRaw)
	}
}

func TestChatThreadStreamSSEStaysSnapshotTerminal(t *testing.T) {
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	enableStandingLive(t, server, standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"harnessProfileName":"standing-harness","mode":"persona"}`)
	appendStreamMessage(t, server, thread.ID, "hello standing stream")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Count(body, "event: snapshot") != 1 || strings.Count(body, "event: terminal") != 1 {
		t.Fatalf("SSE body must stay snapshot + terminal with no token events:\n%s", body)
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(sseStreamEvent(t, body, "terminal"), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Code != "standing_ready" {
		t.Fatalf("terminal = %+v, want standing_ready", terminal)
	}
}
