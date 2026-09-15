package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryTurnRetry(t *testing.T) { exerciseTurnRetry(t, NewMemoryStore()) }

func exerciseTurnRetry(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	profile := "retry-" + uuid.NewString()
	thread, err := s.CreateThread(ctx, Thread{Namespace: "retry-tests", Mode: ModePersona, ProfileName: profile, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	first, user, _, err := s.QueueTurn(ctx, Turn{Namespace: thread.Namespace, ThreadID: thread.ID, ProfileName: profile, RequestID: uuid.NewString(), RunName: "original", RunJSON: json.RawMessage(`{"spec":{"prompt":"hello"}}`), LockKeys: []string{"volume:" + profile}}, Message{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordRun(ctx, first, "original-uid"); err != nil {
		t.Fatal(err)
	}
	first.RunUID = "original-uid"
	retry := TurnRetry{RunName: "second", RunJSON: json.RawMessage(`{"spec":{"prompt":"hello"}}`), Reason: "no execution started", At: time.Now().Add(time.Minute).UTC()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RetryTurn(ctx, first, retry)
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, ErrRequestConflict) {
				t.Errorf("retry: %v", err)
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d retries", accepted)
	}
	turns, err := s.ListTurns(ctx, thread.Namespace, thread.ID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns=%#v err=%v", turns, err)
	}
	next := turns[0]
	if next.UserMessageID != user.ID || next.RequestID != first.RequestID || next.RetryCount != 1 || next.RunName != retry.RunName || next.RunUID != "" || next.Status != "queued" || len(next.Attempts) != 1 || next.Attempts[0].RunUID != first.RunUID || next.RetryAt == nil || !next.RetryAt.Equal(retry.At) {
		t.Fatalf("lost recovery receipt: %#v", next)
	}
	if err = s.RecordRun(ctx, first, first.RunUID); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("stale UID binding accepted: %v", err)
	}
	stale := first
	stale.Status = "failed"
	if err = s.CompleteTurn(ctx, stale, Message{Role: RoleSystem, Content: "stale failure"}); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("stale completion accepted: %v", err)
	}
	messages, err := s.ListMessages(ctx, thread.Namespace, thread.ID)
	if err != nil || len(messages) != 1 || messages[0].ID != user.ID {
		t.Fatalf("retry duplicated transcript: %#v %v", messages, err)
	}
	// Waiting to retry still owns the original profile and shared writable home.
	other, err := s.CreateThread(ctx, Thread{Namespace: thread.Namespace, Mode: ModePersona, ProfileName: profile + "-other", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.QueueTurn(ctx, Turn{Namespace: other.Namespace, ThreadID: other.ID, ProfileName: other.ProfileName, RequestID: uuid.NewString(), LockKeys: first.LockKeys}, Message{Content: "competing work"})
	if !errors.Is(err, ErrTurnActive) {
		t.Fatalf("retry released resource locks: %v", err)
	}
	// An idempotent send receipt follows the current attempt instead of executing
	// a duplicate message after the caller lost the original HTTP response.
	same, sameUser, _, err := s.QueueTurn(ctx, first, Message{Content: "hello"})
	if err != nil || same.RunName != next.RunName || sameUser.ID != user.ID {
		t.Fatalf("lost send idempotency: %#v %v", same, err)
	}
	for i := 1; i < MaxTurnRetries; i++ {
		retry.RunName = fmt.Sprintf("attempt-%d", i+2)
		next, err = s.RetryTurn(ctx, next, retry)
		if err != nil {
			t.Fatal(err)
		}
	}
	retry.RunName = "over-budget"
	if _, err = s.RetryTurn(ctx, next, retry); !errors.Is(err, ErrInvalid) {
		t.Fatalf("retry budget ignored: %v", err)
	}
	next.Status = "succeeded"
	for range 2 {
		if err = s.CompleteTurn(ctx, next, Message{Role: RoleAssistant, Content: "hello back"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.RetryTurn(ctx, next, retry); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("terminal turn resurrected: %v", err)
	}
	messages, err = s.ListMessages(ctx, thread.Namespace, thread.ID)
	if err != nil || len(messages) != 2 || messages[1].Content != "hello back" {
		t.Fatalf("completion: %#v %v", messages, err)
	}
}
