package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestMemoryStandingConversation(t *testing.T) { exerciseStandingConversation(t, NewMemoryStore()) }

func exerciseStandingConversation(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	ns := "standing-" + uuid.NewString()
	seed := Thread{Namespace: ns, ProfileName: "manager", Mode: ModePersona, CreatedBy: "later-user"}
	direct, err := store.CreateThread(ctx, Thread{Namespace: ns, ProfileName: seed.ProfileName, Mode: ModeFleet, CreatedBy: "original-user", Metadata: json.RawMessage(`{"coordination":{"enabled":true,"allowedProfiles":["reviewer"]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AppendMessages(ctx, ns, direct.ID, []Message{{Role: RoleUser, Content: "Remember this work"}}); err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []string{`{"harnessProfileName":"experiment"}`, `{"sourceThreadId":"parent"}`, `{"sourceTurnId":"turn"}`, `{"sourceProfileName":"sender"}`} {
		if _, err = store.CreateThread(ctx, Thread{Namespace: ns, ProfileName: seed.ProfileName, Mode: ModePersona, CreatedBy: "user", Metadata: json.RawMessage(metadata)}); err != nil {
			t.Fatal(err)
		}
	}
	standing, created, err := store.EnsureStandingThread(ctx, seed)
	var before, after any
	_ = json.Unmarshal(direct.Metadata, &before)
	_ = json.Unmarshal(standing.Metadata, &after)
	if err != nil || created || standing.ID != direct.ID || standing.Mode != ModeFleet || standing.CreatedBy != "original-user" || !reflect.DeepEqual(before, after) {
		t.Fatalf("adoption lost identity: %#v created=%v err=%v", standing, created, err)
	}
	// A newer direct history cannot steal the agent identity after adoption.
	if _, err = store.CreateThread(ctx, seed); err != nil {
		t.Fatal(err)
	}
	standing, _, err = store.EnsureStandingThread(ctx, seed)
	if err != nil || standing.ID != direct.ID {
		t.Fatalf("canonical changed: %#v %v", standing, err)
	}
	messages, err := store.ListMessages(ctx, ns, standing.ID)
	if err != nil || len(messages) != 1 || messages[0].Content != "Remember this work" {
		t.Fatalf("history lost: %#v %v", messages, err)
	}
	threads, err := store.ListThreads(ctx, ThreadFilter{Namespace: ns, ProfileName: seed.ProfileName})
	if err != nil || len(threads) != 6 {
		t.Fatalf("historical threads changed: %d %v", len(threads), err)
	}
	for _, other := range []Thread{{Namespace: ns, ProfileName: "reviewer", Mode: ModePersona, CreatedBy: "user"}, {Namespace: ns + "-other", ProfileName: seed.ProfileName, Mode: ModePersona, CreatedBy: "user"}} {
		got, fresh, err := store.EnsureStandingThread(ctx, other)
		if err != nil || !fresh || got.ID == standing.ID {
			t.Fatalf("standing scope collision: %#v %v", got, err)
		}
	}
	seed.ProfileName = "new-agent"
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := store.EnsureStandingThread(ctx, seed)
			ids <- got.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatalf("concurrent canonical duplicates: %s %s", id, got)
		}
		id = got
	}
	threads, err = store.ListThreads(ctx, ThreadFilter{Namespace: ns, ProfileName: seed.ProfileName})
	if err != nil || len(threads) != 1 {
		t.Fatalf("concurrent first use created %d threads: %v", len(threads), err)
	}
}
