package substrate

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestEnsureTurnActorColdThenWarm covers the direct-turn contract: the first
// turn pays the cold create, every later turn on the same thread resumes the
// warm actor without creating duplicate execution identity.
func TestEnsureTurnActorColdThenWarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	spec := ActorSpecForRun("agents", ActorNameForThread("thread-1"), "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "chat-turn-1"})

	first, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil {
		t.Fatalf("ensure cold: %v", err)
	}
	if warm {
		t.Fatal("first ensure must report a cold create")
	}
	if first.State != ActorStateActive || first.ID == "" {
		t.Fatalf("cold handle = %+v, want active with an ID", first)
	}

	second, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil {
		t.Fatalf("ensure warm: %v", err)
	}
	if !warm {
		t.Fatal("second ensure must report a warm resume")
	}
	if second.ID != first.ID {
		t.Fatalf("warm ID = %q, want reuse of %q", second.ID, first.ID)
	}
	if got := client.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want exactly one warm actor", got)
	}
}

// TestPeerResumeContract pins the peer-delivery mapping end to end without a
// cluster. Peer coordination creates one durable child thread per recipient
// with a deterministic ID and a deterministic delivery request ID; the live
// backend resumes the recipient's thread actor through the same EnsureTurnActor
// path as a direct turn, so idempotent retries converge and the existing
// durable wait for busy recipients still owns queueing (warm actors never drop
// a queued peer turn, they only skip the cold start).
func TestPeerResumeContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	parentTurnID := uuid.NewString()
	recipient := "desktop-reviewer"

	// Deterministic child thread and delivery request IDs mirror
	// dispatchChatCoordination: same inputs always address the same actor.
	childID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-child/"+parentTurnID+"/"+recipient)).String()
	deliveryID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-delivery/"+parentTurnID+"/"+recipient)).String()
	if childID == "" || deliveryID == "" {
		t.Fatal("peer child and delivery IDs must be deterministic and non-empty")
	}
	repeatChild := uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-child/"+parentTurnID+"/"+recipient)).String()
	if repeatChild != childID {
		t.Fatal("peer child thread ID must be stable across retries")
	}

	// Wrapper, manager, and peer threads all map through one total function.
	for _, threadID := range []string{"wrapper-thread", "manager-thread", childID} {
		first := ActorNameForThread(threadID)
		second := ActorNameForThread(threadID)
		if first == "" || first != second {
			t.Fatalf("thread %q maps to unstable actor names %q/%q", threadID, first, second)
		}
	}
	if ActorNameForThread(childID) == ActorNameForThread("wrapper-thread") {
		t.Fatal("peer child actor must not collide with the parent thread actor")
	}

	// Retried peer deliveries converge on one warm recipient actor.
	client := NewFakeClient()
	spec := ActorSpecForRun("agents", ActorNameForThread(childID), "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "peer-turn-" + deliveryID[:8]})
	if _, warm, err := EnsureTurnActor(ctx, client, spec); err != nil || warm {
		t.Fatalf("first peer ensure = warm=%v err=%v, want cold", warm, err)
	}
	for i := 0; i < 3; i++ {
		handle, warm, err := EnsureTurnActor(ctx, client, spec)
		if err != nil {
			t.Fatalf("peer retry %d: %v", i, err)
		}
		if !warm {
			t.Fatalf("peer retry %d must resume the warm recipient actor", i)
		}
		if handle.State != ActorStateActive {
			t.Fatalf("peer retry %d state = %q, want Active", i, handle.State)
		}
	}
	if got := client.Created(); got != 1 {
		t.Fatalf("distinct peer actors = %d, want exactly one recipient actor", got)
	}
}

func TestSuspendIdleActorHonorsOptOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	spec := ActorSpec{Namespace: "agents", Name: "chat-thread-1"}
	if _, _, err := EnsureTurnActor(ctx, client, spec); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	disabled := false
	if suspended, err := SuspendIdleActor(ctx, client, "agents", "chat-thread-1", &disabled); err != nil || suspended {
		t.Fatalf("opt-out suspend = %v/%v, want no-op", suspended, err)
	}
	described, err := client.DescribeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != ActorStateActive {
		t.Fatalf("opt-out actor state = %q, want Active", described.State)
	}

	if suspended, err := SuspendIdleActor(ctx, client, "agents", "chat-thread-1", nil); err != nil || !suspended {
		t.Fatalf("default suspend = %v/%v, want suspended", suspended, err)
	}
	if suspended, err := SuspendIdleActor(ctx, client, "agents", "missing", nil); err != nil || suspended {
		t.Fatalf("missing suspend = %v/%v, want best-effort no-op", suspended, err)
	}
}

func TestEnsureTurnActorFailsClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, _, err := EnsureTurnActor(ctx, nil, ActorSpec{Namespace: "agents", Name: "a"}); err == nil {
		t.Fatal("nil client must fail closed")
	}
	client := NewFakeClient()
	if _, _, err := EnsureTurnActor(ctx, client, ActorSpec{}); err == nil {
		t.Fatal("invalid spec must fail closed")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := EnsureTurnActor(cancelled, client, ActorSpec{Namespace: "agents", Name: "a"}); err == nil {
		t.Fatal("cancelled context must fail closed")
	}
}

// TestSuspendedActorResumesWarm pins the suspend-on-idle multiplexing
// contract: suspending an idle actor releases the worker without losing the
// actor, so the next turn resumes warm with stable identity instead of
// paying another cold create.
func TestSuspendedActorResumesWarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	spec := ActorSpecForRun("agents", ActorNameForThread("thread-idle-1"), "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "chat-turn-1"})

	cold, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil || warm {
		t.Fatalf("first ensure = warm=%v err=%v, want cold", warm, err)
	}
	if suspended, err := SuspendIdleActor(ctx, client, "agents", ActorNameForThread("thread-idle-1"), nil); err != nil || !suspended {
		t.Fatalf("default suspend = %v/%v, want suspended", suspended, err)
	}
	described, err := client.DescribeActor(ctx, "agents", ActorNameForThread("thread-idle-1"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != ActorStateSuspended {
		t.Fatalf("idle actor state = %q, want Suspended", described.State)
	}
	resumed, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil || !warm {
		t.Fatalf("post-idle ensure = warm=%v err=%v, want warm resume", warm, err)
	}
	if resumed.ID != cold.ID {
		t.Fatalf("resumed actor = %q, want warm reuse of %q", resumed.ID, cold.ID)
	}
	if resumed.State != ActorStateActive {
		t.Fatalf("resumed actor state = %q, want Active", resumed.State)
	}
	if got := client.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want exactly one actor across suspend/resume", got)
	}
}
