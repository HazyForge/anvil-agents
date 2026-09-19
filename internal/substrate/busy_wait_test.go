package substrate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func busyRecipientSpec(threadID string) ActorSpec {
	return ActorSpecForRun("agents", ActorNameForThread(threadID), "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "peer-busy-wait"})
}

func fakeColdCreates(calls []string) int {
	n := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "create:") && !strings.HasPrefix(call, "create-reuse:") {
			n++
		}
	}
	return n
}

func fakeCreateReuses(calls []string) int {
	n := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "create-reuse:") {
			n++
		}
	}
	return n
}

// TestBusyRecipientWaitThenWarmResumeSameActor is the FakeClient contract
// for the optional latency scenario: occupy a warm recipient, durable-wait a
// peer delivery, then resume the same actor. No second Create, no drop.
func TestBusyRecipientWaitThenWarmResumeSameActor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := NewFakeClient()
	counter := &CountingClient{Client: fake}
	spec := busyRecipientSpec("peer-busy-child-1")

	cold, warm, err := EnsureTurnActor(ctx, counter, spec)
	if err != nil || warm {
		t.Fatalf("pre-bind = warm=%v err=%v, want cold", warm, err)
	}
	if cold.ID == "" || cold.State != ActorStateActive {
		t.Fatalf("pre-bind handle = %+v, want active with an ID", cold)
	}

	occ := NewBusyRecipient(spec)
	busy, warm, err := occ.Occupy(ctx, counter)
	if err != nil || !warm {
		t.Fatalf("occupy = warm=%v err=%v, want warm turn", warm, err)
	}
	if busy.ID != cold.ID {
		t.Fatalf("occupied actor = %q, want warm reuse of %q", busy.ID, cold.ID)
	}
	if !occ.Occupied() {
		t.Fatal("recipient must stay occupied until Release")
	}

	released := make(chan error, 1)
	go func() {
		time.Sleep(15 * time.Millisecond)
		released <- occ.Release(ctx, counter)
	}()

	start := time.Now()
	resumed, warm, err := occ.WaitThenEnsure(ctx, counter)
	waited := time.Since(start)
	if err != nil || !warm {
		t.Fatalf("wait-then-ensure = warm=%v err=%v, want warm resume", warm, err)
	}
	if relErr := <-released; relErr != nil {
		t.Fatalf("release: %v", relErr)
	}
	if resumed.ID != cold.ID {
		t.Fatalf("resumed actor = %q, want warm reuse of %q", resumed.ID, cold.ID)
	}
	if waited < 10*time.Millisecond {
		t.Fatalf("durable wait returned too quickly (%s); peer delivery must wait while busy", waited)
	}
	if occ.Occupied() {
		t.Fatal("recipient must be free after Release")
	}
	if got := fake.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want exactly one recipient actor", got)
	}
	if got := counter.Creates(); got != 1 {
		t.Fatalf("CreateActor calls = %d, want exactly one (no second Create)", got)
	}
	if got := fakeColdCreates(fake.Calls); got != 1 {
		t.Fatalf("cold creates in Calls = %d (%v), want 1", got, fake.Calls)
	}
	if got := fakeCreateReuses(fake.Calls); got != 0 {
		t.Fatalf("create-reuse calls = %d (%v), want 0", got, fake.Calls)
	}
}

// TestBusyRecipientDoesNotCreateWhileWaiting proves the queued peer delivery
// stays parked (no Create, no Resume of a second identity) until Release.
func TestBusyRecipientDoesNotCreateWhileWaiting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := NewFakeClient()
	counter := &CountingClient{Client: fake}
	spec := busyRecipientSpec("peer-busy-child-2")

	cold, _, err := EnsureTurnActor(ctx, counter, spec)
	if err != nil {
		t.Fatalf("pre-bind: %v", err)
	}
	occ := NewBusyRecipient(spec)
	if _, warm, err := occ.Occupy(ctx, counter); err != nil || !warm {
		t.Fatalf("occupy = warm=%v err=%v, want warm", warm, err)
	}

	done := make(chan struct{})
	var resumed ActorHandle
	var warm bool
	var waitErr error
	go func() {
		defer close(done)
		resumed, warm, waitErr = occ.WaitThenEnsure(ctx, counter)
	}()

	select {
	case <-done:
		t.Fatal("WaitThenEnsure returned before Release; durable wait dropped")
	case <-time.After(20 * time.Millisecond):
	}
	if !occ.Occupied() {
		t.Fatal("recipient must stay occupied while the peer delivery waits")
	}
	if got := fake.Created(); got != 1 {
		t.Fatalf("actors while waiting = %d, want 1", got)
	}
	if got := counter.Creates(); got != 1 {
		t.Fatalf("creates while waiting = %d, want 1 (no second Create)", got)
	}

	if err := occ.Release(ctx, counter); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitThenEnsure did not resume after Release")
	}
	if waitErr != nil || !warm {
		t.Fatalf("after release = warm=%v err=%v, want warm resume", warm, waitErr)
	}
	if resumed.ID != cold.ID {
		t.Fatalf("resumed actor = %q, want %q", resumed.ID, cold.ID)
	}
	if got := fake.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want 1", got)
	}
	if got := counter.Creates(); got != 1 {
		t.Fatalf("CreateActor calls = %d, want 1", got)
	}
}

func TestBusyRecipientCancelDoesNotCreate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := NewFakeClient()
	counter := &CountingClient{Client: fake}
	spec := busyRecipientSpec("peer-busy-child-3")

	if _, _, err := EnsureTurnActor(ctx, counter, spec); err != nil {
		t.Fatalf("pre-bind: %v", err)
	}
	occ := NewBusyRecipient(spec)
	if _, _, err := occ.Occupy(ctx, counter); err != nil {
		t.Fatalf("occupy: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, _, err := occ.WaitThenEnsure(ctx, counter)
		errCh <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("cancelled wait must fail closed, not drop into a create/resume")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled wait did not unblock")
	}
	if got := counter.Creates(); got != 1 {
		t.Fatalf("creates after cancel = %d, want 1", got)
	}
	if got := fake.Created(); got != 1 {
		t.Fatalf("actors after cancel = %d, want 1", got)
	}
}
