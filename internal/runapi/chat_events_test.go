package runapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/chat"
)

type sseFrame struct {
	Event string
	Data  string
}

// openChatEvents starts a real HTTP server so the handler streams over a
// connection whose context ends when the test cancels it.
func openChatEvents(t *testing.T, server *Server, namespace string) (*bufio.Reader, func()) {
	t.Helper()
	httpServer := httptest.NewServer(server.routes())
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/v1/namespaces/"+namespace+"/chat/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("chat events = %d %v", response.StatusCode, response.Header)
	}
	return bufio.NewReader(response.Body), func() {
		cancel()
		_ = response.Body.Close()
		httpServer.Close()
	}
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) sseFrame {
	t.Helper()
	frames := make(chan sseFrame, 1)
	failures := make(chan error, 1)
	go func() {
		var frame sseFrame
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				failures <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "" && frame.Event != "":
				frames <- frame
				return
			case strings.HasPrefix(line, "event: "):
				frame.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.Data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	select {
	case frame := <-frames:
		return frame
	case err := <-failures:
		t.Fatalf("read chat event: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a chat event")
	}
	return sseFrame{}
}

func TestChatEventsStreamThreadChangesWithoutContent(t *testing.T) {
	server := chatTestServer(t, true)
	reader, closeStream := openChatEvents(t, server, "agents")
	defer closeStream()
	if frame := readSSEFrame(t, reader); frame.Event != "ready" || !strings.Contains(frame.Data, `"namespace":"agents"`) {
		t.Fatalf("first frame = %+v, want ready for agents", frame)
	}

	thread := createStreamThread(t, server, `{"profileName":"grok45","mode":"persona"}`)
	created := readSSEFrame(t, reader)
	var event chatThreadEvent
	if err := json.Unmarshal([]byte(created.Data), &event); err != nil {
		t.Fatal(err)
	}
	if created.Event != "thread" || event.Type != "thread" || event.ThreadID != thread.ID || event.Reason != chatEventReasonCreated {
		t.Fatalf("create frame = %+v %+v, want created notice for %s", created, event, thread.ID)
	}

	appendStreamMessage(t, server, thread.ID, "secret plan: ship on friday")
	sawTurn := false
	for i := 0; i < 4 && !sawTurn; i++ {
		frame := readSSEFrame(t, reader)
		if strings.Contains(frame.Data, "secret plan") || strings.Contains(frame.Data, "content") {
			t.Fatalf("chat event leaked message content: %s", frame.Data)
		}
		if err := json.Unmarshal([]byte(frame.Data), &event); err != nil {
			t.Fatal(err)
		}
		if event.ThreadID != thread.ID {
			t.Fatalf("event for %q, want %q", event.ThreadID, thread.ID)
		}
		sawTurn = event.Reason == chatEventReasonTurn && event.TurnID != ""
	}
	if !sawTurn {
		t.Fatal("appending a message did not announce the queued turn")
	}
}

func TestChatEventsRequireChatReadInNamespace(t *testing.T) {
	disabled := chatTestServer(t, false)
	for _, test := range []struct {
		name   string
		server *Server
		path   string
		want   int
	}{
		{name: "chat disabled", server: disabled, path: "/api/v1/namespaces/agents/chat/events", want: http.StatusNotFound},
		{name: "unbound namespace", server: chatTestServer(t, true), path: "/api/v1/namespaces/other/chat/events", want: http.StatusNotFound},
		{name: "query token", server: chatTestServer(t, true), path: "/api/v1/namespaces/agents/chat/events?access_token=valid", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer valid")
			response := httptest.NewRecorder()
			test.server.routes().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("%s = %d %s, want %d", test.path, response.Code, response.Body.String(), test.want)
			}
		})
	}
}

func TestChatEventHubScopesNamespacesAndMarksOverflow(t *testing.T) {
	hub := newChatEventHub()
	agents, leave := hub.subscribe("agents")
	other, leaveOther := hub.subscribe("other")
	defer leaveOther()

	hub.publish("other", chatThreadEvent{ThreadID: "elsewhere", Reason: chatEventReasonMessage})
	select {
	case event := <-agents.events:
		t.Fatalf("agents subscriber received %+v from another namespace", event)
	default:
	}
	if got := <-other.events; got.ThreadID != "elsewhere" || got.Type != "thread" || got.At.IsZero() {
		t.Fatalf("other subscriber received %+v", got)
	}

	for i := 0; i < chatEventHubBuffer+3; i++ {
		hub.publish("agents", chatThreadEvent{ThreadID: "busy", Reason: chatEventReasonTurn})
	}
	if !agents.dropped.Load() {
		t.Fatal("overflow did not mark the subscriber for resync")
	}
	leave()
	leave()
	if hub.subscriberCount("agents") != 0 {
		t.Fatalf("subscribers after leave = %d", hub.subscriberCount("agents"))
	}
}

func TestChatEventsResyncAfterOverflow(t *testing.T) {
	server := chatTestServer(t, true)
	reader, closeStream := openChatEvents(t, server, "agents")
	defer closeStream()
	readSSEFrame(t, reader)
	for i := 0; i < chatEventHubBuffer*4; i++ {
		server.chatEvents.publish("agents", chatThreadEvent{ThreadID: "busy", Reason: chatEventReasonTurn})
	}
	for i := 0; i < chatEventHubBuffer*4; i++ {
		if frame := readSSEFrame(t, reader); frame.Event == "resync" {
			return
		}
	}
	t.Fatal("stream never sent resync after its buffer overflowed")
}

func TestNotifyingChatStoreSkipsFailedWrites(t *testing.T) {
	hub := newChatEventHub()
	subscriber, leave := hub.subscribe("agents")
	defer leave()
	store := notifyingChatStore{Store: chat.NewMemoryStore(), hub: hub}
	if _, _, err := store.AppendMessages(context.Background(), "agents", "missing", []chat.Message{{Role: chat.RoleUser, Content: "hello"}}); err == nil {
		t.Fatal("append to a missing thread succeeded")
	}
	select {
	case event := <-subscriber.events:
		t.Fatalf("failed write announced %+v", event)
	default:
	}
}
