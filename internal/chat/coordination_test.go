package chat

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"sync"
	"testing"
)

func TestMemoryCoordinationJourney(t *testing.T) { exerciseCoordinationJourney(t, NewMemoryStore()) }
func TestPostgresCoordinationJourney(t *testing.T) {
	url := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated PostgreSQL chat fixture")
	}
	store, err := OpenPostgresStore(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseCoordinationJourney(t, store)
	// Reopen through the real JSON payload decoder, not an in-memory object.
	reopened, err := OpenPostgresStore(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	parent := makeCoordinationParent(t, store)
	pending, err := reopened.PendingTurns(context.Background(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pending {
		if p.ID == parent.ID {
			found = p.Coordination != nil && p.Coordination.Status == "waiting"
		}
	}
	if !found {
		t.Fatal("waiting journey lost on store reconnect")
	}
}

func makeCoordinationParent(t *testing.T, s Store) Turn {
	t.Helper()
	ctx := context.Background()
	thread, err := s.CreateThread(ctx, Thread{Namespace: "coordination-tests", ProfileName: "inert-" + uuid.NewString(), CreatedBy: "inert-user", Mode: ModePersona})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "inert-original"}, Message{Content: "original user task"})
	if err != nil {
		t.Fatal(err)
	}
	parent.Status = "succeeded"
	parent.Coordination = &Coordination{Status: "waiting"}
	if err = s.CompleteTurn(ctx, parent, Message{Role: RoleAssistant, Content: "peer request acknowledgement"}); err != nil {
		t.Fatal(err)
	}
	return parent
}
func synthesisCandidate(p Turn) Turn {
	return Turn{Namespace: p.Namespace, ThreadID: p.ThreadID, ProfileName: p.ProfileName, RequestID: SynthesisRequestID(p.ID), RunName: "inert-synthesis", ParentTurnID: p.ID, ParentThreadID: p.ThreadID, Resumption: true, Deferred: true}
}

func exerciseCoordinationJourney(t *testing.T, s Store) {
	ctx := context.Background()
	t.Run("recovery pages reach deferred children and revisit moved turns", func(t *testing.T) {
		var parents, children []Turn
		for i := 0; i < 3; i++ {
			parent := makeCoordinationParent(t, s)
			parents = append(parents, parent)
			thread, err := s.CreateThread(ctx, Thread{Namespace: parent.Namespace, ProfileName: "peer-" + uuid.NewString(), CreatedBy: "inert-user", Mode: ModePersona})
			if err != nil {
				t.Fatal(err)
			}
			child, _, _, err := s.QueueTurn(ctx, Turn{Namespace: parent.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "peer-run", ParentTurnID: parent.ID, ParentThreadID: parent.ThreadID, Deferred: true}, Message{Content: "peer request"})
			if err != nil {
				t.Fatal(err)
			}
			children = append(children, child)
		}
		t.Cleanup(func() {
			for i, parent := range parents {
				if _, err := s.CancelCoordination(ctx, parent.Namespace, parent.ThreadID, parent.ID); err != nil {
					t.Errorf("cancel parent: %v", err)
				}
				child := children[i]
				child.Status = "cancelled"
				if err := s.CompleteTurn(ctx, child, Message{Role: RoleSystem, Content: "cancelled"}); err != nil {
					t.Errorf("complete child: %v", err)
				}
			}
		})
		seen := map[string]bool{}
		var cursor *PendingTurnCursor
		var moved string
		for {
			page, next, err := s.PendingTurnsPage(ctx, 2, cursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, turn := range page {
				seen[turn.ID] = true
			}
			if next != nil && next.Priority == 1 && moved == "" {
				for _, child := range children {
					if !seen[child.ID] {
						if active, err := s.ActivateTurn(ctx, child); err != nil || !active {
							t.Fatalf("activate moving child: %v %v", active, err)
						}
						moved = child.ID
						break
					}
				}
			}
			if len(page) < 2 {
				break
			}
			cursor = next
		}
		if moved == "" {
			t.Fatal("no unvisited waiting child to move during scan")
		}
		for _, turn := range append(parents, children...) {
			if turn.ID != moved && !seen[turn.ID] {
				t.Fatalf("recovery pages skipped pending turn %s", turn.ID)
			}
		}
		cursor = nil
		foundMoved := false
		for {
			page, next, err := s.PendingTurnsPage(ctx, 2, cursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, turn := range page {
				foundMoved = foundMoved || turn.ID == moved
			}
			if len(page) < 2 {
				break
			}
			cursor = next
		}
		if !foundMoved {
			t.Fatal("next recovery scan did not revisit the turn that changed priority")
		}
	})
	t.Run("manager followups wait for synthesis without holding shared resources", func(t *testing.T) {
		thread, err := s.CreateThread(ctx, Thread{Namespace: "coordination-tests", ProfileName: "inert-" + uuid.NewString(), CreatedBy: "inert-user", Mode: ModePersona})
		if err != nil {
			t.Fatal(err)
		}
		parent, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString()}, Message{Content: "original task"})
		if err != nil {
			t.Fatal(err)
		}
		queueFollowup := func(content string) Turn {
			t.Helper()
			turn, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), QueueAfterActive: true}, Message{Content: content})
			if err != nil || turn.Status != "waiting" {
				t.Fatalf("followup admission: %#v %v", turn, err)
			}
			return turn
		}
		first := queueFollowup("followup before coordination")
		parent.Status = "succeeded"
		parent.Coordination = &Coordination{Status: "waiting"}
		if err := s.CompleteTurn(ctx, parent, Message{Role: RoleAssistant, Content: "peer request acknowledgement"}); err != nil {
			t.Fatal(err)
		}
		second := queueFollowup("followup during coordination")
		if _, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString()}, Message{Content: "ordinary overlap"}); !errors.Is(err, ErrTurnActive) {
			t.Fatalf("ordinary overlap admitted during coordination: %v", err)
		}
		if active, err := s.ActivateTurn(ctx, first); err != nil || active {
			t.Fatalf("followup activated before synthesis: %v %v", active, err)
		}
		otherThread, err := s.CreateThread(ctx, Thread{Namespace: thread.Namespace, ProfileName: thread.ProfileName, CreatedBy: "inert-user", Mode: ModePersona})
		if err != nil {
			t.Fatal(err)
		}
		other, _, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: otherThread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), QueueAfterActive: true}, Message{Content: "independent work"})
		if err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, other); err != nil || !active {
			t.Fatalf("coordination-blocked followups held shared resource: %v %v", active, err)
		}
		resume, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "bounded peer results"})
		if err != nil {
			t.Fatalf("waiting followups blocked synthesis admission: %v", err)
		}
		if active, err := s.ActivateTurn(ctx, resume); err != nil || active {
			t.Fatalf("synthesis bypassed occupied shared resource: %v %v", active, err)
		}
		other.Status = "succeeded"
		if err := s.CompleteTurn(ctx, other, Message{Role: RoleAssistant, Content: "independent result"}); err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, resume); err != nil || !active {
			t.Fatalf("synthesis did not pass blocked followups: %v %v", active, err)
		}
		if active, err := s.ActivateTurn(ctx, first); err != nil || active {
			t.Fatalf("followup activated before synthesis completed: %v %v", active, err)
		}
		resume.Status = "succeeded"
		if err := s.CompleteTurn(ctx, resume, Message{Role: RoleAssistant, Content: "final result"}); err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, second); err != nil || active {
			t.Fatalf("later followup passed earlier followup: %v %v", active, err)
		}
		if active, err := s.ActivateTurn(ctx, first); err != nil || !active {
			t.Fatalf("first followup not activated after synthesis: %v %v", active, err)
		}
		first.Status = "succeeded"
		if err := s.CompleteTurn(ctx, first, Message{Role: RoleAssistant, Content: "first followup result"}); err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, second); err != nil || !active {
			t.Fatalf("second followup not activated after first: %v %v", active, err)
		}
		second.Status = "succeeded"
		if err := s.CompleteTurn(ctx, second, Message{Role: RoleAssistant, Content: "second followup result"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("exclusive source journey without holding harness locks", func(t *testing.T) {
		parent := makeCoordinationParent(t, s)
		_, _, _, err := s.QueueTurn(ctx, Turn{Namespace: parent.Namespace, ThreadID: parent.ThreadID, ProfileName: parent.ProfileName, RequestID: uuid.NewString()}, Message{Content: "human overlap"})
		if !errors.Is(err, ErrTurnActive) {
			t.Fatalf("human overlap admitted: %v", err)
		}
		other, err := s.CreateThread(ctx, Thread{Namespace: parent.Namespace, ProfileName: parent.ProfileName, CreatedBy: "inert-user", Mode: ModePersona})
		if err != nil {
			t.Fatal(err)
		}
		occupied, _, _, err := s.QueueTurn(ctx, Turn{Namespace: other.Namespace, ThreadID: other.ID, ProfileName: parent.ProfileName, RequestID: uuid.NewString()}, Message{Content: "other conversation work"})
		if err != nil {
			t.Fatalf("waiting journey kept harness lock: %v", err)
		}
		resume, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "bounded peer results"})
		if err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, resume); err != nil || active {
			t.Fatalf("synthesis bypassed occupied harness: %v %v", active, err)
		}
		occupied.Status = "succeeded"
		if err = s.CompleteTurn(ctx, occupied, Message{Role: RoleAssistant, Content: "other result"}); err != nil {
			t.Fatal(err)
		}
		if active, err := s.ActivateTurn(ctx, resume); err != nil || !active {
			t.Fatalf("deferred synthesis not activated: %v %v", active, err)
		}
		resume.Status = "succeeded"
		if err = s.CompleteTurn(ctx, resume, Message{Role: RoleAssistant, Content: "one final result"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("duplicate recovery atomically admits one source synthesis", func(t *testing.T) {
		parent := makeCoordinationParent(t, s)
		missingParent := synthesisCandidate(parent)
		missingParent.ParentTurnID = ""
		if _, _, _, err := s.QueueTurn(ctx, missingParent, Message{Content: "missing parent"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("resumption without parent admitted: %v", err)
		}
		wrong := synthesisCandidate(parent)
		wrong.RequestID = uuid.NewString()
		if _, _, _, err := s.QueueTurn(ctx, wrong, Message{Content: "wrong correlation"}); !errors.Is(err, ErrRequestConflict) {
			t.Fatalf("wrong synthesis ID admitted: %v", err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		ids := map[string]bool{}
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "same bounded results"})
				if err != nil {
					t.Errorf("duplicate synthesis: %v", err)
					return
				}
				mu.Lock()
				ids[v.ID] = true
				mu.Unlock()
			}()
		}
		wg.Wait()
		if len(ids) != 1 {
			t.Fatalf("got %d synthesis turns", len(ids))
		}
		turns, err := s.ListTurns(ctx, parent.Namespace, parent.ThreadID)
		if err != nil || len(turns) != 2 || turns[0].Coordination.Status != "resuming" || turns[0].Coordination.ResumeTurnID != turns[1].ID {
			t.Fatalf("atomic transition: %#v %v", turns, err)
		}
		// Mutating a returned snapshot cannot alter MemoryStore's persisted state.
		turns[0].Coordination.Status = "cancelled"
		turns, _ = s.ListTurns(ctx, parent.Namespace, parent.ThreadID)
		if turns[0].Coordination.Status != "resuming" {
			t.Fatal("snapshot mutated durable journey")
		}
		turns[1].Status = "failed"
		if err = s.CompleteTurn(ctx, turns[1], Message{Role: RoleSystem, Content: "synthesis could not finish"}); err != nil {
			t.Fatal(err)
		}
		turns, _ = s.ListTurns(ctx, parent.Namespace, parent.ThreadID)
		if turns[0].Coordination.Status != "failed" {
			t.Fatalf("synthesis failure left orphan: %#v", turns)
		}
	})
	t.Run("cancel before admission prevents source restart", func(t *testing.T) {
		parent := makeCoordinationParent(t, s)
		if _, err := s.CancelCoordination(ctx, parent.Namespace, parent.ThreadID, parent.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "late results"}); !errors.Is(err, ErrCoordinationCancelled) {
			t.Fatalf("cancelled source resumed: %v", err)
		}
	})
	t.Run("cancel suppresses late child and synthesis completions", func(t *testing.T) {
		parent := makeCoordinationParent(t, s)
		childThread, err := s.CreateThread(ctx, Thread{Namespace: parent.Namespace, ProfileName: "inert-peer-" + uuid.NewString(), CreatedBy: "inert-user", Mode: ModePersona})
		if err != nil {
			t.Fatal(err)
		}
		child, _, _, err := s.QueueTurn(ctx, Turn{Namespace: childThread.Namespace, ThreadID: childThread.ID, ProfileName: childThread.ProfileName, RequestID: uuid.NewString(), ParentTurnID: parent.ID, ParentThreadID: parent.ThreadID, Deferred: true}, Message{Content: "peer request"})
		if err != nil {
			t.Fatal(err)
		}
		resume, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "bounded results"})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err = s.CancelCoordination(ctx, parent.Namespace, parent.ThreadID, parent.ID); err != nil {
				t.Fatal(err)
			}
		}
		for _, late := range []Turn{child, resume} {
			late.Status = "succeeded"
			if err = s.CompleteTurn(ctx, late, Message{Role: RoleAssistant, Content: "late final must be suppressed"}); err != nil {
				t.Fatal(err)
			}
		}
		turns, _ := s.ListTurns(ctx, parent.Namespace, parent.ThreadID)
		if turns[0].Coordination.Status != "cancelled" || turns[1].Status != "cancelled" {
			t.Fatalf("late synthesis resurrected: %#v", turns)
		}
		childTurns, _ := s.ListTurns(ctx, child.Namespace, child.ThreadID)
		if childTurns[0].Status != "cancelled" {
			t.Fatalf("late peer resurrected: %#v", childTurns)
		}
		messages, _ := s.ListMessages(ctx, parent.Namespace, parent.ThreadID)
		count := 0
		for _, m := range messages {
			if m.Content == "late final must be suppressed" {
				t.Fatal("late final persisted")
			}
			if m.Content == "Coordination cancelled. Late peer results will not resume this conversation." {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("duplicate cancellation receipts: %d", count)
		}
	})
	t.Run("cancel versus source queue remains terminal", func(t *testing.T) {
		for attempt := 0; attempt < 12; attempt++ {
			parent := makeCoordinationParent(t, s)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _, _, err := s.QueueTurn(ctx, synthesisCandidate(parent), Message{Content: "racing results"})
				if err != nil && !errors.Is(err, ErrCoordinationCancelled) {
					t.Errorf("queue race: %v", err)
				}
			}()
			go func() {
				defer wg.Done()
				if _, err := s.CancelCoordination(ctx, parent.Namespace, parent.ThreadID, parent.ID); err != nil {
					t.Errorf("cancel race: %v", err)
				}
			}()
			wg.Wait()
			turns, err := s.ListTurns(ctx, parent.Namespace, parent.ThreadID)
			if err != nil || turns[0].Coordination.Status != "cancelled" {
				t.Fatalf("race resurrected parent: %#v %v", turns, err)
			}
			if len(turns) == 2 {
				turns[1].Status = "succeeded"
				if err = s.CompleteTurn(ctx, turns[1], Message{Role: RoleAssistant, Content: "racing late final"}); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
