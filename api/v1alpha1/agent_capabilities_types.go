package v1alpha1

// AgentCapabilityCompositionMode controls whether one canonical composition
// layer appends to or replaces inherited legacy and canonical selections.
type AgentCapabilityCompositionMode string

const (
	AgentCapabilityCompositionAppend  AgentCapabilityCompositionMode = "Append"
	AgentCapabilityCompositionReplace AgentCapabilityCompositionMode = "Replace"
)

// AgentSkillSelection is an ordered union of one atomic skill or skill set.
// +kubebuilder:validation:XValidation:rule="has(self.skillRef) != has(self.skillSetRef)",message="exactly one of skillRef or skillSetRef must be set"
type AgentSkillSelection struct {
	// +optional
	SkillRef *NamespacedObjectReference `json:"skillRef,omitempty"`
	// +optional
	SkillSetRef *NamespacedObjectReference `json:"skillSetRef,omitempty"`
}

type AgentSkillCapabilityComposition struct {
	// +kubebuilder:validation:Enum=Append;Replace
	// +optional
	Mode AgentCapabilityCompositionMode `json:"mode,omitempty"`
	// Selections resolve atomics and sets in declaration order.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Selections []AgentSkillSelection `json:"selections,omitempty"`
	// Overrides apply after this layer's selections.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Overrides []AgentSkillOverrideSpec `json:"overrides,omitempty"`
}

// AgentToolSelection is an ordered union of one atomic tool or tool set.
// +kubebuilder:validation:XValidation:rule="has(self.toolRef) != has(self.toolSetRef)",message="exactly one of toolRef or toolSetRef must be set"
type AgentToolSelection struct {
	// +optional
	ToolRef *NamespacedObjectReference `json:"toolRef,omitempty"`
	// +optional
	ToolSetRef *NamespacedObjectReference `json:"toolSetRef,omitempty"`
}

type AgentToolCapabilityComposition struct {
	// +kubebuilder:validation:Enum=Append;Replace
	// +optional
	Mode AgentCapabilityCompositionMode `json:"mode,omitempty"`
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Selections []AgentToolSelection `json:"selections,omitempty"`
}

// AgentMCPSelection is an ordered union of one server or MCP set.
// +kubebuilder:validation:XValidation:rule="has(self.serverRef) != has(self.mcpSetRef)",message="exactly one of serverRef or mcpSetRef must be set"
type AgentMCPSelection struct {
	// +optional
	ServerRef *NamespacedObjectReference `json:"serverRef,omitempty"`
	// +optional
	MCPSetRef *NamespacedObjectReference `json:"mcpSetRef,omitempty"`
}

type AgentMCPCapabilityComposition struct {
	// +kubebuilder:validation:Enum=Append;Replace
	// +optional
	Mode AgentCapabilityCompositionMode `json:"mode,omitempty"`
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Selections []AgentMCPSelection `json:"selections,omitempty"`
}

// AgentCapabilitiesSpec is the canonical capability-selection block used by
// AgentRunProfiles and AgentRuns. Runtime identity, credentials, storage, and
// placement remain in the selected harness profile.
type AgentCapabilitiesSpec struct {
	// +optional
	Skills *AgentSkillCapabilityComposition `json:"skills,omitempty"`
	// +optional
	Tools *AgentToolCapabilityComposition `json:"tools,omitempty"`
	// +optional
	MCPServers *AgentMCPCapabilityComposition `json:"mcpServers,omitempty"`
}
