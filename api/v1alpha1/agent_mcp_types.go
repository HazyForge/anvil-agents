package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentMCPStdioTransport launches an MCP server as a child process. The first
// argv entry must be supplied by a selected AgentTool or the harness image.
type AgentMCPStdioTransport struct {
	// Command is the complete argv vector; shell evaluation is never used.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=1024
	Command []string `json:"command"`
	// RequiredEnv lists environment-variable names that must exist at preflight.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[A-Z_][A-Z0-9_]*$`
	// +listType=set
	// +optional
	RequiredEnv []string `json:"requiredEnv,omitempty"`
}

// AgentMCPHTTPHeader maps a non-secret HTTP header name to an environment
// variable populated by the harness identity/credential envelope.
type AgentMCPHTTPHeader struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9-]*$`
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:Pattern=`^[A-Z_][A-Z0-9_]*$`
	// +kubebuilder:validation:MaxLength=253
	EnvVar string `json:"envVar"`
}

// AgentMCPStreamableHTTPTransport connects to a Streamable HTTP MCP endpoint.
type AgentMCPStreamableHTTPTransport struct {
	// Endpoint must use HTTPS and cannot contain embedded credentials.
	// +kubebuilder:validation:Pattern=`^https://[^@/?#]+(?::[0-9]+)?(?:/[^?#]*)?$`
	// +kubebuilder:validation:MaxLength=4096
	Endpoint string `json:"endpoint"`
	// Headers map header names to environment variables; values never appear in
	// the CR or immutable payload.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	// +optional
	Headers []AgentMCPHTTPHeader `json:"headers,omitempty"`
	// RequiredEnv lists additional environment-variable names required by the
	// server integration.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[A-Z_][A-Z0-9_]*$`
	// +listType=set
	// +optional
	RequiredEnv []string `json:"requiredEnv,omitempty"`
}

// AgentMCPTransport is an exclusive MCP transport union.
// +kubebuilder:validation:XValidation:rule="has(self.stdio) != has(self.streamableHTTP)",message="exactly one of stdio or streamableHTTP must be set"
type AgentMCPTransport struct {
	// +optional
	Stdio *AgentMCPStdioTransport `json:"stdio,omitempty"`
	// +optional
	StreamableHTTP *AgentMCPStreamableHTTPTransport `json:"streamableHTTP,omitempty"`
}

// AgentMCPServerSpec defines one secret-free MCP server contract.
type AgentMCPServerSpec struct {
	// Description explains the server's bounded purpose.
	// +optional
	Description string            `json:"description,omitempty"`
	Transport   AgentMCPTransport `json:"transport"`
	// ToolAllowlist restricts the MCP tool names exposed to the model. Empty
	// allows all tools reported by tools/list.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:MinLength=1
	// +listType=set
	// +optional
	ToolAllowlist []string `json:"toolAllowlist,omitempty"`
}

// AgentMCPSetSpec is an ordered collection of AgentMCPServer refs.
type AgentMCPSetSpec struct {
	// Description explains the collection's intended use.
	// +optional
	Description string `json:"description,omitempty"`
	// ServerRefs resolve in declaration order in the consuming namespace.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ServerRefs []NamespacedObjectReference `json:"serverRefs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentmcpservers,scope=Namespaced,shortName=agmcp
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentMCPServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentMCPServerSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentMCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentMCPServer `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=agentmcpsets,scope=Namespaced,shortName=agmcps
// +kubebuilder:printcolumn:name="Description",type="string",JSONPath=".spec.description"
type AgentMCPSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AgentMCPSetSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type AgentMCPSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentMCPSet `json:"items"`
}
