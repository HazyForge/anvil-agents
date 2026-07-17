package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentDataVolumePhase string

const (
	AgentDataVolumePhasePending AgentDataVolumePhase = "Pending"
	AgentDataVolumePhaseReady   AgentDataVolumePhase = "Ready"
	AgentDataVolumePhaseBlocked AgentDataVolumePhase = "Blocked"
)

// +kubebuilder:validation:XValidation:rule="has(self.claimName) == has(oldSelf.claimName) && (!has(self.claimName) || self.claimName == oldSelf.claimName)",message="spec.claimName is immutable; migrate data through a new AgentDataVolume"
type AgentDataVolumeSpec struct {
	// ApplicationRef scopes this durable home to one Application. New
	// application-manager-owned volumes must set it; legacy unscoped volumes
	// remain readable during migration.
	// +optional
	ApplicationRef *ApplicationReferenceSpec `json:"applicationRef,omitempty"`
	// AgentName is the stable logical agent identity this storage belongs to.
	// +optional
	AgentName string `json:"agentName,omitempty"`
	// Backend documents the intended AgentRun backend for this durable home.
	// The controller does not require an exact match so data can be migrated,
	// but AgentRun prompts and operator UIs should show it.
	// +kubebuilder:validation:Enum=codex;hermesAgent;openClaw;grokBuild;piAgent;custom
	// +optional
	Backend AgentRunHarnessBackendKind `json:"backend,omitempty"`
	// ProfileRef references a namespace-local VolumeProfile that provides
	// reusable defaults for this concrete agent data volume.
	// +optional
	ProfileRef *corev1.LocalObjectReference `json:"profileRef,omitempty"`
	// ProfileVolumeName selects one entry from the referenced VolumeProfile.
	// It may be omitted only when the profile has exactly one volume entry.
	// +optional
	ProfileVolumeName string `json:"profileVolumeName,omitempty"`
	// ClaimName selects or creates the backing PersistentVolumeClaim. Empty
	// defaults to agent-data-<AgentDataVolume name>.
	// +optional
	ClaimName string `json:"claimName,omitempty"`
	// MountPath is the default container path for AgentRuns that attach this
	// data volume. Backend-specific env should point at this path or a child of
	// it.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// SubPath optionally mounts only one subdirectory of the claim by default.
	// +optional
	SubPath string `json:"subPath,omitempty"`
	// StorageClassName is used when creating the backing claim. Empty uses the
	// operator default when configured, otherwise the cluster default.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// Size is used when creating the backing claim. Empty defaults to 10Gi.
	// +optional
	Size resource.Quantity `json:"size,omitempty"`
	// AccessModes are used when creating the backing claim. Empty defaults to
	// ReadWriteOnce.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	// NodeSelector describes where pods should run when this local volume is
	// attached. AgentRuns merge these labels with their execution nodeSelector.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// ExtraEnv injects non-secret backend state paths when an AgentRun mounts
	// this volume, for example CODEX_HOME, HERMES_HOME, or OPENCLAW_STATE_DIR.
	// Credentials must still come from AgentRun envSecretRefs.
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`
	// ExternalSync declares future object-store sync for this concrete volume.
	// It can inherit defaults from ProfileRef/ProfileVolumeName and can disable
	// inherited sync with disabled=true. The v1alpha1 controller records status
	// only and does not move data.
	// +optional
	ExternalSync *ExternalVolumeSyncSpec `json:"externalSync,omitempty"`
	// Notes documents operational intent, retention, migration, or cleanup
	// expectations for this agent home.
	// +optional
	Notes string `json:"notes,omitempty"`
}

type AgentDataVolumeStatus struct {
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition         `json:"conditions,omitempty"`
	Phase              AgentDataVolumePhase       `json:"phase,omitempty"`
	ProfileRef         *NamespacedObjectReference `json:"profileRef,omitempty"`
	ProfileVolumeName  string                     `json:"profileVolumeName,omitempty"`
	ClaimRef           *NamespacedObjectReference `json:"claimRef,omitempty"`
	StorageClassName   string                     `json:"storageClassName,omitempty"`
	VolumeName         string                     `json:"volumeName,omitempty"`
	Capacity           string                     `json:"capacity,omitempty"`
	MountPath          string                     `json:"mountPath,omitempty"`
	SubPath            string                     `json:"subPath,omitempty"`
	NodeSelector       map[string]string          `json:"nodeSelector,omitempty"`
	ExtraEnv           []corev1.EnvVar            `json:"extraEnv,omitempty"`
	ExternalSync       *ExternalVolumeSyncStatus  `json:"externalSync,omitempty"`
	LastError          string                     `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentdatavolumes,scope=Namespaced,shortName=agdv
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.backend"
// +kubebuilder:printcolumn:name="Claim",type="string",JSONPath=".status.claimRef.name"
// +kubebuilder:printcolumn:name="Volume",type="string",JSONPath=".status.volumeName"
type AgentDataVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentDataVolumeSpec   `json:"spec,omitempty"`
	Status AgentDataVolumeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentDataVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentDataVolume `json:"items"`
}
