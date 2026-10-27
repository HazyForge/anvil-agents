package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
)

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
