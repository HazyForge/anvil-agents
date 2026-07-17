package v1alpha1

// NamespacedObjectReference identifies a Kubernetes object without importing
// another API group. Namespace may be omitted where same-namespace resolution
// is part of the containing resource contract.
type NamespacedObjectReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// SecretKeyReference identifies one key in a Secret. Cross-namespace reads are
// rejected by controllers unless the containing API explicitly allows them.
type SecretKeyReference struct {
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key"`
}

// ApplicationReferenceSpec is an opaque workload identity retained for wire
// compatibility with existing AgentRun resources. anvil-agents does not
// require or read an Application CRD.
type ApplicationReferenceSpec struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ApplicationTargetReferenceSpec is opaque target metadata retained for
// existing AgentRun scopes. The standalone operator does not require or read
// an ApplicationTarget CRD.
type ApplicationTargetReferenceSpec struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}
