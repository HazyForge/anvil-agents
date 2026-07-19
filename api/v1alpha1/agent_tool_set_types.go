package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentToolCompositionMode controls how a run-local tool-set selection
// combines with the selection inherited from an AgentRunProfile.
type AgentToolCompositionMode string

const (
	// AgentToolCompositionAppend retains profile-selected tool sets and appends
	// the current layer's refs. It is the default composition mode.
	AgentToolCompositionAppend AgentToolCompositionMode = "Append"
	// AgentToolCompositionReplace discards inherited profile tool-set refs
	// before resolving the current layer.
	AgentToolCompositionReplace AgentToolCompositionMode = "Replace"
)

// AgentToolCompositionSpec selects ordered AgentToolSets. Tool sets are
// resolved separately from skills so an external integration can evolve and
// be shared independently of the instructions that teach agents when to use
// it.
type AgentToolCompositionSpec struct {
	// Mode controls how this layer combines refs with inherited profile refs.
	// Empty defaults to Append.
	// +kubebuilder:validation:Enum=Append;Replace
	// +optional
	Mode AgentToolCompositionMode `json:"mode,omitempty"`
	// Refs selects AgentToolSets in declaration order. References must resolve
	// in the consuming AgentRun namespace.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Refs []NamespacedObjectReference `json:"refs,omitempty"`
}

// AgentToolSetSpec is a reusable collection of setup and verification
// contracts for tools supplied by external systems. It does not deploy a
// service or own credentials, identities, networking, storage, or placement;
// those stay with the external service and the consuming harness.
type AgentToolSetSpec struct {
	// Description explains what integration this tool set exposes.
	// +optional
	Description string `json:"description,omitempty"`
	// Tools are materialized into compatible agent harnesses in declaration
	// order.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Tools []AgentRunToolSpec `json:"tools,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agenttoolsets,scope=Namespaced,shortName=agtools
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentToolSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentToolSetSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentToolSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentToolSet `json:"items"`
}
