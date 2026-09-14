package chat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestMemoryTurnOutbox(t *testing.T) { exerciseTurnOutbox(t, NewMemoryStore()) }
func exerciseTurnOutbox(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	profile := "turn-test-" + uuid.NewString()
	makeThread := func() Thread {
		thread, err := s.CreateThread(ctx, Thread{Namespace: "turn-tests", Mode: ModePersona, ProfileName: profile, CreatedBy: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return thread
	}
	thread := makeThread()
	request := uuid.NewString()
	candidate := Turn{LockKeys: []string{"harness:" + profile, "volume:" + profile}, ProfileName: profile, Namespace: thread.Namespace, ThreadID: thread.ID, RequestID: request, RunName: "test-run", RunJSON: json.RawMessage(`{"spec":{"prompt":"hello"}}`)}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, _, _, err := s.QueueTurn(ctx, candidate, Message{Content: "hello"})
			if err != nil {
				t.Errorf("retry: %v", err)
				return
			}
			mu.Lock()
			ids[turn.ID] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("request created %d turns", len(ids))
	}
	turns, err := s.ListTurns(ctx, thread.Namespace, thread.ID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %#v %v", turns, err)
	}
	first := turns[0]
	var gotIntent, wantIntent any
	_ = json.Unmarshal(first.RunJSON, &gotIntent)
	_ = json.Unmarshal(candidate.RunJSON, &wantIntent)
	if !reflect.DeepEqual(gotIntent, wantIntent) {
		t.Fatalf("private intent lost %s", first.RunJSON)
	}
	// A second thread for the same profile must wait instead of sharing its home.
	secondThread := makeThread()
	secondCandidate := candidate
	secondCandidate.ThreadID = secondThread.ID
	secondCandidate.ProfileName = profile + "-other"
	secondCandidate.LockKeys = []string{"volume:" + profile, "harness:another"}
	secondCandidate.RequestID = uuid.NewString()
	if _, _, _, err = s.QueueTurn(ctx, secondCandidate, Message{Content: "second"}); !errors.Is(err, ErrTurnActive) {
		t.Fatalf("expected profile busy: %v", err)
	}
	secondCandidate.Deferred = true
	waiting, _, _, err := s.QueueTurn(ctx, secondCandidate, Message{Content: "second"})
	if err != nil || waiting.Status != "waiting" {
		t.Fatalf("waiting=%#v %v", waiting, err)
	}
	if ok, err := s.ActivateTurn(ctx, waiting); err != nil || ok {
		t.Fatalf("activated while busy: %v %v", ok, err)
	}
	first.Status = "succeeded"
	first.Delegates = []Delegate{{ThreadID: secondThread.ID, TurnID: waiting.ID}}
	for i := 0; i < 2; i++ {
		if err = s.CompleteTurn(ctx, first, Message{Role: RoleAssistant, Content: "actual reply"}); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := s.ListMessages(ctx, thread.Namespace, thread.ID)
	if err != nil || len(msgs) != 2 || msgs[1].Content != "actual reply" {
		t.Fatalf("messages %#v %v", msgs, err)
	}
	completed, _ := s.ListTurns(ctx, thread.Namespace, thread.ID)
	if len(completed[0].Delegates) != 1 {
		t.Fatal("delegate receipt lost")
	}
	if ok, err := s.ActivateTurn(ctx, waiting); err != nil || !ok {
		t.Fatalf("waiting did not activate: %v %v", ok, err)
	}
	if err = s.RecordRun(ctx, waiting, "immutable-uid"); err != nil {
		t.Fatal(err)
	}
	recorded, _ := s.ListTurns(ctx, waiting.Namespace, waiting.ThreadID)
	if recorded[0].RunUID != "immutable-uid" {
		t.Fatal("run UID not recorded")
	}
	stale := int64(0)
	retry := candidate
	retry.RequestID = uuid.NewString()
	retry.ExpectedSequence = &stale
	// Wait until the shared-resource child has completed before testing the
	// transcript version check, rather than receiving a resource-busy conflict.
	waiting.Status = "succeeded"
	if err = s.CompleteTurn(ctx, waiting, Message{Role: RoleAssistant, Content: "peer reply"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.QueueTurn(ctx, retry, Message{Content: "stale transcript"}); !errors.Is(err, ErrConversationChanged) {
		t.Fatalf("expected transcript conflict: %v", err)
	}
}
