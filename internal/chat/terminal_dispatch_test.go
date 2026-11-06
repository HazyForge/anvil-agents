package chat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestTerminalDispatchReceiptFencesUnknownAndStaleAttempts(t *testing.T) {
	t.Run("memory", func(t *testing.T) { exerciseTerminalDispatchReceipt(t, NewMemoryStore()) })
	if url := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL"); url != "" {
		t.Run("PostgreSQL", func(t *testing.T) {
			store, err := OpenPostgresStore(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			exerciseTerminalDispatchReceipt(t, store)
		})
	}
}
func exerciseTerminalDispatchReceipt(t *testing.T, s Store) {
	ctx := context.Background()
	thread, err := s.CreateThread(ctx, Thread{Namespace: "dispatch-proof", ProfileName: "inert-" + uuid.NewString(), Mode: ModePersona, CreatedBy: "inert-user"})
	if err != nil {
		t.Fatal(err)
	}
	turn, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "inert-dispatch"}, Message{Content: "inert task"})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimDispatch(ctx, turn, "missing-owner"); err != nil || !claimed {
		t.Fatalf("claim %v %v", claimed, err)
	}
	receipt := TerminalRunReceipt{Namespace: turn.Namespace, RunName: turn.RunName, RunUID: "real-receipt-uid", TurnID: turn.ID, ThreadID: turn.ThreadID, SourceKind: "ChatThread", SourceName: turn.ThreadID, SourceNamespace: turn.Namespace, Phase: "Succeeded"}
	for _, field := range []string{"phase", "namespace", "name", "turn", "thread", "kind", "source", "sourceNamespace", "attempt"} {
		t.Run(field, func(t *testing.T) {
			bad := receipt
			expected := turn
			switch field {
			case "phase":
				bad.Phase = "Running"
			case "namespace":
				bad.Namespace = "other"
			case "name":
				bad.RunName = "other"
			case "turn":
				bad.TurnID = "other"
			case "thread":
				bad.ThreadID = "other"
			case "kind":
				bad.SourceKind = "Other"
			case "source":
				bad.SourceName = "other"
			case "sourceNamespace":
				bad.SourceNamespace = "other"
			case "attempt":
				expected.RetryCount++
			}
			if _, err := s.ResolveTerminalDispatch(ctx, expected, bad); err == nil {
				t.Fatal("unverified evidence resolved ownership")
			}
			turns, _ := s.ListTurns(ctx, turn.Namespace, turn.ThreadID)
			if !Active(turns[0]) || turns[0].Dispatch.State != "creating" {
				t.Fatal("unverified evidence released fence")
			}
		})
	}
	// Cancellation/completion without observed terminal evidence stays blocked.
	terminal := turn
	terminal.Status = "cancelled"
	if err = s.CompleteTurn(ctx, terminal, Message{Role: RoleSystem, Content: "premature release"}); !errors.Is(err, ErrDispatchPending) {
		t.Fatalf("unknown outcome released resources: %v", err)
	}
	if err = s.RecordRun(ctx, turn, "expected-uid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTerminalDispatch(ctx, turn, receipt); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("replacement UID resolved original: %v", err)
	}
	receipt.RunUID = "expected-uid"
	resolved, err := s.ResolveTerminalDispatch(ctx, turn, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Dispatch.State != "terminal" || resolved.RunUID != "expected-uid" {
		t.Fatal("terminal receipt not durably bound")
	}
	resolved.Status = "succeeded"
	if err = s.CompleteTurn(ctx, resolved, Message{Role: RoleAssistant, Content: "one verified reply"}); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishDispatch(ctx, turn, "missing-owner", "created"); err != nil {
		t.Fatal(err)
	}
	turns, _ := s.ListTurns(ctx, turn.Namespace, turn.ThreadID)
	if turns[0].Dispatch.State != "terminal" || turns[0].Status != "succeeded" {
		t.Fatal("late owner downgraded completion")
	}
	// Private codec preserves the terminal proof across a store/process restart.
	encoded, err := encodeTurn(turns[0])
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTurn(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Dispatch.State != "terminal" {
		t.Fatal("terminal proof lost in outbox codec")
	}
	var payload map[string]any
	if err = json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
}
