package onechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const decideSecret = "private-decision-marker"

type decideCloud struct {
	fakeBackend
	failure error
}

func (c *decideCloud) List(context.Context) ([]Session, error) { return nil, c.failure }
func (c *decideCloud) Redact(text string) string {
	return strings.ReplaceAll(text, decideSecret, "[REDACTED]")
}

func TestOneChatDecideTurnDoesNotExecuteOrPersist(t *testing.T) {
	for _, mode := range []string{"send", "create", "validation fallback", "decider failure", "inventory failure"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig(t)
			state := State{Sessions: []Session{{ID: "local-session", Location: "local", SetupID: "local", Harness: "claudeCode", State: "idle", Workdir: config.Local.WorkdirRoots[0]}}}
			at := time.Now().UTC().Add(-time.Hour)
			for i := 0; i < 20; i++ {
				state.History = append(state.History, Message{Role: "user", Text: fmt.Sprintf("message %d", i), At: at.Add(time.Duration(i) * time.Minute)})
			}
			for i := 0; i < 8; i++ {
				state.Sessions[0].Transcript = append(state.Sessions[0].Transcript, Message{Role: "assistant", Text: strings.Repeat("x", 650), At: at.Add(time.Duration(i) * time.Minute)})
			}
			path := filepath.Join(config.StateDir, "onechat.json")
			if err := writeJSON(path, state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			local, cloud := &fakeBackend{}, &decideCloud{}
			if mode == "inventory failure" {
				cloud.failure = errors.New("cannot list " + decideSecret)
			}
			decider := &fakeDecider{decide: func(in DecideInput) (Decision, error) {
				if in.Message != "continue [REDACTED]" || len(in.History) != 10 || in.History[0].Text != "message 10" || in.History[9].Text != "message 19" {
					t.Fatalf("dry input did not use the production history or redaction: %+v", in)
				}
				if len(in.Inventory) != 1 || len(in.Inventory[0].Transcript) != 6 || len([]rune(in.Inventory[0].Transcript[0].Text)) != 600 {
					t.Fatal("dry input did not use the production inventory window")
				}
				action := Action{Type: "send", SessionID: "local-session", Message: "next " + decideSecret}
				switch mode {
				case "create":
					action = Action{Type: "create", SetupID: "local", Title: "new work", Workdir: config.Local.WorkdirRoots[0], Message: "start " + decideSecret}
				case "validation fallback":
					action.SessionID = "unknown-session"
				}
				decision := Decision{Intent: "amend_running", ReasoningSummary: "Continue " + decideSecret, Confidence: 1, Actions: []Action{action}}
				if mode == "decider failure" {
					return decision, errors.New("model failed " + decideSecret)
				}
				return decision, nil
			}, compose: func(ComposeInput) (string, error) {
				t.Fatal("DecideTurn composed an answer")
				return "", nil
			}}
			manager, err := NewManager(config, decider, local, cloud)
			if err != nil {
				t.Fatal(err)
			}
			trace, err := manager.DecideTurn(context.Background(), "continue "+decideSecret)
			if (err != nil) != (mode == "inventory failure") {
				t.Fatalf("DecideTurn error = %v", err)
			}
			if local.creates != 0 || local.sends != 0 || len(trace.Actions) != 0 || trace.TracePath != "" {
				t.Fatalf("dry turn executed or published a trace: %+v", trace)
			}
			if mode == "inventory failure" {
				if len(decider.seen) != 0 || trace.Error != "cannot list [REDACTED]" || err.Error() != trace.Error {
					t.Fatal("inventory failure called the model or lost redaction")
				}
			} else {
				if trace.Totals.ManagerCalls != 1 || trace.Totals.CostUSD == nil || *trace.Totals.CostUSD != 0.02 || trace.DeciderUsage.InputTokens == nil || *trace.DeciderUsage.InputTokens != 11 {
					t.Fatal("dry turn lost decision usage")
				}
				fallback := mode == "validation fallback" || mode == "decider failure"
				if (len(trace.ValidationErrors) != 0) != fallback || (trace.Decision.Actions[0].Type == "ask_user") != fallback || trace.RawDecision == nil {
					t.Fatalf("wrong effective decision: %+v", trace)
				}
			}
			encoded, err := json.Marshal(trace)
			if err != nil || strings.Contains(string(encoded), decideSecret) {
				t.Fatalf("dry trace was not sanitized: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) || !reflect.DeepEqual(manager.History(0), state.History) {
				t.Fatal("dry turn changed state or history")
			}
			entries, err := os.ReadDir(config.StateDir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "onechat.json" {
				t.Fatal("dry turn created files")
			}
		})
	}
}

func TestOneChatDecideTurnSharesTurnLock(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	decider := &fakeDecider{decide: func(DecideInput) (Decision, error) {
		close(entered)
		<-release
		return Decision{ReasoningSummary: "Greeting.", Confidence: 1, Actions: []Action{{Type: "answer", Text: "Hello"}}}, nil
	}}
	manager, err := NewManager(testConfig(t), decider, &fakeBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.DecideTurn(context.Background(), "hello")
		done <- err
	}()
	defer close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("dry turn did not reach the decider")
	}
	for _, turn := range []func(context.Context, string) (TurnTrace, error){manager.Turn, manager.DecideTurn} {
		if _, err := turn(context.Background(), "concurrent"); err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("concurrent turn was allowed: %v", err)
		}
	}
	// The deferred release lets the dry turn finish without any execution path.
	// Wait in cleanup so the goroutine cannot outlive its temporary state.
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("dry turn did not finish")
		}
	})
}
