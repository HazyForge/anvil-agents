package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentCouncilMemberSpec names one highest-level AgentRunProfile role in a
// durable multi-agent workforce inventory. Membership is inventory only; it
// does not auto-inject peer prompts, credentials, or execution authority.
type AgentCouncilMemberSpec struct {
	// Role is a human-readable council role label such as coordinator or
	// implementer. It is inventory metadata, not automatic peer authority.
	// +kubebuilder:validation:MinLength=1
	Role string `json:"role"`
	// ProfileRef selects an existing same-namespace AgentRunProfile that plays
	// this role. Namespace must be empty or the council namespace.
	ProfileRef NamespacedObjectReference `json:"profileRef"`
	// Description is an optional human/manager inventory note. It is not
	// auto-injected as peer authority into member runs.
	// +optional
	Description string `json:"description,omitempty"`
}

// AgentCouncilSpec is a named, durable group of highest-level AgentRunProfile
// members (a workforce). An optional CouncilPrompt explains multi-agent
// interaction and is materialized only when an executing profile or run opts
// in via councilRef.
//
// Councils do not create multi-agent Jobs. The controller still runs one
// adapter per AgentRun. Councils cannot select Secrets, ServiceAccounts,
// harnesses, tools, storage, or any other execution authority.
type AgentCouncilSpec struct {
	// Description explains the workforce boundary and when to select this council.
	// +optional
	Description string `json:"description,omitempty"`
	// Members lists the highest-level roles in this workforce. Each referenced
	// profile must exist in the council namespace and may appear only once.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Members []AgentCouncilMemberSpec `json:"members"`
	// CouncilPrompt is optional multi-agent interaction guidance. When non-empty
	// and the run/profile associates this council via councilRef, the controller
	// injects a reserved skill named council-<council-name>. Empty means
	// association-only inventory/evidence without prompt injection.
	// +kubebuilder:validation:MaxLength=65536
	// +optional
	CouncilPrompt string `json:"councilPrompt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentcouncils,scope=Namespaced,shortName=agcouncil
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentCouncil struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentCouncilSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentCouncilList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentCouncil `json:"items"`
}
