// Package standing is the slice-1 standing in-process harness surface for
// Anvil Agents.
//
// Austin's 2026-09-18 direction prefers standing in-process harnesses for
// interactive chat snappiness: one long-lived harness/model session per agent
// thread (Grok-Bot feel) instead of paying a cold Job/Pod start per turn,
// delivered over WebSocket streaming for first-token feel.
//
// Slice 1 is API-first and stub-friendly, mirroring the Substrate spike's
// shape (internal/substrate) without touching it:
//
//   - Backend is the thin lifecycle a standing process needs: create (or
//     reuse) a session for a thread, resume it before a turn, stream tokens
//     for the turn, and suspend it when the turn goes idle.
//   - FakeBackend is an in-memory Backend with warm-reuse semantics for unit
//     tests. No test needs a live harness.
//   - The WebSocket helper (ws.go) is a stdlib-only RFC 6455 server upgrade
//     used by the runapi chat stream endpoint. It carries the same event
//     contract as the SSE fallback.
//
// Boundaries that do not move in slice 1:
//
//   - AgentRun stays append-only; new execution intent creates a new AgentRun.
//     A standing session accelerates turns, it never replaces durable turns.
//   - create-agent stays Wrapper/manager-only; peers request. Every accepted
//     message (direct turn and each peer child turn) still creates a real
//     AgentRun through the same turn path, so peer cooperation keeps working
//     with the same durable child threads — each thread (parent or peer
//     child) owns its standing session via EnsureTurnSession.
//   - Jobs remain the default execution plane for scouts and batch; the
//     optional SubstrateActor isolation/density plane is untouched. The
//     controller holds InProcess runs without creating a Job until a live
//     backend is wired behind an explicit opt-in gate.
//
// See docs/standing-inprocess-harness.md.
package standing

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SessionSpec describes the desired standing session for one chat thread.
// Namespace/ThreadID scope the session; SessionName is the stable process-
// local name derived from the thread ID (see SessionNameForThread).
// HarnessKind carries the Anvil harness adapter (codex, openCode, ...) so a
// future live backend can route to the matching runner.
type SessionSpec struct {
	Namespace   string
	ThreadID    string
	SessionName string
	HarnessKind string
	Labels      map[string]string
}

// SessionHandle is the observed identity of a standing session.
type SessionHandle struct {
	Namespace   string
	ThreadID    string
	SessionName string
	HarnessKind string
	// ID is the backend-assigned session identity. The fake derives a stable
	// ID from namespace/session name so warm reuse is observable without a
	// live harness.
	ID string
	// Warm reports whether the handle refers to a pre-existing session.
	Warm bool
	// Resumes counts resume transitions observed by the backend.
	Resumes int
	// Suspended reports the last observed idle state. It is advisory: suspend
	// is a density optimization, never a correctness gate.
	Suspended bool
}

// TokenEvent is one streamed unit of a standing turn. Seq orders tokens
// within the turn; the final event carries Done with the remainder of the
// reply (possibly empty) so the full reply is always recoverable from the
// stream even if a client joins late.
type TokenEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Seq      int    `json:"seq"`
	Token    string `json:"token"`
	Done     bool   `json:"done"`
}

// Sink receives streamed token events for a turn.
type Sink interface {
	OnToken(ctx context.Context, event TokenEvent) error
}

// SinkFunc adapts a function to a Sink.
type SinkFunc func(ctx context.Context, event TokenEvent) error

// OnToken implements Sink.
func (fn SinkFunc) OnToken(ctx context.Context, event TokenEvent) error {
	return fn(ctx, event)
}

// Backend is the minimal standing-session lifecycle the Anvil side needs for
// interactive chat: create (or reuse) a session per thread, resume it before
// a turn, stream the turn's tokens, and suspend it when the turn goes idle.
type Backend interface {
	CreateSession(ctx context.Context, spec SessionSpec) (SessionHandle, error)
	ResumeSession(ctx context.Context, namespace, name string) (SessionHandle, error)
	DescribeSession(ctx context.Context, namespace, name string) (SessionHandle, error)
	SuspendSession(ctx context.Context, namespace, name string) (SessionHandle, error)
	// StreamTurn runs one turn on the session and streams its tokens. It
	// returns the full reply so the caller can persist the durable turn
	// record; streaming is delivery, not storage.
	StreamTurn(ctx context.Context, handle SessionHandle, turnID, prompt string, sink Sink) (string, error)
}

// ErrSessionNotFound is returned when a session does not exist in the backend.
var ErrSessionNotFound = errors.New("standing session not found")

// maxPromptBytes bounds prompts accepted by the session contract, mirroring
// the chat prompt envelope so a standing turn can never smuggle a larger
// prompt than a Job turn.
const maxPromptBytes = 256 * 1024

// ValidateSpec rejects session specs that can never address a backend
// session. It stays intentionally small: real identity is enforced by the
// owning process, while slice 1 only needs fail-fast caller behavior.
func ValidateSpec(spec SessionSpec) error {
	if strings.TrimSpace(spec.Namespace) == "" {
		return fmt.Errorf("standing session namespace is required")
	}
	if strings.TrimSpace(spec.ThreadID) == "" {
		return fmt.Errorf("standing session thread ID is required")
	}
	if strings.TrimSpace(spec.SessionName) == "" {
		return fmt.Errorf("standing session name is required")
	}
	if strings.TrimSpace(spec.HarnessKind) != spec.HarnessKind || strings.ContainsAny(spec.HarnessKind, " \t\n\r") {
		return fmt.Errorf("standing session harness kind must not contain surrounding or inner whitespace")
	}
	return nil
}

// SessionKey scopes a session to its namespace, matching the Anvil rule that
// harness and volume references never cross namespaces.
func SessionKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}

// EnsureTurnSession binds one chat turn to its standing session. Direct turns
// and peer deliveries share this path: the caller maps the thread ID (parent
// thread for a direct message, recipient child thread for a peer delivery)
// through SessionNameForThread and this helper resumes the existing session
// or creates it exactly once.
//
// The boolean result reports warm reuse: false means the session was created
// by this call (cold start), true means an existing session was resumed.
// Retried deliveries with the same deterministic request ID converge on the
// same session without creating duplicate execution identity, and the chat
// durable-wait semantics for busy recipients carry over unchanged: a warm
// session never drops a queued peer turn, it only skips the cold start.
func EnsureTurnSession(ctx context.Context, backend Backend, spec SessionSpec) (SessionHandle, bool, error) {
	if backend == nil {
		return SessionHandle{}, false, errors.New("standing backend is not configured")
	}
	if err := ValidateSpec(spec); err != nil {
		return SessionHandle{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, false, err
	}
	if described, err := backend.DescribeSession(ctx, spec.Namespace, spec.SessionName); err == nil {
		resumed, err := backend.ResumeSession(ctx, spec.Namespace, spec.SessionName)
		if err == nil {
			return resumed, true, nil
		}
		if !errors.Is(err, ErrSessionNotFound) {
			return SessionHandle{}, false, err
		}
		// The session vanished between describe and resume; fall through to
		// cold create so the turn still binds exactly one session.
		_ = described
	} else if !errors.Is(err, ErrSessionNotFound) {
		return SessionHandle{}, false, err
	}
	created, err := backend.CreateSession(ctx, spec)
	if err != nil {
		return SessionHandle{}, false, err
	}
	return created, false, nil
}

// ShouldSuspendOnIdle reports whether an idle session should be suspended. Nil
// defaults to true; suspension only releases process-local resources, so the
// default favors density the way Substrate suspend-on-idle does.
func ShouldSuspendOnIdle(suspendOnIdle *bool) bool {
	if suspendOnIdle == nil {
		return true
	}
	return *suspendOnIdle
}

// SuspendIdleSession releases session resources when the turn goes idle. It
// is a no-op (not an error) when suspend-on-idle is disabled or the session
// is already gone, so terminal reconciliation stays best-effort: suspend is
// an optimization, never a correctness gate.
func SuspendIdleSession(ctx context.Context, backend Backend, namespace, name string, suspendOnIdle *bool) (bool, error) {
	if !ShouldSuspendOnIdle(suspendOnIdle) {
		return false, nil
	}
	if backend == nil {
		return false, errors.New("standing backend is not configured")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := backend.SuspendSession(ctx, namespace, name); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
