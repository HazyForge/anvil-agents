// Package substrate is the optional standing-chat actor backend surface for
// Anvil Agents.
//
// Substrate (agent-substrate/substrate) multiplexes idle actors onto warm
// workers with Create/Resume/Suspend/Pause, which lets interactive standing
// chat turns skip the cold Job/Pod start (~8-44s measured on Primaris) that
// dominates turn latency today. Kubernetes Jobs remain the default and only
// live execution plane: scouts, batch, scheduled, and chained runs always use
// Jobs, and the controller holds SubstrateActor runs without creating a Job
// until live dispatch lands (see docs/substrate-spike.md).
//
// Substrate is early and its APIs are expected to churn, so this package binds
// only to stable lifecycle concepts behind the Client interface. The live ATE
// binding (live.go) maps that interface onto the real ateapi Control RPCs
// (Create/Resume/Suspend/Pause/GetActor) without vendoring upstream generated
// types, so swapping in a generated-stub dialer later touches only the
// transport. Tests use FakeClient and an in-memory ATEControl fake; no test
// needs a live Substrate cluster.
package substrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ActorState is the lifecycle state of a Substrate actor as observed through
// the Client interface.
type ActorState string

const (
	// ActorStateActive means the actor is resident on a warm worker.
	ActorStateActive ActorState = "Active"
	// ActorStateSuspended means actor state is persisted and the worker is released.
	ActorStateSuspended ActorState = "Suspended"
	// ActorStatePaused means the actor is resident but not scheduled.
	ActorStatePaused ActorState = "Paused"
)

// ActorSpec describes the desired actor for one standing-chat execution.
// Namespace/Name scope the actor; HarnessKind carries the Anvil harness
// adapter (codex, openCode, ...) so the future live backend can route to the
// matching runner payload. ActorClass and Pool mirror the CRD tuning fields.
type ActorSpec struct {
	Namespace   string
	Name        string
	HarnessKind string
	ActorClass  string
	Pool        string
	Labels      map[string]string
}

// ActorHandle is the observed identity of an actor.
type ActorHandle struct {
	Namespace string
	Name      string
	// ID is the backend-assigned actor identity. The fake derives a stable ID
	// from namespace/name so warm reuse is observable without a cluster.
	ID    string
	State ActorState
	// Resumes counts ResumeActor transitions observed by the backend.
	Resumes int
}

// Client is the minimal actor lifecycle the Anvil side needs for standing
// chat: create (or reuse) a warm actor, resume it before a turn, and suspend
// or pause it when the turn goes idle.
type Client interface {
	CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error)
	ResumeActor(ctx context.Context, namespace, name string) (ActorHandle, error)
	SuspendActor(ctx context.Context, namespace, name string) (ActorHandle, error)
	PauseActor(ctx context.Context, namespace, name string) (ActorHandle, error)
	DescribeActor(ctx context.Context, namespace, name string) (ActorHandle, error)
}

// ErrActorNotFound is returned when an actor does not exist in the backend.
var ErrActorNotFound = errors.New("substrate actor not found")

// ValidateSpec rejects actor specs that can never address a backend actor.
// It stays intentionally small: DNS shape is enforced by the API server for
// real objects, while the spike only needs fail-fast client behavior.
func ValidateSpec(spec ActorSpec) error {
	if strings.TrimSpace(spec.Namespace) == "" {
		return fmt.Errorf("substrate actor namespace is required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("substrate actor name is required")
	}
	if strings.TrimSpace(spec.ActorClass) != spec.ActorClass || strings.ContainsAny(spec.ActorClass, " \t\n\r") {
		return fmt.Errorf("substrate actor class must not contain surrounding or inner whitespace")
	}
	if strings.TrimSpace(spec.Pool) != spec.Pool || strings.ContainsAny(spec.Pool, " \t\n\r") {
		return fmt.Errorf("substrate actor pool must not contain surrounding or inner whitespace")
	}
	return nil
}

// ActorKey scopes an actor to its namespace, matching the Anvil rule that
// harness and volume references never cross namespaces.
func ActorKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}
