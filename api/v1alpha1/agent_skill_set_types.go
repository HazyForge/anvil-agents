package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentSkillSetSpec is a reusable, backend-neutral instruction pack. Skills
// and optional delegated personas stay together, while independently owned
// tools should use AgentToolSet. Images, workload identities, credentials, and
// placement remain owned by a harness profile or run profile.
type AgentSkillSetSpec struct {
	// Description explains when this capability pack should be selected.
	// +optional
	Description string `json:"description,omitempty"`
	// Global attaches this skill set to every AgentRun in the same namespace
	// unless the profile or run sets skillSets.excludeGlobal. Globals apply
	// before explicit profile/run refs and are not duplicated when also listed
	// explicitly. Use this for namespace shared instruction packs such as
	// knowledge-base usage that every agent should receive by default.
	// +optional
	Global bool `json:"global,omitempty"`
	// Skills are named instruction packs materialized for the selected harness.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Skills []AgentRunSkillInjectionSpec `json:"skills,omitempty"`
	// Tools are legacy setup and verification contracts required by this skill
	// set. Prefer AgentToolSet when a tool has an independent lifecycle.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Tools []AgentRunToolSpec `json:"tools,omitempty"`
	// Subagents are named delegated personas used by skills in this set.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Subagents []AgentRunSubagentSpec `json:"subagents,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentskillsets,scope=Namespaced,shortName=agskills
// +kubebuilder:printcolumn:name="Global",type="boolean",JSONPath=".spec.global"
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentSkillSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentSkillSetSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentSkillSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSkillSet `json:"items"`
}
