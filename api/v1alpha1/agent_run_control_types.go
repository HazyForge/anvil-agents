package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type AgentRunControlLaunchPolicy string

const (
	AgentRunControlLaunchPolicyAllowed AgentRunControlLaunchPolicy = "Allowed"
	AgentRunControlLaunchPolicyPaused  AgentRunControlLaunchPolicy = "Paused"
)

type AgentRunControlPhase string

const (
	AgentRunControlPhaseAllowed AgentRunControlPhase = "Allowed"
	AgentRunControlPhasePaused  AgentRunControlPhase = "Paused"
	AgentRunControlPhaseExpired AgentRunControlPhase = "Expired"
	AgentRunControlPhaseBlocked AgentRunControlPhase = "Blocked"
)

// AgentRunControlSourceSpec records non-authoritative metadata about the event
// or operator request that created the control. Authorization is still derived
// from the authenticated API caller, not these descriptive fields.
type AgentRunControlSourceSpec struct {
	// +optional
	Kind string `json:"kind,omitempty"`
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	URL string `json:"url,omitempty"`
	// +optional
	Actor string `json:"actor,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.launchPolicy != 'Paused' || (has(self.reason) && size(self.reason) > 0)",message="spec.reason is required when launchPolicy is Paused"
type AgentRunControlSpec struct {
	// ApplicationRef identifies the cluster-scoped Application whose AgentRun
	// launches are governed by this control.
	ApplicationRef ApplicationReferenceSpec `json:"applicationRef"`
	// LaunchPolicy controls whether new application-scoped AgentRun Jobs may
	// launch. Paused never terminates an already-created Job.
	// +kubebuilder:validation:Enum=Allowed;Paused
	LaunchPolicy AgentRunControlLaunchPolicy `json:"launchPolicy"`
	// MaxConcurrentRuns optionally overrides the operator-wide concurrency
	// default for AgentRuns with this opaque application scope. When multiple
	// controls match, the lowest positive value wins.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxConcurrentRuns int32 `json:"maxConcurrentRuns,omitempty"`
	// Reason explains why the control exists. It is required for Paused controls.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Source carries optional descriptive metadata for audit and operator UI.
	// +optional
	Source *AgentRunControlSourceSpec `json:"source,omitempty"`
	// ExpiresAt makes the control inactive at or after this time. Expired Paused
	// controls no longer block launches.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

type AgentRunControlStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// +kubebuilder:validation:Enum=Allowed;Paused;Expired;Blocked
	Phase                 AgentRunControlPhase `json:"phase,omitempty"`
	AffectedScheduleCount int32                `json:"affectedScheduleCount,omitempty"`
	PendingRunCount       int32                `json:"pendingRunCount,omitempty"`
	ActiveRunCount        int32                `json:"activeRunCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentruncontrols,scope=Cluster,shortName=agctl
// +kubebuilder:printcolumn:name="Application",type="string",JSONPath=".spec.applicationRef.name"
// +kubebuilder:printcolumn:name="Policy",type="string",JSONPath=".spec.launchPolicy"
// +kubebuilder:printcolumn:name="Max Runs",type="integer",JSONPath=".spec.maxConcurrentRuns"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Schedules",type="integer",JSONPath=".status.affectedScheduleCount"
// +kubebuilder:printcolumn:name="Pending",type="integer",JSONPath=".status.pendingRunCount"
// +kubebuilder:printcolumn:name="Active",type="integer",JSONPath=".status.activeRunCount"
type AgentRunControl struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentRunControlSpec   `json:"spec,omitempty"`
	Status AgentRunControlStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentRunControlList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRunControl `json:"items"`
}
