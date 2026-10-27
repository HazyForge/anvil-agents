package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
)

type redactingCloud struct {
	hostBackend
	marker string
}

func (b *redactingCloud) List(context.Context) ([]onechat.Session, error) {
	return nil, fmt.Errorf("inventory rejected %s", b.marker)
}

func (b *redactingCloud) Redact(text string) string {
	return strings.ReplaceAll(text, b.marker, "[REDACTED]")
}

func TestTurnFinishedPreservesRedactedCloudError(t *testing.T) {
	f := newHostFixture(t, false, "unused")
	marker := "FAKE-CLOUD-TOKEN-MARKER"
	f.manager.Cloud = &redactingCloud{marker: marker}
	c := dialProtocol(t, f)
	bodyAs[map[string]any](t, c.hello(t))
	turn := bodyAs[struct{ TurnID string }](t, c.request(t, "turn.start", map[string]string{"text": "read current work", "requestId": uuid.NewString()}))
	c.waitIdle(t)
	found := false
	for _, event := range c.seen {
		if strings.Contains(string(event.Body), marker) {
			t.Fatalf("event %s exposed the cloud credential marker", event.Event)
		}
		if event.Event == "turn.finished" {
			found = true
			body := bodyAs[struct{ Error string }](t, event)
			if body.Error != "inventory rejected [REDACTED]" {
				t.Fatalf("finished error = %q, want the sanitized inventory failure", body.Error)
			}
		}
	}
	if !found {
		t.Fatal("inventory failure omitted turn.finished")
	}
	trace := bodyAs[struct{ Trace onechat.TurnTrace }](t, c.request(t, "trace.get", map[string]string{"turnId": turn.TurnID})).Trace
	if trace.Error != "inventory rejected [REDACTED]" {
		t.Fatalf("saved trace error = %q", trace.Error)
	}
}

type refreshingCloud struct {
	hostBackend
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (b *refreshingCloud) List(ctx context.Context) ([]onechat.Session, error) {
	if b.calls.Add(1) == 1 {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []onechat.Session{}, nil
}

func TestTurnAcceptedDuringRefreshAndCancellationWhileWaiting(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelWaiting), func(t *testing.T) {
			f := newHostFixture(t, false, "work completed after inventory refresh")
			cloud := &refreshingCloud{entered: make(chan struct{}), release: make(chan struct{})}
			f.manager.Cloud = cloud
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(cloud.release) }) }
			defer release()
			refresher, actor := dialProtocol(t, f), dialProtocol(t, f)
			bodyAs[map[string]any](t, refresher.hello(t))
			bodyAs[map[string]any](t, actor.hello(t))
			refresher.write(t, []byte(`{"type":"request","id":"refresh","op":"sessions.list","body":{"refresh":true}}`))
			select {
			case <-cloud.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("inventory refresh did not reach the blocked backend")
			}
			turn := bodyAs[struct{ TurnID string }](t, actor.request(t, "turn.start", map[string]string{"text": "work after refresh", "requestId": uuid.NewString()}))
			status := bodyAs[struct {
				State string
				Turn  struct{ TurnID string }
			}](t, actor.request(t, "status", map[string]any{}))
			if status.State != "busy" || status.Turn.TurnID != turn.TurnID || f.decider.calls.Load() != 0 {
				t.Fatalf("turn did not wait for inventory refresh: status=%+v decider calls=%d", status, f.decider.calls.Load())
			}
			if cancelWaiting {
				bodyAs[map[string]any](t, actor.request(t, "turn.cancel", map[string]string{"turnId": turn.TurnID}))
				actor.waitIdle(t)
				if f.decider.calls.Load() != 0 {
					t.Fatal("turn cancelled while waiting still called the decider")
				}
			}
			release()
			for {
				response := refresher.read(t)
				if response.Type == "response" {
					if response.ID != "refresh" || !response.OK {
						t.Fatalf("inventory refresh response = %+v", response)
					}
					break
				}
			}
			actor.waitIdle(t)
			if !cancelWaiting && f.decider.calls.Load() != 1 {
				t.Fatalf("accepted turn called the decider %d times after refresh", f.decider.calls.Load())
			}
			want := []string{"status", "turn.started", "turn.inventory", "turn.decided", "action.started", "action.finished", "turn.answered", "turn.finished", "sessions.changed", "status"}
			if cancelWaiting {
				want = []string{"status", "turn.started", "turn.finished", "sessions.changed", "status"}
			}
			var names []string
			seq := 0
			for _, event := range actor.seen {
				names = append(names, event.Event)
				if event.TurnID != "" {
					seq++
					if event.TurnID != turn.TurnID || event.Seq != seq {
						t.Fatalf("refresh changed turn event identity or sequence: %+v", event)
					}
				}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("events after waiting for refresh = %v, want %v", names, want)
			}
			var finished []protocolFrame
			for _, event := range actor.seen {
				if event.Event == "turn.finished" {
					finished = append(finished, event)
				}
			}
			if len(finished) != 1 {
				t.Fatalf("accepted turn emitted %d finished events", len(finished))
			}
			failure := bodyAs[struct{ Error string }](t, finished[0]).Error
			if cancelWaiting && !strings.Contains(failure, "canceled") || !cancelWaiting && failure != "" {
				t.Fatalf("finished error = %q, cancel=%t", failure, cancelWaiting)
			}
		})
	}
}

func TestHistoryListReturnsNewestValidFilesDespiteCorruptTrace(t *testing.T) {
	dir := t.TempDir()
	tracesDir := filepath.Join(dir, "traces")
	if err := os.Mkdir(tracesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	writeTrace := func(name string, raw []byte, modified time.Time) {
		t.Helper()
		path := filepath.Join(tracesDir, name+".json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	// Candidate selection uses file modification time. The final ordering uses
	// the saved turn timestamp, which deliberately differs from that order.
	for i, at := range []time.Time{now.Add(5 * time.Hour), now.Add(time.Hour), now.Add(-time.Hour)} {
		raw, err := json.Marshal(onechat.TurnTrace{ID: ids[i], At: at, Message: fmt.Sprintf("message-%d", i), Answer: "saved answer", Actions: []onechat.ActionResult{{Reply: "result"}}})
		if err != nil {
			t.Fatal(err)
		}
		writeTrace(ids[i], raw, now.Add(time.Duration(i-3)*time.Hour))
	}
	writeTrace(uuid.NewString(), []byte(`{"invalid":`), now.Add(time.Hour))
	writeTrace("not-a-uuid", []byte(`{"id":"ignored","at":"2099-01-01T00:00:00Z"}`), now.Add(2*time.Hour))
	manager, err := onechat.NewManager(onechat.Config{StateDir: dir}, &hostDecider{}, &hostBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{host: &Host{dir: dir, manager: manager}}
	result, failure := c.handle(request{Op: "history.list", Body: json.RawMessage(`{"limit":2}`)})
	if failure != nil {
		t.Fatalf("history failed on a corrupt neighbor: %+v", failure)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var history struct{ Turns []turnSummary }
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Turns) != 2 || history.Turns[0].TurnID != ids[1] || history.Turns[1].TurnID != ids[2] {
		t.Fatalf("newest valid file summaries in turn-time order = %+v", history.Turns)
	}
	if history.Turns[0].Message != "message-1" || history.Turns[1].ActionCount != 1 || history.Turns[0].Answer != "saved answer" {
		t.Fatalf("history lost trace summary fields: %+v", history.Turns)
	}
}
