package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentChainPhase is the high-level lifecycle of a completion-driven chain.
type AgentChainPhase string

const (
	AgentChainPhasePending      AgentChainPhase = "Pending"
	AgentChainPhaseIdle         AgentChainPhase = "Idle"
	AgentChainPhaseRunning      AgentChainPhase = "Running"
	AgentChainPhaseWaitingHuman AgentChainPhase = "WaitingHuman"
	AgentChainPhaseSuspended    AgentChainPhase = "Suspended"
	AgentChainPhaseBlocked      AgentChainPhase = "Blocked"
)

// AgentChainConcurrencyPolicy controls how many chain instances may be active.
// v1 only supports Forbid (one active instance). Queue is reserved.
type AgentChainConcurrencyPolicy string

const (
	AgentChainConcurrencyForbid AgentChainConcurrencyPolicy = "Forbid"
	AgentChainConcurrencyQueue  AgentChainConcurrencyPolicy = "Queue"
)

const (
	// AgentChainStartNowAnnotation is a replay-safe manual start. Change the
	// annotation value to a new token to request one new chain instance.
	AgentChainStartNowAnnotation = "control.anvil.hazyforge.io/chain-start-now"
	// AgentChainCancelInstanceAnnotation stops advancing the named instance.
	// Active Jobs are not killed.
	AgentChainCancelInstanceAnnotation = "control.anvil.hazyforge.io/chain-cancel-instance"
)

// AgentChainWhenSpec selects when a step may start after a prior step.
type AgentChainWhenSpec struct {
	// PreviousStep is the name of the immediately prior step that must finish.
	// Required for every step after the first.
	// +kubebuilder:validation:MinLength=1
	// +optional
	PreviousStep string `json:"previousStep,omitempty"`
	// OnPhases is the set of prior-run terminal phases that allow this step.
	// Empty defaults to [Succeeded].
	// +kubebuilder:validation:MaxItems=8
	// +optional
	OnPhases []AgentRunPhase `json:"onPhases,omitempty"`
	// OnDecisionActions optionally requires the prior run's status.decision.action
	// to match one of these values (case-insensitive). Empty disables the filter.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	OnDecisionActions []string `json:"onDecisionActions,omitempty"`
}

// AgentChainHandoffSpec controls status-only context injected into the next run.
// Secrets, credentials, and peer service accounts are never copied.
type AgentChainHandoffSpec struct {
	// IncludeDecision appends the prior run decision classification/action/summary.
	// +optional
	IncludeDecision bool `json:"includeDecision,omitempty"`
	// IncludeLatestReports appends prior status.reports entries (type/summary only).
	// +optional
	IncludeLatestReports bool `json:"includeLatestReports,omitempty"`
	// IncludePullRequestURL includes the prior run pullRequestURL when set.
	// +optional
	IncludePullRequestURL bool `json:"includePullRequestURL,omitempty"`
	// IncludeOutputExcerptBytes caps a prior output excerpt (0 = omit). Hard max 8192.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8192
	// +optional
	IncludeOutputExcerptBytes int `json:"includeOutputExcerptBytes,omitempty"`
	// IncludeAncestorSteps lists earlier steps in the same instance whose status
	// should be summarized. Empty means only the immediate previous step.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	IncludeAncestorSteps []string `json:"includeAncestorSteps,omitempty"`
}

// AgentChainStepSpec is one linear step in an AgentChain.
type AgentChainStepSpec struct {
	// Name is a stable step identifier within the chain.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
	// When controls advancement from the previous step. Required for all but the first step.
	// +optional
	When *AgentChainWhenSpec `json:"when,omitempty"`
	// RunTemplate is copied into the AgentRun created for this step. The controller
	// fills purpose, sourceRef, trigger, and chain labels when empty.
	RunTemplate AgentRunSpec `json:"runTemplate"`
	// Handoff controls status-only context from prior steps in the same instance.
	// +optional
	Handoff *AgentChainHandoffSpec `json:"handoff,omitempty"`
}

// AgentChainBackoffSpec delays new instance starts after a terminal stop.
type AgentChainBackoffSpec struct {
	// FailedSeconds delays new starts after an instance stops on Failed.
	// +kubebuilder:validation:Minimum=0
	// +optional
	FailedSeconds int `json:"failedSeconds,omitempty"`
	// NeedsHumanSeconds delays new starts after an instance stops on NeedsHuman.
	// +kubebuilder:validation:Minimum=0
	// +optional
	NeedsHumanSeconds int `json:"needsHumanSeconds,omitempty"`
}

// AgentChainSpec defines a GitOps-owned linear completion-driven agent workflow.
// It does not replace AgentSchedule (wall-clock cadence) or AgentCouncil
// (association-only inventory).
type AgentChainSpec struct {
	// ApplicationRef identifies the opaque product/workload scope for every step.
	// Required for AgentRunControl pause checks and provenance labels.
	// +optional
	ApplicationRef *ApplicationReferenceSpec `json:"applicationRef,omitempty"`
	// Suspend prevents new instances and step advances while retaining status.
	// Active Jobs continue.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
	// StartIntervalSeconds optionally starts a new instance on a wall-clock period.
	// Empty means instances start only via the chain-start-now annotation or CLI.
	// +kubebuilder:validation:Minimum=1
	// +optional
	StartIntervalSeconds int `json:"startIntervalSeconds,omitempty"`
	// StartInitialDelaySeconds delays the first automatic start after creation.
	// +kubebuilder:validation:Minimum=0
	// +optional
	StartInitialDelaySeconds int `json:"startInitialDelaySeconds,omitempty"`
	// ConcurrencyPolicy controls concurrent instances. Empty defaults to Forbid.
	// v1 implements Forbid only; Queue is reserved and rejected until implemented.
	// +kubebuilder:validation:Enum=Forbid;Queue
	// +optional
	ConcurrencyPolicy AgentChainConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`
	// MaxInstancesPerDay caps new instances started during a UTC calendar day.
	// Empty disables the cap.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxInstancesPerDay int `json:"maxInstancesPerDay,omitempty"`
	// Backoff delays automatic starts after a stopped instance.
	// Manual chain-start-now bypasses backoff.
	// +optional
	Backoff *AgentChainBackoffSpec `json:"backoff,omitempty"`
	// Steps is the ordered linear sequence. The first step starts each instance.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Steps []AgentChainStepSpec `json:"steps"`
}

// AgentChainStepRunStatus records one step's AgentRun for an instance.
type AgentChainStepRunStatus struct {
	// InstanceID is the chain instance that owns this step run.
	InstanceID string `json:"instanceId,omitempty"`
	// Step is the step name.
	Step string `json:"step,omitempty"`
	// RunRef points at the created AgentRun.
	RunRef *NamespacedObjectReference `json:"runRef,omitempty"`
	// Phase is the last observed AgentRun phase.
	Phase AgentRunPhase `json:"phase,omitempty"`
}

// AgentChainStatus is the observed state of an AgentChain.
type AgentChainStatus struct {
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition         `json:"conditions,omitempty"`
	Phase              AgentChainPhase            `json:"phase,omitempty"`
	ActiveInstanceID   string                     `json:"activeInstanceId,omitempty"`
	ActiveStep         string                     `json:"activeStep,omitempty"`
	ActiveRunRef       *NamespacedObjectReference `json:"activeRunRef,omitempty"`
	LastInstanceID     string                     `json:"lastInstanceId,omitempty"`
	LastStartToken     string                     `json:"lastStartToken,omitempty"`
	LastCancelToken    string                     `json:"lastCancelToken,omitempty"`
	// StepRuns lists step runs for the active or most recent instance (newest last).
	// +optional
	StepRuns []AgentChainStepRunStatus `json:"stepRuns,omitempty"`
	// NextStartAt is when the next automatic instance may start.
	// +optional
	NextStartAt *metav1.Time `json:"nextStartAt,omitempty"`
	// InstancesToday counts instances started in the current UTC day.
	// +optional
	InstancesToday int `json:"instancesToday,omitempty"`
	// LastError is a human-readable controller error for operators.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentchains,scope=Namespaced,shortName=agchain
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Active Step",type="string",JSONPath=".status.activeStep"
// +kubebuilder:printcolumn:name="Instance",type="string",JSONPath=".status.activeInstanceId"
// +kubebuilder:printcolumn:name="Next Start",type="string",JSONPath=".status.nextStartAt"
type AgentChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentChainSpec   `json:"spec,omitempty"`
	Status AgentChainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentChainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentChain `json:"items"`
}
