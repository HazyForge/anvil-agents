package chat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

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

func TestMemoryQueuedFollowups(t *testing.T) {
	exerciseQueuedFollowups(t, NewMemoryStore(), nil)
}

// Protects the manager's durable followup contract at the storage owner: the
// previously unfinished-thread gate rejected every amendment while work ran.
// One shared scenario checks memory parity and real PostgreSQL reconnect/FIFO;
// no fixture invents acceptance, completion, or persisted state.
func exerciseQueuedFollowups(t *testing.T, store Store, reconnect func() Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	namespace := "followups-" + uuid.NewString()
	profile := "manager"
	thread, err := store.CreateThread(ctx, Thread{Namespace: namespace, Mode: ModePersona, ProfileName: profile, CreatedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateThread(ctx, Thread{Namespace: namespace, Mode: ModePersona, ProfileName: "other-profile", CreatedBy: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pg, ok := store.(*PostgresStore); ok {
			_, _ = pg.pool.Exec(context.Background(), `DELETE FROM anvil_agents_chat.turns WHERE namespace=$1`, namespace)
			_, _ = pg.pool.Exec(context.Background(), `DELETE FROM anvil_agents_chat.threads WHERE namespace=$1`, namespace)
		}
	})
	makeTurn := func(thread Thread, text string, allowQueue bool) Turn {
		return Turn{Namespace: namespace, ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: text, QueueAfterActive: allowQueue, LockKeys: []string{"volume:shared"}, RunJSON: json.RawMessage(`{"spec":{"prompt":"` + text + `"}}`)}
	}
	first, _, _, err := store.QueueTurn(ctx, makeTurn(thread, "first", false), Message{Content: "Start work"})
	if err != nil {
		t.Fatal(err)
	}
	direct := makeTurn(thread, "direct", false)
	for _, deferred := range []bool{false, true} {
		direct.Deferred = deferred
		if _, _, _, err = store.QueueTurn(ctx, direct, Message{Content: "ordinary chat"}); !errors.Is(err, ErrTurnActive) {
			t.Fatalf("ordinary chat behavior changed (deferred=%v): %v", deferred, err)
		}
	}
	secondInput := makeTurn(thread, "second", true)
	second, secondUser, _, err := store.QueueTurn(ctx, secondInput, Message{Content: "Add the retry limit"})
	if err != nil || second.Status != "waiting" {
		t.Fatalf("first followup not retained: %#v, %v", second, err)
	}
	thirdInput := makeTurn(thread, "third", true)
	thirdInput.Deferred = true
	third, thirdUser, _, err := store.QueueTurn(ctx, thirdInput, Message{Content: "Use three attempts"})
	if err != nil || third.Status != "waiting" {
		t.Fatalf("second followup not retained: %#v, %v", third, err)
	}
	if secondUser.Sequence != 2 || thirdUser.Sequence != 3 {
		t.Fatalf("accepted message order = %d, %d", secondUser.Sequence, thirdUser.Sequence)
	}
	// A different profile that shares the volume must not pass earlier queued
	// amendments merely because its own thread has no active turn.
	fourthInput := makeTurn(other, "fourth", true)
	fourth, _, _, err := store.QueueTurn(ctx, fourthInput, Message{Content: "Other project"})
	if err != nil || fourth.Status != "waiting" {
		t.Fatalf("shared-resource followup not retained: %#v, %v", fourth, err)
	}
	for _, pending := range []Turn{second, third, fourth} {
		if active, err := store.ActivateTurn(ctx, pending); err != nil || active {
			t.Fatalf("activated %s before active work finished: %v, %v", pending.RunName, active, err)
		}
	}
	if reconnect != nil {
		store = reconnect()
	}
	turns, err := store.ListTurns(ctx, namespace, thread.ID)
	if err != nil || len(turns) != 3 || turns[0].ID != first.ID || turns[1].ID != second.ID || turns[2].ID != third.ID {
		t.Fatalf("accepted FIFO lost across reconnect: %#v, %v", turns, err)
	}
	first, second, third = turns[0], turns[1], turns[2]
	if len(second.RunJSON) == 0 || string(second.RunJSON) != string(secondInput.RunJSON) {
		// PostgreSQL JSONB reformatting must not be mistaken for intent mutation.
		var before, after any
		_ = json.Unmarshal(secondInput.RunJSON, &before)
		_ = json.Unmarshal(second.RunJSON, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("frozen followup intent lost: %s", second.RunJSON)
		}
	}
	replayed, user, _, err := store.QueueTurn(ctx, secondInput, Message{Content: "Add the retry limit"})
	if err != nil || replayed.ID != second.ID || user.ID != secondUser.ID {
		t.Fatalf("followup replay duplicated work: %#v, %v", replayed, err)
	}
	if _, _, _, err = store.QueueTurn(ctx, secondInput, Message{Content: "changed request"}); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("conflicting retry accepted: %v", err)
	}
	complete := func(turn Turn) {
		t.Helper()
		turn.Status = "succeeded"
		if err := store.CompleteTurn(ctx, turn, Message{Role: RoleAssistant, Content: "Completed " + turn.RunName}); err != nil {
			t.Fatal(err)
		}
	}
	complete(first)
	// Deliberately attempt activation backwards, as concurrent recovery/read
	// handlers can do. Storage must enforce order independently of poll order.
	for _, later := range []Turn{fourth, third} {
		if active, err := store.ActivateTurn(ctx, later); err != nil || active {
			t.Fatalf("later turn %s jumped queued work: %v, %v", later.RunName, active, err)
		}
	}
	if active, err := store.ActivateTurn(ctx, second); err != nil || !active {
		t.Fatalf("oldest followup did not activate: %v, %v", active, err)
	}
	if active, err := store.ActivateTurn(ctx, third); err != nil || active {
		t.Fatalf("two turns active on one thread: %v, %v", active, err)
	}
	complete(second)
	if active, err := store.ActivateTurn(ctx, fourth); err != nil || active {
		t.Fatalf("shared volume jumped earlier followup: %v, %v", active, err)
	}
	if active, err := store.ActivateTurn(ctx, third); err != nil || !active {
		t.Fatalf("second followup did not activate: %v, %v", active, err)
	}
	complete(third)
	if active, err := store.ActivateTurn(ctx, fourth); err != nil || !active {
		t.Fatalf("shared volume not released: %v, %v", active, err)
	}
	complete(fourth)
	messages, err := store.ListMessages(ctx, namespace, thread.ID)
	if err != nil || len(messages) != 6 {
		t.Fatalf("transcript lost or duplicated messages: %#v, %v", messages, err)
	}
	for i, want := range []string{"Start work", "Add the retry limit", "Use three attempts", "Completed first", "Completed second", "Completed third"} {
		if messages[i].Content != want {
			t.Fatalf("message %d = %q, want %q", i, messages[i].Content, want)
		}
	}
}
