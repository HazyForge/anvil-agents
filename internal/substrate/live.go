// Live Substrate ATE transport for the standing-chat actor backend.
//
// This file binds substrate.Client to the real Agent Substrate (ATE) surface
// in agent-substrate/substrate — the ateapi gRPC service
// (pkg/proto/ateapipb/ateapi.proto, service ateapi.Control) driven the same
// way cmd/kubectl-ate drives it — instead of any invented HTTP mapping.
// ATEControl is a narrow, proto-agnostic seam shaped 1:1 on the five
// lifecycle RPCs; the generated-stub dialer in ate_grpc.go
// (ateapipb.ControlClient) implements it, so swapping transports never
// touches Client callers, merge rules, or chat selection.
//
// Upstream lifecycle mapping (see docs/substrate-spike.md for the full table):
//
//	CreateActor   -> Control/CreateActor  (kubectl ate create actor <name> -a <atespace> --template <template>)
//	ResumeActor   -> Control/ResumeActor  (kubectl ate resume actor <name> -a <atespace>)
//	SuspendActor  -> Control/SuspendActor (kubectl ate suspend actor <name> -a <atespace>)
//	PauseActor    -> Control/PauseActor   (kubectl ate pause actor <name> -a <atespace>)
//	DescribeActor -> Control/GetActor     (kubectl ate get actor <name> -a <atespace>)
//
// Addressing: a Kubernetes namespace maps to the ATE atespace of the same
// name (both are k8s short-names), unless ATEClientConfig.Atespace overrides
// it. ActorSpec.ActorClass names the ActorTemplate in the actor's atespace;
// empty ActorClass falls back to ATEClientConfig.Template. ActorSpec.Pool
// becomes a worker_selector match label (see WorkerSelectorPoolLabel).
//
// The opt-in gate itself lives in gate.go (GateConfig): the controller and
// the latency harness consult GateConfig.LiveEnabled, then dial the client
// below (DialATEClient in ate_grpc.go). Tests construct ATEClient with an
// injected ATEControl (in-memory fake); the mapping below is the live
// transport contract.
package substrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// maxATEActorNameLen is the k8s short-name bound ATE enforces on actor names
// (ResourceMetadata.name, +k8s:format=k8s-short-name). Thread IDs in practice
// are UUIDs, so ActorNameForThread output ("chat-" + uuid, 41 chars) fits;
// overlong derived names fail fast in CreateActor instead of being sent to
// a server that would reject them.
const maxATEActorNameLen = 63

// WorkerSelectorPoolLabel carries ActorSpec.Pool into the ATE per-actor
// worker_selector, ANDed server-side with the template's own selector. The
// key is provisional: ATE matches opaque worker-pool labels defined by the
// fleet, so align this with the fleet's pool label before promoting past the
// spike. TODO(ate-pool-label): confirm against the Kind fleet's WorkerPool
// labels (see hack/install-ate-kind.sh overlay) and fleet docs.
const WorkerSelectorPoolLabel = "anvil.hazyforge.io/worker-pool"

// ATEActorState mirrors the ateapi.ActorState lifecycle vocabulary. Only the
// states the Anvil side can observe through ATEControl are listed.
type ATEActorState string

const (
	ATEActorStateUnspecified ATEActorState = "Unspecified"
	ATEActorStateResuming    ATEActorState = "Resuming"
	ATEActorStateRunning     ATEActorState = "Running"
	ATEActorStateSuspending  ATEActorState = "Suspending"
	ATEActorStateSuspended   ATEActorState = "Suspended"
	ATEActorStatePausing     ATEActorState = "Pausing"
	ATEActorStatePaused      ATEActorState = "Paused"
	ATEActorStateCrashed     ATEActorState = "Crashed"
	ATEActorStateDeleting    ATEActorState = "Deleting"
	// ATEActorStateReverting tracks an actor mid-RevertActor (upstream
	// ACTOR_STATE_REVERTING = 9, added in 944abe3): the server is moving a
	// crashed, running, or paused actor back to SUSPENDED. The spike binds
	// no Revert/Delete RPCs; the state folds onto Suspended via MapATEState.
	ATEActorStateReverting ATEActorState = "Reverting"
)

// ATEObjectRef addresses one ATE resource. It mirrors ateapi.ObjectRef for
// atespaced resources: atespace is required, name is the actor name.
type ATEObjectRef struct {
	Atespace string
	Name     string
}

// ATEActor is the Anvil-side projection of an ateapi.Actor: identity plus the
// lifecycle state. Template and placement fields are echoed so tests and
// operators can verify what a create actually requested.
type ATEActor struct {
	Atespace         string
	Name             string
	UID              string
	TemplateAtespace string
	TemplateName     string
	WorkerSelector   map[string]string
	State            ATEActorState
}

// ATECreateSpec is the create payload for ATEControl.CreateActor, mirroring
// the actor field of ateapi.CreateActorRequest (metadata + actor_template
// ref + worker_selector).
type ATECreateSpec struct {
	Atespace         string
	Name             string
	TemplateAtespace string
	TemplateName     string
	WorkerSelector   map[string]string
}

// ATECode classifies transport-agnostic ATE failures so the mapping onto
// Client semantics (notably ErrActorNotFound) does not depend on gRPC status
// codes at the call site. The future generated-stub adapter maps
// codes.NotFound -> ATECodeNotFound, codes.AlreadyExists ->
// ATECodeAlreadyExists, and everything else to ATECodeOther.
type ATECode string

const (
	ATECodeNotFound      ATECode = "NotFound"
	ATECodeAlreadyExists ATECode = "AlreadyExists"
	ATECodeOther         ATECode = "Other"
)

// ATEError is the failure shape returned by ATEControl implementations.
type ATEError struct {
	Code ATECode
	Err  error
}

func (e *ATEError) Error() string {
	if e == nil || e.Err == nil {
		return "substrate ATE error"
	}
	return e.Err.Error()
}

// Unwrap lets errors.Is/As see through to the cause and to *ATEError itself.
func (e *ATEError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsATENotFound reports whether err is an ATE missing-actor failure. The
// generated-stub adapter must surface server codes.NotFound (which ateapi
// returns for unknown actors on every lifecycle RPC) through this path.
func IsATENotFound(err error) bool {
	var ateErr *ATEError
	if errors.As(err, &ateErr) {
		return ateErr.Code == ATECodeNotFound
	}
	return false
}

// IsATEAlreadyExists reports whether err signals the actor already exists
// (server codes.AlreadyExists on CreateActor), which the client treats as
// warm reuse rather than failure.
func IsATEAlreadyExists(err error) bool {
	var ateErr *ATEError
	if errors.As(err, &ateErr) {
		return ateErr.Code == ATECodeAlreadyExists
	}
	return false
}

func ateNotFoundf(format string, args ...any) error {
	return &ATEError{Code: ATECodeNotFound, Err: fmt.Errorf(format, args...)}
}

// ATEControl is the narrow slice of ateapi.Control the Anvil side needs. Each
// method documents its exact upstream RPC (service ateapi.Control in
// pkg/proto/ateapipb/ateapi.proto) and the kubectl-ate equivalent so the
// future generated-stub adapter is mechanical and reviewers can verify the
// binding without reading generated code.
type ATEControl interface {
	// GetActor issues Control/GetActor (kubectl ate get actor <name> -a
	// <atespace>). Unknown actors surface as ATECodeNotFound.
	GetActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error)
	// CreateActor issues Control/CreateActor with the actor deriving from the
	// given ActorTemplate (kubectl ate create actor <name> -a <atespace>
	// --template <template>). An existing name surfaces as
	// ATECodeAlreadyExists.
	CreateActor(ctx context.Context, spec ATECreateSpec) (ATEActor, error)
	// ResumeActor issues Control/ResumeActor (kubectl ate resume actor <name>
	// -a <atespace>). The resumed flag mirrors
	// ResumeActorResponse.resumed: false when the actor was already RUNNING
	// (warm no-op), true when a resume workflow ran. Unknown actors surface
	// as ATECodeNotFound.
	ResumeActor(ctx context.Context, ref ATEObjectRef) (ATEActor, bool, error)
	// SuspendActor issues Control/SuspendActor, checkpointing a running actor
	// or uploading the node-local snapshot of a paused one (kubectl ate
	// suspend actor <name> -a <atespace>). Unknown actors surface as
	// ATECodeNotFound.
	SuspendActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error)
	// PauseActor issues Control/PauseActor, keeping snapshots on the node VM
	// (kubectl ate pause actor <name> -a <atespace>). Unknown actors surface
	// as ATECodeNotFound.
	PauseActor(ctx context.Context, ref ATEObjectRef) (ATEActor, error)
}

// ATEClientConfig configures the live ATE plane. Address points at ateapi
// (kubectl-ate --endpoint); TokenFile holds the bearer token (kubectl-ate
// --token-file). The opt-in gate itself lives in GateConfig; this struct
// carries only transport parameters. Address and the token-file path (never
// token bytes) may appear in diagnostics; they must stay out of status.
type ATEClientConfig struct {
	// Address is the ateapi gRPC target. Required.
	Address string
	// Atespace forces one atespace for every actor. Empty maps each
	// Kubernetes namespace to the same-named atespace.
	Atespace string
	// Template is the default ActorTemplate when ActorSpec.ActorClass is
	// empty. Required because CreateActor always derives from a template.
	Template string
	// TokenFile is the path to the file holding the ateapi bearer token.
	TokenFile string
	// Token is the inline bearer token. Prefer TokenFile: inline tokens are
	// harder to rotate and easier to leak via the process environment. The
	// dialer only ever attaches it to the verified gRPC channel and it must
	// never appear in status, logs, or API JSON.
	Token string
	// Insecure dials TLS with certificate verification skipped for a local
	// Kind port-forward. ateapi always serves TLS, so this is still a TLS
	// channel — never plaintext. Kind-only: the dialer refuses every
	// non-loopback endpoint when set.
	// Production and shared clusters must use verified TLS (default).
	Insecure bool
	// CAFile is the path to the ateapi server CA PEM (ATE 0.0.8 jwt
	// ConfigMap ateapi-ca, key ca.crt). Path never includes PEM bytes.
	CAFile string
	// CAConfigMapName names a ConfigMap holding that CA when CAFile is
	// empty (default lookup name ateapi-ca when set).
	CAConfigMapName string
	// CAConfigMapNamespace is the ConfigMap namespace (default ate-system).
	CAConfigMapNamespace string
	// CAConfigMapKey is the ConfigMap data key (default ca.crt).
	CAConfigMapKey string
	// TLSServerName overrides TLS ServerName (default api.ate-system.svc).
	TLSServerName string
}

// ATEClientConfigFromGate bridges the opt-in gate to the transport config.
func ATEClientConfigFromGate(gate GateConfig) ATEClientConfig {
	return ATEClientConfig{
		Address:              strings.TrimSpace(gate.Endpoint),
		Atespace:             strings.TrimSpace(gate.Atespace),
		Template:             strings.TrimSpace(gate.Template),
		TokenFile:            strings.TrimSpace(gate.TokenFile),
		Token:                strings.TrimSpace(gate.Token),
		Insecure:             gate.Insecure,
		CAFile:               strings.TrimSpace(gate.CAFile),
		CAConfigMapName:      strings.TrimSpace(gate.CAConfigMapName),
		CAConfigMapNamespace: strings.TrimSpace(gate.CAConfigMapNamespace),
		CAConfigMapKey:       strings.TrimSpace(gate.CAConfigMapKey),
		TLSServerName:        strings.TrimSpace(gate.TLSServerName),
	}
}

// Validate rejects configurations that can never reach ateapi.
func (c ATEClientConfig) Validate() error {
	if c.Address == "" {
		return fmt.Errorf("substrate ATE address is required (see %s)", GateEndpointEnvVar)
	}
	if c.Template == "" {
		return fmt.Errorf("substrate ATE template is required (see %s)", GateTemplateEnvVar)
	}
	return nil
}

// NewATEClient binds the Client lifecycle onto a real ATE backend behind
// cfg. control is the ATEControl transport (DialATEControl in ate_grpc.go;
// tests inject an in-memory fake).
func NewATEClient(cfg ATEClientConfig, control ATEControl) (Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if control == nil {
		return nil, fmt.Errorf("substrate ATE control transport is required")
	}
	return &ATEClient{cfg: cfg, control: control, resumes: map[string]int{}}, nil
}

// ATEClient implements Client against a live ATE backend.
//
// Create/Resume mapping (the warm-actor property the spike depends on):
//   - CreateActor is create-or-reuse: GetActor first; a hit returns the warm
//     actor unchanged, a miss creates from the ActorTemplate, and an
//     AlreadyExists race re-reads. Re-creation never resets the resume
//     counter, so warm reuse stays observable like FakeClient.
//   - ResumeActor runs the turn's resume: the server reports whether a resume
//     workflow ran (ResumeActorResponse.resumed), which feeds the handle's
//     Resumes count; an already-RUNNING actor is a warm no-op.
//   - Peer turns resume the recipient's thread actor (see
//     ActorNameForThread) and suspend on idle, exactly as FakeClient models.
type ATEClient struct {
	cfg     ATEClientConfig
	control ATEControl

	mu      sync.Mutex
	resumes map[string]int
}

// ref resolves a namespace/name pair to the ATE address. Namespaces map to
// same-named atespaces unless the config forces one.
func (c *ATEClient) ref(namespace, name string) ATEObjectRef {
	atespace := c.cfg.Atespace
	if atespace == "" {
		atespace = strings.TrimSpace(namespace)
	}
	return ATEObjectRef{Atespace: atespace, Name: strings.TrimSpace(name)}
}

// createSpec resolves an ActorSpec to the ATE create payload: template from
// ActorClass (or the configured default) in the actor's atespace, placement
// from Pool.
func (c *ATEClient) createSpec(spec ActorSpec) ATECreateSpec {
	ref := c.ref(spec.Namespace, spec.Name)
	template := strings.TrimSpace(spec.ActorClass)
	if template == "" {
		template = c.cfg.Template
	}
	return ATECreateSpec{
		Atespace:         ref.Atespace,
		Name:             ref.Name,
		TemplateAtespace: ref.Atespace,
		TemplateName:     template,
		WorkerSelector:   selectorForPool(spec.Pool),
	}
}

// selectorForPool carries an optional pool into the per-actor
// worker_selector. Empty pools leave placement to the template default.
func selectorForPool(pool string) map[string]string {
	if strings.TrimSpace(pool) == "" {
		return nil
	}
	return map[string]string{WorkerSelectorPoolLabel: strings.TrimSpace(pool)}
}

// MapATEState folds the ateapi.ActorState vocabulary onto the Client
// tri-state. Transitional states map to their target (Resuming->Active,
// Suspending/Reverting->Suspended, Pausing->Paused). Crashed holds no worker like
// Suspended, so it maps there and the next Resume either rehydrates or fails
// loudly. Deleting/Unspecified/unknown values map to Active so Describe never
// fails on a valid server state; callers observing them should re-Describe.
// TODO(ate-terminal-states): surface Crashed/Deleting distinctly once the
// controller consumes live states instead of the SubstrateActorNotWired hold.
func MapATEState(state ATEActorState) ActorState {
	switch state {
	case ATEActorStateRunning, ATEActorStateResuming:
		return ActorStateActive
	case ATEActorStateSuspended, ATEActorStateSuspending, ATEActorStateCrashed, ATEActorStateReverting:
		return ActorStateSuspended
	case ATEActorStatePaused, ATEActorStatePausing:
		return ActorStatePaused
	default:
		return ActorStateActive
	}
}

// handle builds the observed Client handle, attaching the client-side resume
// count for warm-vs-cold observability.
func (c *ATEClient) handle(actor ATEActor) ActorHandle {
	key := ActorKey(actor.Atespace, actor.Name)
	c.mu.Lock()
	resumes := c.resumes[key]
	c.mu.Unlock()
	return ActorHandle{
		Namespace: actor.Atespace,
		Name:      actor.Name,
		ID:        actor.UID,
		State:     MapATEState(actor.State),
		Resumes:   resumes,
	}
}

// noteResumed records a server-executed resume workflow for warm-vs-cold
// observability. Best-effort and client-local: it counts resume workflows
// this client observed, not a server-side total.
func (c *ATEClient) noteResumed(actor ATEActor) {
	key := ActorKey(actor.Atespace, actor.Name)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resumes[key]++
}

// notFoundAsClient maps ATE missing-actor failures onto ErrActorNotFound so
// callers stay transport-agnostic (errors.Is(err, ErrActorNotFound) holds for
// both FakeClient and ATEClient).
func notFoundAsClient(ref ATEObjectRef, err error) error {
	if IsATENotFound(err) {
		return fmt.Errorf("%w: %s/%s", ErrActorNotFound, ref.Atespace, ref.Name)
	}
	return err
}

// CreateActor creates the actor from its template, or returns the existing
// warm actor when the name is already known (see ATEClient for the mapping).
func (c *ATEClient) CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	if err := ValidateSpec(spec); err != nil {
		return ActorHandle{}, err
	}
	if len(strings.TrimSpace(spec.Name)) > maxATEActorNameLen {
		return ActorHandle{}, fmt.Errorf("substrate actor name %q exceeds the ATE short-name bound of %d characters", strings.TrimSpace(spec.Name), maxATEActorNameLen)
	}
	create := c.createSpec(spec)
	ref := ATEObjectRef{Atespace: create.Atespace, Name: create.Name}

	if existing, err := c.control.GetActor(ctx, ref); err == nil {
		return c.handle(existing), nil
	} else if !IsATENotFound(err) {
		return ActorHandle{}, err
	}

	created, err := c.control.CreateActor(ctx, create)
	if err != nil {
		if IsATEAlreadyExists(err) {
			existing, getErr := c.control.GetActor(ctx, ref)
			if getErr != nil {
				return ActorHandle{}, notFoundAsClient(ref, getErr)
			}
			return c.handle(existing), nil
		}
		return ActorHandle{}, err
	}
	return c.handle(created), nil
}

// ResumeActor resumes the actor before a turn, counting the resume when the
// server ran a resume workflow.
func (c *ATEClient) ResumeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	ref := c.ref(namespace, name)
	actor, resumed, err := c.control.ResumeActor(ctx, ref)
	if err != nil {
		return ActorHandle{}, notFoundAsClient(ref, err)
	}
	if resumed {
		c.noteResumed(actor)
	}
	return c.handle(actor), nil
}

// SuspendActor checkpoints the actor and releases its worker.
func (c *ATEClient) SuspendActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	ref := c.ref(namespace, name)
	actor, err := c.control.SuspendActor(ctx, ref)
	if err != nil {
		return ActorHandle{}, notFoundAsClient(ref, err)
	}
	return c.handle(actor), nil
}

// PauseActor keeps the actor resident but unscheduled.
func (c *ATEClient) PauseActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	ref := c.ref(namespace, name)
	actor, err := c.control.PauseActor(ctx, ref)
	if err != nil {
		return ActorHandle{}, notFoundAsClient(ref, err)
	}
	return c.handle(actor), nil
}

// DescribeActor returns the current handle without changing lifecycle state.
func (c *ATEClient) DescribeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if err := ctx.Err(); err != nil {
		return ActorHandle{}, err
	}
	ref := c.ref(namespace, name)
	actor, err := c.control.GetActor(ctx, ref)
	if err != nil {
		return ActorHandle{}, notFoundAsClient(ref, err)
	}
	return c.handle(actor), nil
}
