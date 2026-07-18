package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentHarnessProfileSpec stores one reusable backend and Kubernetes execution
// envelope. It intentionally excludes role, scope, skills, and run policy so
// callers can swap harnesses without changing what an agent knows or should do.
type AgentHarnessProfileSpec struct {
	// Description explains the harness profile's runtime and ownership boundary.
	// +optional
	Description string `json:"description,omitempty"`
	// Backend selects and configures the harness adapter and image.
	Backend AgentRunHarnessBackendSpec `json:"backend"`
	// Execution configures the pod identity, credentials, storage, resources,
	// placement, and timeout for the selected backend.
	// +optional
	Execution AgentRunHarnessExecutionSpec `json:"execution,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentharnessprofiles,scope=Namespaced,shortName=agharness
// +kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.backend.kind"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.backend.image"
// +kubebuilder:printcolumn:name="ServiceAccount",type="string",JSONPath=".spec.execution.serviceAccountName"
type AgentHarnessProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentHarnessProfileSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentHarnessProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentHarnessProfile `json:"items"`
}
