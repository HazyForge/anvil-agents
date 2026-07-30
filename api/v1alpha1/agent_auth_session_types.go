package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentAuthSessionProvider identifies which harness auth store a session mutates.
// codex covers OpenAI Codex ChatGPT/device-auth homes; grokBuild covers xAI Grok
// OAuth homes under the Grok Build durable root; openClaw covers OpenClaw-native
// per-agent auth profile stores (OAuth today; api_key profile import is supported
// structurally without putting keys in manifests).
// +kubebuilder:validation:Enum=codex;grokBuild;openClaw
type AgentAuthSessionProvider string

const (
	AgentAuthSessionProviderCodex     AgentAuthSessionProvider = "codex"
	AgentAuthSessionProviderGrokBuild AgentAuthSessionProvider = "grokBuild"
	AgentAuthSessionProviderOpenClaw  AgentAuthSessionProvider = "openClaw"
)

// AgentAuthSessionAction is the maintenance operation performed against durable auth state.
// reauth stages/replaces provider-native auth; logout clears it; verify checks it
// without mutation.
// +kubebuilder:validation:Enum=reauth;logout;verify
type AgentAuthSessionAction string

const (
	AgentAuthSessionActionReauth AgentAuthSessionAction = "reauth"
	AgentAuthSessionActionLogout AgentAuthSessionAction = "logout"
	AgentAuthSessionActionVerify AgentAuthSessionAction = "verify"
)

// AgentAuthSessionPhase is the controller-reported lifecycle of one auth maintenance session.
// +kubebuilder:validation:Enum=Pending;WaitingForIdle;Running;Succeeded;Failed
type AgentAuthSessionPhase string

const (
	AgentAuthSessionPhasePending        AgentAuthSessionPhase = "Pending"
	AgentAuthSessionPhaseWaitingForIdle AgentAuthSessionPhase = "WaitingForIdle"
	AgentAuthSessionPhaseRunning        AgentAuthSessionPhase = "Running"
	AgentAuthSessionPhaseSucceeded      AgentAuthSessionPhase = "Succeeded"
	AgentAuthSessionPhaseFailed         AgentAuthSessionPhase = "Failed"
)

// AgentAuthSessionSpec is the immutable intent for one auth maintenance operation.
// Credential bytes never appear in the spec; reauth mounts a short-lived staging Secret.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="AgentAuthSession spec is immutable; create a new session instead"
// +kubebuilder:validation:XValidation:rule="self.action != 'reauth' || (has(self.stagingSecretRef) && has(self.stagingSecretRef.name) && size(self.stagingSecretRef.name) > 0)",message="spec.stagingSecretRef is required when action is reauth"
// +kubebuilder:validation:XValidation:rule="self.action != 'reauth' || (has(self.seedID) && size(self.seedID) > 0)",message="spec.seedID is required when action is reauth"
// +kubebuilder:validation:XValidation:rule="!(self.action == 'logout' || self.action == 'verify') || !has(self.stagingSecretRef)",message="spec.stagingSecretRef is forbidden when action is logout or verify"
// +kubebuilder:validation:XValidation:rule="!(self.action == 'logout' || self.action == 'verify') || !has(self.seedID) || size(self.seedID) == 0",message="spec.seedID is forbidden when action is logout or verify"
// +kubebuilder:validation:XValidation:rule="self.action == 'reauth' || !has(self.bootstrapSecretRef)",message="spec.bootstrapSecretRef is allowed only when action is reauth"
// +kubebuilder:validation:XValidation:rule="self.action == 'reauth' || !has(self.bootstrapSecretKey) || size(self.bootstrapSecretKey) == 0",message="spec.bootstrapSecretKey is allowed only when action is reauth"
// +kubebuilder:validation:XValidation:rule="self.provider != 'openClaw' || (has(self.agentID) && size(self.agentID) > 0)",message="spec.agentID is required when provider is openClaw"
// +kubebuilder:validation:XValidation:rule="self.provider != 'openClaw' || (has(self.authMode) && size(self.authMode) > 0)",message="spec.authMode is required when provider is openClaw"
// +kubebuilder:validation:XValidation:rule="self.provider != 'openClaw' || (has(self.modelProvider) && size(self.modelProvider) > 0)",message="spec.modelProvider is required when provider is openClaw"
// +kubebuilder:validation:XValidation:rule="self.provider == 'openClaw' || !has(self.agentID) || size(self.agentID) == 0",message="spec.agentID is forbidden unless provider is openClaw"
// +kubebuilder:validation:XValidation:rule="self.provider == 'openClaw' || !has(self.modelProvider) || size(self.modelProvider) == 0",message="spec.modelProvider is forbidden unless provider is openClaw"
type AgentAuthSessionSpec struct {
	// Provider selects the harness-specific durable auth layout.
	Provider AgentAuthSessionProvider `json:"provider"`
	// Action selects whether to import fresh credentials, clear durable login
	// state, or verify existing provider-native auth without mutation.
	Action AgentAuthSessionAction `json:"action"`
	// AuthMode records how the staged or existing credentials authenticate.
	// Required for provider=openClaw (oauth is the current operational path;
	// apiKey is accepted for valid OpenClaw api_key profile import). Optional
	// for codex/grokBuild for backward compatibility.
	// +optional
	// +kubebuilder:validation:Enum=apiKey;oauth
	AuthMode AgentRunProviderAuthMode `json:"authMode,omitempty"`
	// ModelProvider identifies the credential provider inside the selected
	// OpenClaw agent store (for example xai). Required for provider=openClaw
	// and forbidden for other providers so an unrelated OAuth profile cannot
	// satisfy an auth receipt.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	ModelProvider string `json:"modelProvider,omitempty"`
	// AgentID is the OpenClaw agent identity whose auth store is maintained.
	// Required for provider=openClaw and forbidden for other providers. Must
	// match the harness OpenClaw AgentID (DNS label). The maintenance Job
	// resolves the registered agent's agentDir by strictly parsing the
	// volume-owned openclaw.json without loading plugins or concatenating an
	// assumed database path.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	AgentID string `json:"agentID,omitempty"`
	// DataVolumeRef names the same-namespace AgentDataVolume whose PVC holds the durable home.
	DataVolumeRef corev1.LocalObjectReference `json:"dataVolumeRef"`
	// StagingSecretRef names a same-namespace Secret that holds provider auth bytes for reauth.
	// Codex expects CODEX_AUTH_JSON; grokBuild expects GROK_AUTH_JSON; openClaw expects
	// OPENCLAW_AUTH_PROFILES_JSON (version=1 profile store). The controller mounts it
	// read-only and deletes it after a successful reauth when it is session-owned.
	// Forbidden for logout and verify.
	// +optional
	StagingSecretRef *corev1.LocalObjectReference `json:"stagingSecretRef,omitempty"`
	// BootstrapSecretRef optionally names the durable seed Secret updated after successful reauth.
	// The controller refuses ExternalSecret-managed targets.
	// +optional
	BootstrapSecretRef *corev1.LocalObjectReference `json:"bootstrapSecretRef,omitempty"`
	// BootstrapSecretKey is the key written into the bootstrap Secret. Defaults to CODEX_AUTH_JSON
	// for codex, GROK_AUTH_JSON for grokBuild, and OPENCLAW_AUTH_PROFILES_JSON for openClaw.
	// +optional
	BootstrapSecretKey string `json:"bootstrapSecretKey,omitempty"`
	// SeedID is an opaque marker written next to durable auth state so runners can reseed only
	// when operators deliberately change the seed. Required for reauth; forbidden for logout/verify.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._:-]+$`
	SeedID string `json:"seedID,omitempty"`
	// TimeoutSeconds bounds the maintenance Job active deadline. Defaults to 300.
	// +optional
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=3600
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
}

// AgentAuthSessionStatus records verified volume identity, Job progress, and terminal outcome.
type AgentAuthSessionStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// +kubebuilder:validation:Enum=Pending;WaitingForIdle;Running;Succeeded;Failed
	Phase              AgentAuthSessionPhase      `json:"phase,omitempty"`
	DataVolumeRef      *NamespacedObjectReference `json:"dataVolumeRef,omitempty"`
	DataVolumeUID      string                     `json:"dataVolumeUID,omitempty"`
	ClaimRef           *NamespacedObjectReference `json:"claimRef,omitempty"`
	ClaimUID           string                     `json:"claimUID,omitempty"`
	MountPath          string                     `json:"mountPath,omitempty"`
	SeedID             string                     `json:"seedID,omitempty"`
	JobRef             *NamespacedObjectReference `json:"jobRef,omitempty"`
	JobUID             string                     `json:"jobUID,omitempty"`
	ActiveConsumerRuns []string                   `json:"activeConsumerRuns,omitempty"`
	StartedAt          *metav1.Time               `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time               `json:"completedAt,omitempty"`
	LastError          string                     `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentauthsessions,scope=Namespaced,shortName=agauth
// +kubebuilder:printcolumn:name="Provider",type="string",JSONPath=".spec.provider"
// +kubebuilder:printcolumn:name="Action",type="string",JSONPath=".spec.action"
// +kubebuilder:printcolumn:name="Volume",type="string",JSONPath=".spec.dataVolumeRef.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentAuthSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentAuthSessionSpec   `json:"spec,omitempty"`
	Status AgentAuthSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentAuthSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentAuthSession `json:"items"`
}

// AgentAuthSessionIsTerminal reports whether the session has finished.
func AgentAuthSessionIsTerminal(phase AgentAuthSessionPhase) bool {
	switch phase {
	case AgentAuthSessionPhaseSucceeded, AgentAuthSessionPhaseFailed:
		return true
	default:
		return false
	}
}
