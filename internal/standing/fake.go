package standing

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// FakeBackend is an in-memory Backend for unit tests and API-first
// verification. It models the one property the slice depends on: sessions are
// standing and warm, so resuming an existing thread session skips the cold
// start. It performs no network, cluster, or model I/O.
type FakeBackend struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	// Calls records the lifecycle call sequence for assertions.
	Calls []string
	// Reply prefixes the deterministic streamed reply. Empty uses a default
	// that echoes the turn identity so tests can assert durability.
	Reply string
}

type fakeSession struct {
	spec    SessionSpec
	handle  SessionHandle
	created int
	turns   int
}

// NewFakeBackend returns an empty FakeBackend.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{sessions: map[string]*fakeSession{}}
}

func fakeID(namespace, name string) string {
	return "fake-standing-" + SessionKey(namespace, name)
}

func (f *FakeBackend) record(call string) {
	f.Calls = append(f.Calls, call)
}

// CreateSession creates the session or returns the existing warm session when
// the name is already known in the namespace. Re-creation never resets the
// resume counter, which is how tests observe warm reuse versus a cold start.
func (f *FakeBackend) CreateSession(ctx context.Context, spec SessionSpec) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	if err := ValidateSpec(spec); err != nil {
		return SessionHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := SessionKey(spec.Namespace, spec.SessionName)
	if existing, ok := f.sessions[key]; ok {
		f.record("create-reuse:" + key)
		handle := existing.handle
		handle.Warm = true
		handle.Suspended = false
		existing.handle = handle
		return handle, nil
	}
	handle := SessionHandle{
		Namespace:   spec.Namespace,
		ThreadID:    spec.ThreadID,
		SessionName: spec.SessionName,
		HarnessKind: spec.HarnessKind,
		ID:          fakeID(spec.Namespace, spec.SessionName),
	}
	f.sessions[key] = &fakeSession{spec: spec, handle: handle, created: 1}
	f.record("create:" + key)
	return handle, nil
}

func (f *FakeBackend) lookup(namespace, name string) (*fakeSession, string, error) {
	key := SessionKey(namespace, name)
	session, ok := f.sessions[key]
	if !ok {
		return nil, key, fmt.Errorf("%w: %s", ErrSessionNotFound, key)
	}
	return session, key, nil
}

// ResumeSession marks the session warm and counts the resume so tests can
// tell a warm resume apart from a fresh create.
func (f *FakeBackend) ResumeSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, key, err := f.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	session.handle.Warm = true
	session.handle.Suspended = false
	session.handle.Resumes++
	f.record("resume:" + key)
	return session.handle, nil
}

// DescribeSession returns the current handle without changing lifecycle state.
func (f *FakeBackend) DescribeSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, _, err := f.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	return session.handle, nil
}

// SuspendSession marks the session idle without dropping it, so the next
// turn resumes warm.
func (f *FakeBackend) SuspendSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, key, err := f.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	session.handle.Suspended = true
	f.record("suspend:" + key)
	return session.handle, nil
}

// StreamTurn streams a deterministic reply in word-sized token events and
// returns the full reply so the caller can persist the durable turn record.
// The reply echoes the turn identity and the prompt head: it proves ordering,
// first-token delivery, and durable completion without invoking a model.
func (f *FakeBackend) StreamTurn(ctx context.Context, handle SessionHandle, turnID, prompt string, sink Sink) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(turnID) == "" {
		return "", fmt.Errorf("standing turn ID is required")
	}
	if len(prompt) > maxPromptBytes {
		return "", fmt.Errorf("standing prompt exceeds %d bytes", maxPromptBytes)
	}
	f.mu.Lock()
	session, key, err := f.lookup(handle.Namespace, handle.SessionName)
	if err != nil {
		f.mu.Unlock()
		return "", err
	}
	session.turns++
	prefix := f.Reply
	if strings.TrimSpace(prefix) == "" {
		prefix = "standing reply"
	}
	f.record("stream:" + key + ":" + turnID)
	head := strings.TrimSpace(prompt)
	if len(head) > 256 {
		head = head[:256]
	}
	reply := prefix + " to turn " + strings.TrimSpace(turnID) + ": " + head
	f.mu.Unlock()

	words := strings.Fields(reply)
	var full strings.Builder
	seq := 0
	for i, word := range words {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		token := word
		if i < len(words)-1 {
			token += " "
		}
		full.WriteString(token)
		if sink != nil {
			if err := sink.OnToken(ctx, TokenEvent{
				ThreadID: handle.ThreadID,
				TurnID:   strings.TrimSpace(turnID),
				Seq:      seq,
				Token:    token,
			}); err != nil {
				return "", err
			}
		}
		seq++
	}
	if sink != nil {
		if err := sink.OnToken(ctx, TokenEvent{
			ThreadID: handle.ThreadID,
			TurnID:   strings.TrimSpace(turnID),
			Seq:      seq,
			Done:     true,
		}); err != nil {
			return "", err
		}
	}
	return full.String(), nil
}

// Created reports how many distinct sessions were created (not reused).
func (f *FakeBackend) Created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sessions)
}

// Turns reports how many streamed turns the backend has served.
func (f *FakeBackend) Turns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, session := range f.sessions {
		total += session.turns
	}
	return total
}
