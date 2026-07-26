package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentAuthSessionProvider identifies which harness auth store a session mutates.
// codex covers OpenAI Codex ChatGPT/device-auth homes; grokBuild covers xAI Grok
// OAuth homes under the Grok Build durable root.
// +kubebuilder:validation:Enum=codex;grokBuild
type AgentAuthSessionProvider string

const (
	AgentAuthSessionProviderCodex     AgentAuthSessionProvider = "codex"
	AgentAuthSessionProviderGrokBuild AgentAuthSessionProvider = "grokBuild"
)

// AgentAuthSessionAction is the maintenance operation performed against durable auth state.
// +kubebuilder:validation:Enum=reauth;logout
type AgentAuthSessionAction string

const (
	AgentAuthSessionActionReauth AgentAuthSessionAction = "reauth"
	AgentAuthSessionActionLogout AgentAuthSessionAction = "logout"
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
type AgentAuthSessionSpec struct {
	// Provider selects the harness-specific durable auth layout.
	Provider AgentAuthSessionProvider `json:"provider"`
	// Action selects whether to import fresh credentials or clear durable login state.
	Action AgentAuthSessionAction `json:"action"`
	// DataVolumeRef names the same-namespace AgentDataVolume whose PVC holds the durable home.
	DataVolumeRef corev1.LocalObjectReference `json:"dataVolumeRef"`
	// StagingSecretRef names a same-namespace Secret that holds provider auth bytes for reauth.
	// Codex expects CODEX_AUTH_JSON; grokBuild expects GROK_AUTH_JSON. The controller mounts it
	// read-only and deletes it after a successful reauth when it is session-owned.
	// +optional
	StagingSecretRef *corev1.LocalObjectReference `json:"stagingSecretRef,omitempty"`
	// BootstrapSecretRef optionally names the durable seed Secret updated after successful reauth.
	// The controller refuses ExternalSecret-managed targets.
	// +optional
	BootstrapSecretRef *corev1.LocalObjectReference `json:"bootstrapSecretRef,omitempty"`
	// BootstrapSecretKey is the key written into the bootstrap Secret. Defaults to CODEX_AUTH_JSON
	// for codex and GROK_AUTH_JSON for grokBuild.
	// +optional
	BootstrapSecretKey string `json:"bootstrapSecretKey,omitempty"`
	// SeedID is an opaque marker written next to durable auth.json so runners can reseed only
	// when operators deliberately change the seed. Required for reauth.
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
	Phase              AgentAuthSessionPhase     `json:"phase,omitempty"`
	DataVolumeRef      *NamespacedObjectReference `json:"dataVolumeRef,omitempty"`
	DataVolumeUID      string                    `json:"dataVolumeUID,omitempty"`
	ClaimRef           *NamespacedObjectReference `json:"claimRef,omitempty"`
	ClaimUID           string                    `json:"claimUID,omitempty"`
	MountPath          string                    `json:"mountPath,omitempty"`
	SeedID             string                    `json:"seedID,omitempty"`
	JobRef             *NamespacedObjectReference `json:"jobRef,omitempty"`
	JobUID             string                    `json:"jobUID,omitempty"`
	ActiveConsumerRuns []string                  `json:"activeConsumerRuns,omitempty"`
	StartedAt          *metav1.Time              `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time              `json:"completedAt,omitempty"`
	LastError          string                    `json:"lastError,omitempty"`
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
