package standing

import (
	"context"
	"strings"
	"testing"
)

func TestSessionNameForThreadIsTotalAndStable(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"Desktop Reviewer",
		"",
		"   ",
		strings.Repeat("a", 400),
		"thread/with/slashes and spaces",
	} {
		first := SessionNameForThread(id)
		second := SessionNameForThread(id)
		if first == "" || first != second {
			t.Fatalf("session name for %q = %q/%q, want stable non-empty", id, first, second)
		}
		if len(first) > maxSessionNameLen {
			t.Fatalf("session name for %q exceeds %d bytes: %q", id, maxSessionNameLen, first)
		}
		if !strings.HasPrefix(first, sessionNamePrefix) {
			t.Fatalf("session name for %q = %q, want prefix %q", id, first, sessionNamePrefix)
		}
	}
	if SessionNameForThread("parent-thread") == SessionNameForThread("child-id") {
		t.Fatal("parent and peer child threads must not share a session name")
	}
}

func TestEnsureTurnSessionWarmsAcrossTurns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewFakeBackend()
	spec := SessionSpec{
		Namespace:   "agents",
		ThreadID:    "thread-1",
		SessionName: SessionNameForThread("thread-1"),
		HarnessKind: "openCode",
	}

	first, warm, err := EnsureTurnSession(ctx, backend, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if warm {
		t.Fatal("first ensure must be a cold create")
	}
	if first.Warm || first.ID == "" {
		t.Fatalf("cold handle = %+v, want fresh session with an ID", first)
	}

	second, warm, err := EnsureTurnSession(ctx, backend, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !warm {
		t.Fatal("second ensure must resume the warm session")
	}
	if second.ID != first.ID {
		t.Fatalf("warm resume changed identity: %q vs %q", second.ID, first.ID)
	}
	if second.Resumes != 1 {
		t.Fatalf("resumes = %d, want 1", second.Resumes)
	}
	if backend.Created() != 1 {
		t.Fatalf("created = %d, want exactly one session for the thread", backend.Created())
	}
}

func TestPeerChildThreadOwnsItsSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewFakeBackend()
	parent := SessionSpec{Namespace: "agents", ThreadID: "parent-thread", SessionName: SessionNameForThread("parent-thread"), HarnessKind: "openCode"}
	child := SessionSpec{Namespace: "agents", ThreadID: "child-id", SessionName: SessionNameForThread("child-id"), HarnessKind: "openCode"}

	parentHandle, _, err := EnsureTurnSession(ctx, backend, parent)
	if err != nil {
		t.Fatalf("parent ensure: %v", err)
	}
	childHandle, warm, err := EnsureTurnSession(ctx, backend, child)
	if err != nil {
		t.Fatalf("child ensure: %v", err)
	}
	if warm {
		t.Fatal("peer child session must start cold, not reuse the parent session")
	}
	if childHandle.ID == parentHandle.ID {
		t.Fatal("peer child session must not share the parent session identity")
	}
}

func TestStreamTurnOrdersTokensAndReturnsDurableReply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewFakeBackend()
	spec := SessionSpec{Namespace: "agents", ThreadID: "thread-1", SessionName: SessionNameForThread("thread-1"), HarnessKind: "openCode"}
	handle, _, err := EnsureTurnSession(ctx, backend, spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	var events []TokenEvent
	reply, err := backend.StreamTurn(ctx, handle, "turn-1", "review the proposal", SinkFunc(func(_ context.Context, event TokenEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if reply == "" || !strings.Contains(reply, "turn-1") {
		t.Fatalf("reply = %q, want durable reply carrying the turn identity", reply)
	}
	if len(events) < 2 {
		t.Fatalf("events = %d, want word tokens plus a done marker", len(events))
	}
	last := events[len(events)-1]
	if !last.Done || last.Seq != len(events)-1 {
		t.Fatalf("last event = %+v, want done marker with monotonic seq", last)
	}
	for i, event := range events {
		if event.Seq != i || event.TurnID != "turn-1" || event.ThreadID != "thread-1" {
			t.Fatalf("event %d = %+v, want ordered turn-scoped tokens", i, event)
		}
	}
	var rebuilt strings.Builder
	for _, event := range events {
		rebuilt.WriteString(event.Token)
	}
	if rebuilt.String() != reply {
		t.Fatalf("token concatenation = %q, want the durable reply %q", rebuilt.String(), reply)
	}
	// The first token must be deliverable before the turn completes: a
	// cancelled sink observes a partial stream while the error surfaces.
	firstOnly := 0
	_, err = backend.StreamTurn(ctx, handle, "turn-2", "hello again", SinkFunc(func(_ context.Context, _ TokenEvent) error {
		firstOnly++
		if firstOnly == 1 {
			return context.Canceled
		}
		return nil
	}))
	if err == nil {
		t.Fatal("expected sink cancellation to abort the stream")
	}
	if firstOnly != 1 {
		t.Fatalf("sink saw %d tokens, want the stream to stop at the first failure", firstOnly)
	}
}

func TestSuspendIdleSessionIsBestEffort(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewFakeBackend()
	spec := SessionSpec{Namespace: "agents", ThreadID: "thread-1", SessionName: SessionNameForThread("thread-1")}
	if _, _, err := EnsureTurnSession(ctx, backend, spec); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	off := false
	if suspended, err := SuspendIdleSession(ctx, backend, "agents", spec.SessionName, &off); err != nil || suspended {
		t.Fatalf("suspend with opt-out = %v/%v, want no-op", suspended, err)
	}
	if suspended, err := SuspendIdleSession(ctx, backend, "agents", spec.SessionName, nil); err != nil || !suspended {
		t.Fatalf("suspend with default = %v/%v, want suspend", suspended, err)
	}
	described, err := backend.DescribeSession(ctx, "agents", spec.SessionName)
	if err != nil || !described.Suspended {
		t.Fatalf("describe = %+v/%v, want suspended session retained", described, err)
	}
	// A suspended session still resumes warm: suspend is density, not a drop.
	resumed, warm, err := EnsureTurnSession(ctx, backend, spec)
	if err != nil || !warm || resumed.Suspended {
		t.Fatalf("resume after suspend = %+v/%v/%v, want warm active session", resumed, warm, err)
	}
	if suspended, err := SuspendIdleSession(ctx, backend, "agents", "missing", nil); err != nil || suspended {
		t.Fatalf("suspend of missing session = %v/%v, want best-effort no-op", suspended, err)
	}
}

func TestStandingValidationFailsFast(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewFakeBackend()
	if _, _, err := EnsureTurnSession(ctx, nil, SessionSpec{}); err == nil {
		t.Fatal("expected error without a backend")
	}
	for _, spec := range []SessionSpec{
		{},
		{Namespace: "agents", ThreadID: "t"},
		{Namespace: "agents", ThreadID: "t", SessionName: "s", HarnessKind: "bad kind"},
	} {
		if _, _, err := EnsureTurnSession(ctx, backend, spec); err == nil {
			t.Fatalf("expected validation error for %+v", spec)
		}
	}
	handle := SessionHandle{Namespace: "agents", ThreadID: "t", SessionName: SessionNameForThread("t")}
	if _, err := backend.StreamTurn(ctx, handle, "", "prompt", nil); err == nil {
		t.Fatal("expected turn ID validation")
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-1", "prompt", nil); err == nil {
		t.Fatal("expected unknown-session error")
	}
}
