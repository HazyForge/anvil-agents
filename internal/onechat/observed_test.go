package onechat

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOneChatObservedTurn(t *testing.T) {
	const token = "fake-sensitive-cloud-bearer"
	for _, mode := range []string{"send", "fallback", "inventory failure"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig(t)
			config.Cloud = CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}, TokenCommand: []string{"fake-token"}}
			cloud, err := NewCloudBackend(context.Background(), config.Cloud, time.Second, &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
				if mode == "inventory failure" {
					return response(503, map[string]any{"error": map[string]string{"code": "unavailable", "message": "failed " + token}}), nil
				}
				return response(200, map[string]any{"items": []any{}}), nil
			})}, func(context.Context) (string, error) { return token, nil })
			if err != nil {
				t.Fatal(err)
			}
			decider := &fakeDecider{decide: func(DecideInput) (Decision, error) {
				id := "local-session"
				if mode == "fallback" {
					id = "unknown"
				}
				return Decision{ReasoningSummary: "continue " + token, Confidence: 1, Actions: []Action{{Type: "send", SessionID: id, Message: "work " + token}}}, nil
			}, compose: func(ComposeInput) (string, error) { return "answer " + token, nil }}
			local := &observedBackend{}
			manager, err := NewManager(config, decider, local, cloud)
			if err != nil {
				t.Fatal(err)
			}
			manager.state.Sessions = []Session{{ID: "local-session", Location: "local", SetupID: "local", Harness: "claudeCode", Title: "work " + token, State: "idle", Workdir: config.Local.WorkdirRoots[0]}}
			var events []Event
			id := uuid.New()
			trace, err := manager.TurnObserved(WithTurnID(context.Background(), id), "message "+token, func(event Event) {
				events = append(events, event)
			})
			if (err != nil) != (mode == "inventory failure") {
				t.Fatalf("turn error = %v", err)
			}
			if trace.ID != id.String() || trace.Version != "onechat.turn/v1" {
				t.Fatalf("trace identity changed: %s %s", trace.ID, trace.Version)
			}
			want := []string{"turn.inventory", "turn.decided", "action.started", "action.finished", "turn.answered"}
			if mode == "inventory failure" {
				want = []string{"turn.inventory", "turn.answered"}
			}
			var names []string
			previous := trace.At
			for _, event := range events {
				names = append(names, event.Name)
				if event.At.Before(previous) {
					t.Fatal("event times went backwards")
				}
				previous = event.At
				body, err := json.Marshal(event.Body)
				if err != nil || strings.Contains(string(body), token) {
					t.Fatalf("%s body was not sanitized: %v", event.Name, err)
				}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("events = %v, want %v", names, want)
			}
			if mode == "inventory failure" {
				body := events[0].Body.(map[string]any)
				if !strings.Contains(body["error"].(string), "[REDACTED]") || local.sends != 0 {
					t.Fatal("inventory failure lost its sanitized error or sent work")
				}
			} else {
				var decision struct {
					Decision         Decision `json:"decision"`
					ValidationErrors []string `json:"validationErrors"`
				}
				body, _ := json.Marshal(events[1].Body)
				if err := json.Unmarshal(body, &decision); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(decision.Decision, trace.Decision) || !reflect.DeepEqual(decision.ValidationErrors, trace.ValidationErrors) {
					t.Fatal("observed decision differs from saved validated decision")
				}
				if mode == "fallback" && (decision.Decision.Actions[0].Type != "ask_user" || local.sends != 0) {
					t.Fatal("fallback event announced unvalidated work")
				}
				var finished struct {
					Index  int          `json:"index"`
					Result ActionResult `json:"result"`
				}
				body, _ = json.Marshal(events[3].Body)
				if err := json.Unmarshal(body, &finished); err != nil || finished.Index != 0 || !reflect.DeepEqual(finished.Result, trace.Actions[0]) {
					t.Fatalf("observed action differs from trace: %v", err)
				}
				if mode == "send" && (finished.Result.Usage.InputTokens == nil || *finished.Result.Usage.InputTokens != observedInputTokens) {
					t.Fatal("action event lost integer usage precision")
				}
			}
			if events[len(events)-1].Body.(map[string]any)["answer"] != trace.Answer {
				t.Fatal("answer event differs from final answer")
			}
			assertSavedObservedTrace(t, trace)
			if mode == "send" {
				// The original API still writes the same complete trace format.
				ordinary, err := manager.Turn(context.Background(), "ordinary turn")
				if err != nil || ordinary.Version != "onechat.turn/v1" || ordinary.ID == trace.ID {
					t.Fatalf("ordinary Turn changed: %v", err)
				}
				assertSavedObservedTrace(t, ordinary)
			}
		})
	}
}

// This integer cannot round-trip through float64; events must retain the typed
// trace's exact reported usage rather than silently rounding it.
const observedInputTokens int64 = 9007199254740993

type observedBackend struct{ fakeBackend }

func (b *observedBackend) Send(ctx context.Context, setup Setup, session Session, text string) (ActionResult, error) {
	result, err := b.fakeBackend.Send(ctx, setup, session, text)
	count := observedInputTokens
	result.Usage.InputTokens = &count
	return result, err
}

func assertSavedObservedTrace(t *testing.T, trace TurnTrace) {
	t.Helper()
	raw, err := os.ReadFile(trace.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	returned, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var saved, actual any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(returned, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, actual) {
		t.Fatal("saved trace differs from returned turn")
	}
}

type snapshotBackend struct {
	fakeBackend
	entered chan struct{}
	release chan struct{}
}

func (b *snapshotBackend) Send(ctx context.Context, setup Setup, session Session, text string) (ActionResult, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.fakeBackend.Send(ctx, setup, session, text)
	case <-ctx.Done():
		return ActionResult{}, ctx.Err()
	}
}

func TestOneChatSnapshotsRemainAvailableDuringTurn(t *testing.T) {
	config := testConfig(t)
	transcript := make([]Message, 8)
	for i := range transcript {
		transcript[i] = Message{Role: "assistant", Text: strings.Repeat("界", 650)}
	}
	state := State{Sessions: []Session{{ID: "local-session", Location: "local", SetupID: "local", Harness: "claudeCode", State: "idle", Workdir: config.Local.WorkdirRoots[0], Transcript: transcript}}, History: []Message{{Role: "user", Text: "earlier question"}, {Role: "assistant", Text: "earlier answer"}}}
	if err := writeJSON(filepath.Join(config.StateDir, "onechat.json"), state); err != nil {
		t.Fatal(err)
	}
	backend := &snapshotBackend{entered: make(chan struct{}), release: make(chan struct{})}
	decider := &fakeDecider{decide: func(DecideInput) (Decision, error) {
		return Decision{ReasoningSummary: "continue", Confidence: 1, Actions: []Action{{Type: "send", SessionID: "local-session", Message: "new work"}}}, nil
	}, compose: func(ComposeInput) (string, error) { return "new answer", nil }}
	manager, err := NewManager(config, decider, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Inventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, refreshed := manager.LastInventory()
	if refreshed.IsZero() || len(first) != 1 || len(first[0].Transcript) != 8 || len([]rune(first[0].Transcript[0].Text)) != 650 {
		t.Fatal("snapshot lost full owned transcript or refresh time")
	}
	first[0].Title, first[0].Transcript[0].Text = "tampered", "tampered"
	history := manager.History(1)
	if len(history) != 1 || history[0].Text != "earlier answer" {
		t.Fatal("history limit did not select latest messages")
	}
	history[0].Text = "tampered"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := manager.Turn(ctx, "new question")
		done <- err
	}()
	select {
	case <-backend.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("turn never reached blocked backend")
	}
	available := make(chan bool, 1)
	go func() {
		sessions, at := manager.LastInventory()
		messages := manager.History(50)
		available <- len(sessions) == 1 && !at.Before(refreshed) && sessions[0].Title != "tampered" && sessions[0].Transcript[0].Text != "tampered" && len(messages) == 2 && messages[1].Text == "earlier answer"
	}()
	select {
	case valid := <-available:
		if !valid {
			t.Fatal("snapshots alias returned copies or expose unfinished history")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot reads waited for the running turn")
	}
	close(backend.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("released turn did not finish")
	}
	final, at := manager.LastInventory()
	if !at.After(refreshed) || len(final[0].Transcript) != 10 || final[0].Transcript[9].Text != "actual harness reply: new work" {
		t.Fatal("completed execution was not reflected in snapshot")
	}
	if history := manager.History(50); len(history) != 4 || history[3].Text != "new answer" {
		t.Fatal("finished turn did not publish history")
	}
}
