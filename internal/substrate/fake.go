package substrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// FakeClient is an in-memory Client for unit tests and API-first verification.
// It models the one property the spike depends on: actors are warm and
// multiplexed, so creating an existing actor reuses it instead of paying a
// cold start. It performs no network or cluster I/O.
type FakeClient struct {
	mu     sync.Mutex
	actors map[string]*fakeActor
	// Calls records the lifecycle call sequence for assertions.
	Calls []string
}

type fakeActor struct {
	spec    ActorSpec
	handle  ActorHandle
	created int
}

// NewFakeClient returns an empty FakeClient.
func NewFakeClient() *FakeClient {
	return &FakeClient{actors: map[string]*fakeActor{}}
}

func fakeID(namespace, name string) string {
	return "fake-" + ActorKey(namespace, name)
}

func (f *FakeClient) record(call string) {
	f.Calls = append(f.Calls, call)
}

// CreateActor creates the actor or returns the existing warm actor when the
// name is already known in the namespace. Re-creation never resets the resume
// counter, which is how tests observe warm reuse versus a cold start.
func (f *FakeClient) CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	if err := ValidateSpec(spec); err != nil {
		return ActorHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := ActorKey(spec.Namespace, spec.Name)
	if existing, ok := f.actors[key]; ok {
		f.record("create-reuse:" + key)
		handle := existing.handle
		handle.State = ActorStateActive
		existing.handle = handle
		return handle, nil
	}
	handle := ActorHandle{
		Namespace: spec.Namespace,
		Name:      spec.Name,
		ID:        fakeID(spec.Namespace, spec.Name),
		State:     ActorStateActive,
	}
	f.actors[key] = &fakeActor{spec: spec, handle: handle, created: 1}
	f.record("create:" + key)
	return handle, nil
}

func (f *FakeClient) lookup(namespace, name string) (*fakeActor, string, error) {
	key := ActorKey(namespace, name)
	actor, ok := f.actors[key]
	if !ok {
		return nil, key, fmt.Errorf("%w: %s", ErrActorNotFound, key)
	}
	return actor, key, nil
}

// ResumeActor marks the actor active and counts the resume so tests can tell a
// warm resume apart from a fresh create.
func (f *FakeClient) ResumeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	actor, key, err := f.lookup(namespace, name)
	if err != nil {
		return ActorHandle{}, err
	}
	actor.handle.State = ActorStateActive
	actor.handle.Resumes++
	f.record("resume:" + key)
	return actor.handle, nil
}

// SuspendActor persists actor state and releases the worker.
func (f *FakeClient) SuspendActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	actor, key, err := f.lookup(namespace, name)
	if err != nil {
		return ActorHandle{}, err
	}
	actor.handle.State = ActorStateSuspended
	f.record("suspend:" + key)
	return actor.handle, nil
}

// PauseActor keeps the actor resident but unscheduled.
func (f *FakeClient) PauseActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	actor, key, err := f.lookup(namespace, name)
	if err != nil {
		return ActorHandle{}, err
	}
	actor.handle.State = ActorStatePaused
	f.record("pause:" + key)
	return actor.handle, nil
}

// DescribeActor returns the current handle without changing lifecycle state.
func (f *FakeClient) DescribeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	actor, _, err := f.lookup(namespace, name)
	if err != nil {
		return ActorHandle{}, err
	}
	return actor.handle, nil
}

// Created reports how many distinct actors were created (not reused).
func (f *FakeClient) Created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.actors)
}

// FakeGenerator is an in-memory TurnGenerator for controller tests.
type FakeGenerator struct {
	mu    sync.Mutex
	Reply string
	Err   error
	Calls []GenerateRequest
}

// Generate records the request and returns Reply or Err.
func (f *FakeGenerator) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, req)
	if f.Err != nil {
		return GenerateResult{}, f.Err
	}
	text := strings.TrimSpace(f.Reply)
	if text == "" {
		text = strings.TrimSpace(req.Prompt)
	}
	return GenerateResult{Text: text, StopReason: "end_turn"}, nil
}
