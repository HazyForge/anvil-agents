package onechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

func TestOneChatCloudCreateAppendReplyAndRefresh(t *testing.T) {
	const firstToken, secondToken = "fake-initial-bearer", "fake-refreshed-bearer"
	refreshes, appends, reads := 0, 0, 0
	requestID := ""
	thread := chat.Thread{ID: uuid.NewString(), Namespace: "agents", ProfileName: "p", Title: "Cloud work", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	turn := chat.Turn{ID: "turn-accepted", ThreadID: thread.ID, RunName: "run-accepted", Status: "running"}
	transport := fakeTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.RawQuery != "" && req.URL.RawQuery != "limit=200" {
			t.Fatalf("unexpected query %s", req.URL.RawQuery)
		}
		if strings.Contains(req.URL.String(), "bearer") {
			t.Fatal("bearer in URL")
		}
		wantToken := firstToken
		if refreshes > 1 {
			wantToken = secondToken
		}
		if req.Header.Get("Authorization") != "Bearer "+wantToken {
			t.Fatal("missing bearer header")
		}
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/threads"):
			var create map[string]any
			if err := json.NewDecoder(req.Body).Decode(&create); err != nil {
				t.Fatal(err)
			}
			_, standing := create["standing"]
			if standing || create["mode"] != "persona" || create["profileName"] != "p" || create["title"] != "Cloud work" {
				t.Fatalf("bad create %+v", create)
			}
			return response(201, thread), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/threads"):
			return response(200, runapi.ChatThreadListResponse{Items: []chat.Thread{thread}}), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/messages"):
			appends++
			var appendBody runapi.AppendChatMessageRequest
			if err := json.NewDecoder(req.Body).Decode(&appendBody); err != nil {
				t.Fatal(err)
			}
			if _, err := uuid.Parse(appendBody.RequestID); err != nil || appendBody.Content != "do cloud work" {
				t.Fatalf("bad append %+v", appendBody)
			}
			if requestID != "" && appendBody.RequestID != requestID {
				t.Fatal("401 retry changed request identity")
			}
			requestID = appendBody.RequestID
			if appends == 1 {
				return response(401, map[string]string{"error": firstToken}), nil
			}
			return response(202, runapi.ChatAppendResponse{Thread: thread, Turn: turn, User: chat.Message{ID: "user-accepted"}}), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, thread.ID):
			reads++
			messages := []chat.Message{{ID: "old", Role: "assistant", Content: "old unrelated reply", Metadata: json.RawMessage(`{"turnId":"old-turn"}`)}}
			if reads > 1 {
				turn.Status = "succeeded"
				messages = append(messages, chat.Message{ID: "assistant-accepted", Role: "assistant", Content: "real reply", Metadata: json.RawMessage(`{"turnId":"turn-accepted","runName":"run-accepted"}`)})
			}
			return response(200, runapi.ChatThreadDetailResponse{Thread: thread, Turns: []chat.Turn{turn}, Messages: messages}), nil
		default:
			t.Fatalf("unexpected HTTP request %s %s", req.Method, req.URL)
			return nil, fmt.Errorf("unexpected request")
		}
	})
	b, err := NewCloudBackend(context.Background(), CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}}, time.Second, &http.Client{Transport: transport}, func(context.Context) (string, error) {
		refreshes++
		if refreshes == 1 {
			return firstToken, nil
		}
		return secondToken, nil
	})
	if err != nil || refreshes != 1 {
		t.Fatalf("token not acquired at start: %v", err)
	}
	b.PollInterval = time.Millisecond
	setup := Setup{ID: "cloud", Location: "cloud", Harness: "codex", Namespace: "agents", Profile: "p"}
	s, receipt, err := b.Create(context.Background(), setup, "Cloud work", "")
	if err != nil || receipt.Cloud.Created == nil || !*receipt.Cloud.Created {
		t.Fatalf("create %v %+v", err, receipt)
	}
	listed, err := b.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ThreadID != thread.ID {
		t.Fatalf("list %v %+v", err, listed)
	}
	result, err := b.Send(context.Background(), setup, s, "do cloud work")
	if err != nil || result.Reply != "real reply" || result.State != "idle" || refreshes != 2 || appends != 2 || reads != 2 || result.Receipt.Cloud.TurnID != "turn-accepted" || result.Receipt.Cloud.RunName != "run-accepted" || strings.Join(result.Receipt.Cloud.MessageIDs, ",") != "user-accepted,assistant-accepted" || result.Receipt.Cloud.RequestID != requestID {
		t.Fatalf("cloud result %+v err=%v", result, err)
	}
	if result.Usage.InputTokens != nil || result.Usage.CostUSD != nil {
		t.Fatal("invented cloud usage")
	}
	if b.Redact(firstToken+secondToken) != "[REDACTED][REDACTED]" {
		t.Fatal("forgot retired token")
	}
}

func TestOneChatCloudFailureAndTimeout(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			transport := fakeTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					return response(202, runapi.ChatAppendResponse{Turn: chat.Turn{ID: "accepted", RunName: "run"}, User: chat.Message{ID: "user"}}), nil
				}
				turn := chat.Turn{ID: "accepted", Status: "running"}
				if failure {
					turn.Status = "failed"
					turn.Error = "actual provider error"
				}
				messages := []chat.Message{{Role: "assistant", Content: "old answer", Metadata: json.RawMessage(`{"turnId":"old"}`)}}
				if failure {
					messages = append(messages, chat.Message{ID: "failure-message", Role: "system", Content: "failed", Metadata: json.RawMessage(`{"turnId":"accepted"}`)})
				}
				return response(200, runapi.ChatThreadDetailResponse{Turns: []chat.Turn{turn}, Messages: messages}), nil
			})
			b, err := NewCloudBackend(context.Background(), CloudConfig{BaseURL: "https://api.invalid"}, 10*time.Millisecond, &http.Client{Transport: transport}, func(context.Context) (string, error) { return "fake-token", nil })
			if err != nil {
				t.Fatal(err)
			}
			b.PollInterval = time.Millisecond
			result, err := b.Send(context.Background(), Setup{Harness: "codex"}, Session{ID: "s", Namespace: "agents", ThreadID: "thread"}, "work")
			if err == nil || result.Reply != "" || result.Receipt.Cloud.TurnID != "accepted" {
				t.Fatalf("lost failure receipt: %+v %v", result, err)
			}
			if failure && (result.State != "failed" || !strings.Contains(result.Error, "actual provider error")) {
				t.Fatal("lost terminal failure")
			}
			if failure && strings.Join(result.Receipt.Cloud.MessageIDs, ",") != "user,failure-message" {
				t.Fatal("lost failure message receipt")
			}
			if !failure && (result.State != "running" || !strings.Contains(result.Error, "deadline")) {
				t.Fatal("timeout pretended remote work stopped")
			}
		})
	}
}

func TestOneChatCloudTokenNeverEntersTrace(t *testing.T) {
	const token = "fake-sensitive-cloud-bearer"
	c := testConfig(t)
	c.Cloud = CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}, TokenCommand: []string{"fake-token"}}
	c.Setups = append(c.Setups, Setup{ID: "cloud", Location: "cloud", Harness: "codex", Namespace: "agents", Profile: "p", Notes: "routing data " + token})
	thread := chat.Thread{ID: "thread", Namespace: "agents", ProfileName: "p", Title: "title " + token}
	sent := false
	b, err := NewCloudBackend(context.Background(), c.Cloud, time.Second, &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("auth token absent from HTTP header")
		}
		if strings.HasSuffix(req.URL.Path, "/threads") {
			return response(200, runapi.ChatThreadListResponse{Items: []chat.Thread{thread}}), nil
		}
		if req.Method == http.MethodPost {
			sent = true
			return response(202, runapi.ChatAppendResponse{Turn: chat.Turn{ID: "turn"}, User: chat.Message{ID: "user"}}), nil
		}
		d := runapi.ChatThreadDetailResponse{Thread: thread, Messages: []chat.Message{{Role: "user", Content: "history " + token}}}
		if sent {
			d.Turns = []chat.Turn{{ID: "turn", Status: "succeeded"}}
			d.Messages = append(d.Messages, chat.Message{ID: "reply", Role: "assistant", Content: "answer " + token, Metadata: json.RawMessage(`{"turnId":"turn"}`)})
		}
		return response(200, d), nil
	})}, func(context.Context) (string, error) { return token, nil })
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDecider{decide: func(in DecideInput) (Decision, error) {
		raw, _ := json.Marshal(in)
		if strings.Contains(string(raw), token) {
			t.Fatal("token reached decider")
		}
		return Decision{ReasoningSummary: "continue", Confidence: 1, Actions: []Action{{Type: "send", SessionID: in.Inventory[0].ID, Message: "work"}}}, nil
	}, compose: func(in ComposeInput) (string, error) {
		raw, _ := json.Marshal(in)
		if strings.Contains(string(raw), token) {
			t.Fatal("token reached compose")
		}
		return "answer " + token, nil
	}}
	m, err := NewManager(c, d, &fakeBackend{}, b)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := m.Turn(context.Background(), "message "+token)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(trace)
	if strings.Contains(string(serialized), token) || !strings.Contains(string(serialized), "[REDACTED]") {
		t.Fatal("returned trace leaked bearer")
	}
	for _, path := range []string{trace.TracePath, filepath.Join(c.StateDir, "onechat.json")} {
		raw, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(raw), token) {
			t.Fatalf("secret persisted in %s: %v", path, err)
		}
	}
}

func TestOneChatCloudTokenCommandAndRejectedRefresh(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "token-command")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' fake-command-token\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	b, err := NewCloudBackend(context.Background(), CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}, TokenCommand: []string{command}}, time.Second, &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") != "Bearer fake-command-token" {
			t.Fatal("command token not used")
		}
		return response(401, map[string]string{"error": "fake-command-token"}), nil
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.List(context.Background())
	if err == nil || calls != 2 || strings.Contains(err.Error(), "fake-command-token") {
		t.Fatalf("unbounded or leaking refresh %d %v", calls, err)
	}
}

func TestOneChatCloudPersonaCreatesDistinctSessionsAndRejectsReuse(t *testing.T) {
	calls := 0
	backend, err := NewCloudBackend(context.Background(), CloudConfig{BaseURL: "https://api.invalid"}, time.Second, &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/threads") {
			t.Fatal("unexpected persona request")
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["standing"]; ok || body["profileName"] != "same-profile" || body["mode"] != "persona" || body["title"] != "new work" {
			t.Fatalf("incorrect persona body: %+v", body)
		}
		code := http.StatusCreated
		if calls == 3 {
			code = http.StatusOK
		}
		return response(code, chat.Thread{ID: fmt.Sprintf("thread-%d", calls), Namespace: "agents", ProfileName: "same-profile", Mode: "persona", Title: "new work"}), nil
	})}, func(context.Context) (string, error) { return "fake-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	setup := Setup{ID: "cloud", Location: "cloud", Harness: "codex", Namespace: "agents", Profile: "same-profile"}
	first, _, err := backend.Create(context.Background(), setup, "new work", "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := backend.Create(context.Background(), setup, "new work", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.ThreadID == second.ThreadID {
		t.Fatal("same-profile sessions shared a thread")
	}
	reused, receipt, err := backend.Create(context.Background(), setup, "new work", "")
	if err == nil || !strings.Contains(err.Error(), "expected 201") || reused.ID != "" || receipt.Cloud == nil || receipt.Cloud.Created == nil || *receipt.Cloud.Created || receipt.Cloud.ThreadID != "thread-3" {
		t.Fatalf("reuse was adopted or receipt lost: %+v %+v %v", reused, receipt, err)
	}
}
