package chat

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestMemoryReplyRepair(t *testing.T) { exerciseReplyRepair(t, NewMemoryStore()) }
func exerciseReplyRepair(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	thread, err := store.CreateThread(ctx, Thread{Namespace: "repair-test", Mode: ModePersona, ProfileName: "auditor", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := store.AppendMessages(ctx, thread.Namespace, thread.ID, []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "legacy diagnostic", Metadata: json.RawMessage(`{"backend":"openClaw","turnId":"turn","runName":"run"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err = store.ListMessages(ctx, thread.Namespace, thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := records[1]
	if _, err = store.RepairAssistantReply(ctx, "another-namespace", old, "answer", "openclaw.payloads/v1"); !errors.Is(err, ErrRequestConflict) {
		t.Fatal("cross-namespace repair accepted", err)
	}
	if _, err = store.RepairAssistantReply(ctx, thread.Namespace, records[0], "changed user", "openclaw.payloads/v1"); !errors.Is(err, ErrInvalid) {
		t.Fatal("user rewrite accepted", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := store.RepairAssistantReply(ctx, thread.Namespace, old, "saved answer", "openclaw.payloads/v1")
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrRequestConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
	if _, err = store.RepairAssistantReply(ctx, thread.Namespace, old, "stale overwrite", "openclaw.payloads/v1"); !errors.Is(err, ErrRequestConflict) {
		t.Fatal("stale repair accepted", err)
	}
	saved, err := store.ListMessages(ctx, thread.Namespace, thread.ID)
	if err != nil || len(saved) != 2 || saved[0].Content != "hello" || saved[1].Content != "saved answer" || saved[1].ID != old.ID || saved[1].Sequence != old.Sequence || !saved[1].CreatedAt.Equal(old.CreatedAt) {
		t.Fatal("durable identity/content mismatch", err)
	}
}
