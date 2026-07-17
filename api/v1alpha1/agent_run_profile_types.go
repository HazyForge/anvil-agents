package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentRunProfileSpec stores reusable prompt, scope, tool, and execution
// defaults for AgentRuns in the same namespace.
type AgentRunProfileSpec struct {
	// Description explains the profile's intended ownership boundary and use.
	// +optional
	Description string `json:"description,omitempty"`
	// Scope supplies default application, target, namespace, and resource-kind
	// boundaries for AgentRuns that reference this profile.
	// +optional
	Scope AgentRunScopeSpec `json:"scope,omitempty"`
	// Docs supplies default docs/runtime consistency instructions.
	// +optional
	Docs *AgentRunDocsSpec `json:"docs,omitempty"`
	// IssueTracking supplies default ticket context and update policy.
	// +optional
	IssueTracking *AgentRunIssueTrackingSpec `json:"issueTracking,omitempty"`
	// Harness supplies default backend, prompt, skills, subagents, tools,
	// service account, credentials, data volumes, and execution settings.
	// New profiles should select reusable runtime mechanics through
	// harnessProfileRef and reusable capabilities through skillSets; this field
	// remains an inline compatibility and override layer in v1alpha1.
	// +optional
	Harness AgentRunHarnessSpec `json:"harness,omitempty"`
	// HarnessProfileRef selects a reusable same-namespace backend and execution
	// envelope. Inline harness runtime fields remain compatibility overrides.
	// +optional
	HarnessProfileRef *NamespacedObjectReference `json:"harnessProfileRef,omitempty"`
	// SkillSets composes ordered backend-neutral capability packs and
	// profile-local named skill overrides.
	// +optional
	SkillSets *AgentSkillCompositionSpec `json:"skillSets,omitempty"`
	// Notifications supplies default operator notification routing.
	// +optional
	Notifications *AgentRunNotificationSpec `json:"notifications,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentrunprofiles,scope=Namespaced,shortName=agprofile
// +kubebuilder:printcolumn:name="Application",type="string",JSONPath=".spec.scope.applicationRef.name"
// +kubebuilder:printcolumn:name="Target",type="string",JSONPath=".spec.scope.applicationTargetRef.name"
// +kubebuilder:printcolumn:name="Intent",type="string",JSONPath=".spec.harness.intent"
// +kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.harness.backend.kind"
// +kubebuilder:printcolumn:name="HarnessProfile",type="string",JSONPath=".spec.harnessProfileRef.name"
type AgentRunProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentRunProfileSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type AgentRunProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRunProfile `json:"items"`
}
