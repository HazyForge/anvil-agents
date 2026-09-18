package v1alpha1

import "strings"

// AgentRunExecutionRuntime selects the Kubernetes execution plane for a
// harness. It is orthogonal to AgentRunHarnessBackendKind: the backend kind
// selects the harness adapter (codex, openCode, ...) while the runtime selects
// where that adapter executes.
//
// Job is the default and only live plane. Every scout, batch, scheduled, and
// chained run stays on Jobs. SubstrateActor is an API-first spike surface for
// standing/manager chat turns that may run on warm Substrate actors instead of
// paying a cold Job/Pod start per turn. See docs/substrate-spike.md.
type AgentRunExecutionRuntime string

const (
	// AgentRunExecutionRuntimeJob creates exactly one Kubernetes Job per
	// AgentRun. This is the default when runtime is empty.
	AgentRunExecutionRuntimeJob AgentRunExecutionRuntime = "Job"
	// AgentRunExecutionRuntimeSubstrateActor routes execution to a warm
	// Substrate actor (agent-substrate/substrate) instead of a Job. Live
	// dispatch is not wired yet; the controller holds these runs without
	// creating a Job until the Kind e2e follow-up lands.
	AgentRunExecutionRuntimeSubstrateActor AgentRunExecutionRuntime = "SubstrateActor"
)

// AgentRunSubstrateActorSpec tunes the optional Substrate actor execution
// plane. It is required when execution.runtime is SubstrateActor and must be
// nil otherwise so Job runs never carry actor configuration by accident.
//
// Field names stay provider-neutral on purpose: Substrate is early and its
// APIs are expected to churn, so the Anvil side binds only to stable concepts
// (actor class, warm pool, idle suspend) behind the substrate.Client interface
// instead of vendoring Substrate API types.
type AgentRunSubstrateActorSpec struct {
	// ActorClass selects the Substrate actor class for this execution, for
	// example "standing-chat". Empty uses the Substrate default.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ActorClass string `json:"actorClass,omitempty"`
	// Pool selects the warm worker pool that multiplexes idle actors. Empty
	// uses the Substrate default pool.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Pool string `json:"pool,omitempty"`
	// SuspendOnIdle releases the warm worker while keeping actor state when
	// the turn goes idle. Nil defaults to true in the future live backend;
	// the spike surface records intent only.
	// +optional
	SuspendOnIdle *bool `json:"suspendOnIdle,omitempty"`
}

// AgentRunSubstrateActorStatus records the bound Substrate actor identity once
// live dispatch lands. The spike backend never populates it.
type AgentRunSubstrateActorStatus struct {
	// ActorName is the stable Substrate actor name for this execution.
	// +optional
	ActorName string `json:"actorName,omitempty"`
	// ActorID is the Substrate-assigned actor identity, if any.
	// +optional
	ActorID string `json:"actorId,omitempty"`
	// State is the last observed actor lifecycle state, for example
	// "Active", "Suspended", or "Paused".
	// +optional
	State string `json:"state,omitempty"`
}

// EffectiveRuntime returns the selected execution plane, defaulting to Job
// when runtime is empty or unrecognized so older clients keep Job semantics.
func (s *AgentRunHarnessExecutionSpec) EffectiveRuntime() AgentRunExecutionRuntime {
	if s == nil {
		return AgentRunExecutionRuntimeJob
	}
	switch AgentRunExecutionRuntime(strings.TrimSpace(string(s.Runtime))) {
	case AgentRunExecutionRuntimeSubstrateActor:
		return AgentRunExecutionRuntimeSubstrateActor
	default:
		return AgentRunExecutionRuntimeJob
	}
}

// UsesSubstrateActors reports whether this execution selects the optional
// Substrate actor plane instead of the default Job plane.
func (s *AgentRunHarnessExecutionSpec) UsesSubstrateActors() bool {
	return s.EffectiveRuntime() == AgentRunExecutionRuntimeSubstrateActor
}

// ValidateSubstrateExecution checks the structural shape of the optional
// Substrate surface. It returns a controller reason/message pair when invalid
// and empty strings when valid. Unknown runtime spellings are not rejected
// here; EffectiveRuntime folds them back to the Job default.
func ValidateSubstrateExecution(spec *AgentRunHarnessExecutionSpec) (reason, message string) {
	if spec == nil {
		return "", ""
	}
	raw := strings.TrimSpace(string(spec.Runtime))
	switch AgentRunExecutionRuntime(raw) {
	case "", AgentRunExecutionRuntimeJob:
		if spec.Substrate != nil {
			return "InvalidSubstrateSpec", "spec.harness.execution.substrate must only be set when execution.runtime is SubstrateActor."
		}
		return "", ""
	case AgentRunExecutionRuntimeSubstrateActor:
		if spec.Substrate == nil {
			return "InvalidSubstrateSpec", "spec.harness.execution.substrate is required when execution.runtime is SubstrateActor."
		}
		if reason, message := spec.Substrate.validate(); reason != "" {
			return reason, message
		}
		return "", ""
	default:
		// Forward-compatible: an unrecognized runtime folds back to Job, and
		// any actor section alongside it is rejected as likely misconfiguration.
		if spec.Substrate != nil {
			return "InvalidSubstrateSpec", "spec.harness.execution.substrate must only be set when execution.runtime is SubstrateActor."
		}
		return "", ""
	}
}

func (s *AgentRunSubstrateActorSpec) validate() (reason, message string) {
	if s == nil {
		return "InvalidSubstrateSpec", "spec.harness.execution.substrate is required when execution.runtime is SubstrateActor."
	}
	if strings.TrimSpace(s.ActorClass) != s.ActorClass || strings.ContainsAny(s.ActorClass, " \t\n\r") {
		return "InvalidSubstrateSpec", "spec.harness.execution.substrate.actorClass must not contain surrounding or inner whitespace."
	}
	if strings.TrimSpace(s.Pool) != s.Pool || strings.ContainsAny(s.Pool, " \t\n\r") {
		return "InvalidSubstrateSpec", "spec.harness.execution.substrate.pool must not contain surrounding or inner whitespace."
	}
	return "", ""
}
