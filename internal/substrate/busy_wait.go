package substrate

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// BusyRecipient is the actor-plane model of the chat durable wait for a busy
// recipient: one thread actor is occupied by a warm turn; a queued peer
// delivery waits until that turn completes, then resumes the same actor.
//
// The helper never drops the waiter and never Creates a second actor. Chat
// still owns the real queue (`waiting` while the profile is busy); this type
// is what the latency harness and FakeClient tests use to time and prove the
// wait-then-warm-resume seam. create-agent stays Wrapper/manager-only;
// agent-sandbox WarmPool is not this path.
type BusyRecipient struct {
	spec   ActorSpec
	handle ActorHandle

	mu   sync.Mutex
	busy bool
	// wait is closed when the recipient is free to resume. Occupy replaces
	// it with a fresh open channel; Release closes that channel after the
	// occupying turn has suspended.
	wait chan struct{}
}

// NewBusyRecipient binds one recipient actor spec. The actor is not occupied
// until Occupy; WaitThenEnsure on a free recipient Ensures immediately.
func NewBusyRecipient(spec ActorSpec) *BusyRecipient {
	return &BusyRecipient{spec: spec, wait: closedBusyWait()}
}

func closedBusyWait() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// Handle is the last observed actor identity (empty before the first Occupy
// or pre-bind). Warm resume must keep this ID.
func (b *BusyRecipient) Handle() ActorHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.handle
}

// Occupied reports whether a warm turn currently holds the recipient.
func (b *BusyRecipient) Occupied() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.busy
}

// Occupy ensures the recipient actor is Active and marks it busy. Callers
// that want a warm occupying turn (the latency scenario) must pre-bind with
// EnsureTurnActor first so this call resumes instead of creating.
func (b *BusyRecipient) Occupy(ctx context.Context, client Client) (ActorHandle, bool, error) {
	if b == nil {
		return ActorHandle{}, false, fmt.Errorf("substrate busy recipient is not configured")
	}
	b.mu.Lock()
	if b.busy {
		b.mu.Unlock()
		return ActorHandle{}, false, fmt.Errorf("substrate busy recipient %s is already occupied", ActorKey(b.spec.Namespace, b.spec.Name))
	}
	spec := b.spec
	b.mu.Unlock()

	handle, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil {
		return ActorHandle{}, false, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy {
		return ActorHandle{}, false, fmt.Errorf("substrate busy recipient %s is already occupied", ActorKey(spec.Namespace, spec.Name))
	}
	b.handle = handle
	b.busy = true
	b.wait = make(chan struct{})
	return handle, warm, nil
}

// Release completes the occupying warm turn (suspend-on-idle) and unblocks
// every WaitThenEnsure waiter. A warm actor is never deleted here: suspend
// releases the worker so the queued peer delivery can Resume the same
// identity. Release is a no-op when the recipient is not occupied.
func (b *BusyRecipient) Release(ctx context.Context, client Client) error {
	if b == nil {
		return fmt.Errorf("substrate busy recipient is not configured")
	}
	b.mu.Lock()
	if !b.busy {
		b.mu.Unlock()
		return nil
	}
	spec := b.spec
	wait := b.wait
	b.mu.Unlock()

	_, susErr := SuspendIdleActor(ctx, client, spec.Namespace, spec.Name, nil)

	b.mu.Lock()
	b.busy = false
	if wait != nil {
		select {
		case <-wait:
		default:
			close(wait)
		}
	}
	b.mu.Unlock()
	return susErr
}

// WaitThenEnsure blocks while the recipient is occupied, then Ensures the
// same actor. It never Creates a second actor when the occupied identity
// still exists: after Release the actor is Suspended and Ensure resumes it
// warm. Context cancel unblocks the waiter without dropping or creating.
func (b *BusyRecipient) WaitThenEnsure(ctx context.Context, client Client) (ActorHandle, bool, error) {
	if b == nil {
		return ActorHandle{}, false, fmt.Errorf("substrate busy recipient is not configured")
	}
	b.mu.Lock()
	wait := b.wait
	wantID := b.handle.ID
	spec := b.spec
	b.mu.Unlock()

	if wait != nil {
		select {
		case <-ctx.Done():
			return ActorHandle{}, false, ctx.Err()
		case <-wait:
		}
	}

	handle, warm, err := EnsureTurnActor(ctx, client, spec)
	if err != nil {
		return ActorHandle{}, false, err
	}
	if wantID != "" && handle.ID != wantID {
		return ActorHandle{}, false, fmt.Errorf("busy-wait resume identity %q != occupied %q", handle.ID, wantID)
	}
	b.mu.Lock()
	b.handle = handle
	b.mu.Unlock()
	return handle, warm, nil
}

// CountingClient wraps Client and counts successful CreateActor calls,
// including FakeClient reuse. The busy-recipient durable-wait path must stay
// at exactly one Create (the initial bind). A second Create means the waiter
// dropped the warm actor and started a new one.
type CountingClient struct {
	Client
	creates atomic.Int64
}

// CreateActor delegates and counts a successful create or reuse.
func (c *CountingClient) CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error) {
	if c == nil || c.Client == nil {
		return ActorHandle{}, fmt.Errorf("substrate client is not configured")
	}
	handle, err := c.Client.CreateActor(ctx, spec)
	if err == nil {
		c.creates.Add(1)
	}
	return handle, err
}

// Creates is the number of successful CreateActor calls observed.
func (c *CountingClient) Creates() int {
	if c == nil {
		return 0
	}
	return int(c.creates.Load())
}
