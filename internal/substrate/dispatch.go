package substrate

import (
	"context"
	"errors"
)

// EnsureTurnActor binds one chat turn to its warm actor. Direct turns and peer
// deliveries share this path: the caller maps the thread ID (parent thread for
// a direct message, recipient child thread for a peer delivery) through
// ActorNameForThread and this helper resumes the existing actor or creates it
// cold exactly once.
//
// The boolean result reports warm reuse: false means the actor was created by
// this call (cold start), true means an existing actor was resumed. Retried
// deliveries with the same deterministic request ID converge on the same actor
// without creating duplicate execution identity, and the chat durable-wait
// semantics for busy recipients carry over unchanged: a warm actor never drops
// a queued peer turn, it only skips the cold start.
func EnsureTurnActor(ctx context.Context, client Client, spec ActorSpec) (ActorHandle, bool, error) {
	if client == nil {
		return ActorHandle{}, false, errors.New("substrate client is not configured")
	}
	if err := ValidateSpec(spec); err != nil {
		return ActorHandle{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, false, err
	}
	if described, err := client.DescribeActor(ctx, spec.Namespace, spec.Name); err == nil {
		resumed, err := client.ResumeActor(ctx, spec.Namespace, spec.Name)
		if err == nil {
			return resumed, true, nil
		}
		if !errors.Is(err, ErrActorNotFound) {
			return ActorHandle{}, false, err
		}
		// The actor vanished between describe and resume; fall through to cold
		// create so the turn still binds exactly one actor.
		_ = described
	} else if !errors.Is(err, ErrActorNotFound) {
		return ActorHandle{}, false, err
	}
	created, err := client.CreateActor(ctx, spec)
	if err != nil {
		return ActorHandle{}, false, err
	}
	return created, false, nil
}

// SuspendIdleActor releases the actor worker when the turn goes idle. It is a
// no-op (not an error) when suspend-on-idle is disabled or the actor is
// already gone, so terminal reconciliation stays best-effort: suspend is an
// optimization for pool multiplexing, never a correctness gate.
func SuspendIdleActor(ctx context.Context, client Client, namespace, name string, suspendOnIdle *bool) (bool, error) {
	if !ShouldSuspendOnIdle(suspendOnIdle) {
		return false, nil
	}
	if client == nil {
		return false, errors.New("substrate client is not configured")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := client.SuspendActor(ctx, namespace, name); err != nil {
		if errors.Is(err, ErrActorNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
