package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeThreadRequiresPersonaProfile(t *testing.T) {
	t.Parallel()
	_, err := NormalizeThread(Thread{
		Namespace: "agents",
		Mode:      ModePersona,
		CreatedBy: "user-1",
	}, time.Now().UTC())
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "profileName") {
		t.Fatalf("expected persona profile rejection, got %v", err)
	}
}

func TestNormalizeThreadAllowsFleetWithoutProfile(t *testing.T) {
	t.Parallel()
	thread, err := NormalizeThread(Thread{
		Namespace: "agents",
		Mode:      ModeFleet,
		CreatedBy: "user-1",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID == "" || thread.Title != DefaultThreadTitle || thread.ProfileName != "" {
		t.Fatalf("unexpected thread: %#v", thread)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	ctx := context.Background()
	created, err := store.CreateThread(ctx, Thread{
		Namespace:   "agents",
		ProfileName: "grok45",
		Mode:        ModePersona,
		CreatedBy:   "user-1",
		Metadata:    json.RawMessage(`{"source":"test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListThreads(ctx, ThreadFilter{Namespace: "agents", ProfileName: "grok45"})
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %#v err=%v", listed, err)
	}
	_, err = store.GetThread(ctx, "agents", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, thread, err := store.AppendMessages(ctx, "agents", created.ID, []Message{{
		Role:    RoleUser,
		Content: "hello standing chat",
	}, {
		Role:    RoleAssistant,
		Content: "echo: hello standing chat",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || stored[0].Sequence != 1 || stored[1].Sequence != 2 {
		t.Fatalf("messages = %#v", stored)
	}
	if thread.Title != "hello standing chat" {
		t.Fatalf("title = %q", thread.Title)
	}
	messages, err := store.ListMessages(ctx, "agents", created.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("list messages = %#v err=%v", messages, err)
	}
	_, err = store.GetThread(ctx, "agents", "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestNormalizeMessageRejectsEmptyContent(t *testing.T) {
	t.Parallel()
	_, err := NormalizeMessage(Message{
		ThreadID: "11111111-1111-4111-8111-111111111111",
		Role:     RoleUser,
		Content:  "   ",
		Sequence: 1,
	}, time.Now().UTC())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid content, got %v", err)
	}
}
