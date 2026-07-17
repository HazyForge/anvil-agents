package agentfeedback

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscordAdapterWaitsForReplyToQuestion(t *testing.T) {
	t.Parallel()

	var polls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bot test-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/channels/c123/messages":
			var payload discordCreateMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Content, "Should I restart the controller?") {
				t.Fatalf("posted content = %q, want question", payload.Content)
			}
			if len(payload.AllowedMentions.Parse) != 0 {
				t.Fatalf("allowed mentions parse = %#v, want empty", payload.AllowedMentions.Parse)
			}
			_, _ = w.Write([]byte(`{"id":"100","channel_id":"c123","content":"question","author":{"id":"bot","bot":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/channels/c123/messages":
			if got, want := r.URL.Query().Get("after"), "100"; got != want {
				t.Fatalf("after = %q, want %q", got, want)
			}
			if atomic.AddInt32(&polls, 1) == 1 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"id":"102","channel_id":"c123","content":"ignore non reply","author":{"id":"u1","username":"Austin","bot":false}},
				{"id":"101","channel_id":"c123","content":"yes, restart it","author":{"id":"u1","username":"Austin","bot":false},"message_reference":{"message_id":"100","channel_id":"c123"}}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := NewDiscordAdapter(DiscordConfig{
		BotToken:   "test-token",
		ChannelID:  "c123",
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := adapter.Ask(context.Background(), Question{
		Prompt:       "Should I restart the controller?",
		PollInterval: time.Millisecond,
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Text, "yes, restart it"; got != want {
		t.Fatalf("response text = %q, want %q", got, want)
	}
	if got, want := response.AuthorID, "u1"; got != want {
		t.Fatalf("author id = %q, want %q", got, want)
	}
}

func TestDiscordAdapterCanAcceptAnyMessageAfterQuestion(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"100","channel_id":"c123","content":"question","author":{"id":"bot","bot":true}}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"101","channel_id":"c123","content":"continue","author":{"id":"u1","username":"Austin","bot":false}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := NewDiscordAdapter(DiscordConfig{
		BotToken:   "test-token",
		ChannelID:  "c123",
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := adapter.Ask(context.Background(), Question{
		Prompt:         "Continue?",
		PollInterval:   time.Millisecond,
		Timeout:        time.Second,
		AcceptAnyAfter: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := response.Text, "continue"; got != want {
		t.Fatalf("response text = %q, want %q", got, want)
	}
}
