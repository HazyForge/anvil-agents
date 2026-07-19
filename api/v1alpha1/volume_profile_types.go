package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type VolumeProfilePhase string

const (
	VolumeProfilePhaseReady   VolumeProfilePhase = "Ready"
	VolumeProfilePhaseBlocked VolumeProfilePhase = "Blocked"
)

type ExternalVolumeSyncProvider string

const (
	ExternalVolumeSyncProviderS3 ExternalVolumeSyncProvider = "s3"
)

type ExternalVolumeSyncDirection string

const (
	ExternalVolumeSyncDirectionSeedOnly      ExternalVolumeSyncDirection = "SeedOnly"
	ExternalVolumeSyncDirectionSyncBack      ExternalVolumeSyncDirection = "SyncBack"
	ExternalVolumeSyncDirectionBidirectional ExternalVolumeSyncDirection = "Bidirectional"
)

type ExternalVolumeSyncPhase string

const (
	ExternalVolumeSyncPhaseDisabled ExternalVolumeSyncPhase = "Disabled"
	ExternalVolumeSyncPhaseStubOnly ExternalVolumeSyncPhase = "StubOnly"
)

// ExternalVolumeSyncSpec declares future object-store synchronization for a
// volume. The v1alpha1 API records intent only; the operator does not move data
// until a later controller slice implements a sync executor.
type ExternalVolumeSyncSpec struct {
	// Disabled lets a concrete volume explicitly turn off sync inherited from a
	// reusable profile.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
	// Provider identifies the external sync backend.
	// +kubebuilder:validation:Enum=s3
	// +optional
	Provider ExternalVolumeSyncProvider `json:"provider,omitempty"`
	// Direction documents the intended data-flow direction.
	// +kubebuilder:validation:Enum=SeedOnly;SyncBack;Bidirectional
	// +optional
	Direction ExternalVolumeSyncDirection `json:"direction,omitempty"`
	// SeedOnCreate requests initial volume population from the external source.
	// It is recorded as intent only in this API slice.
	// +optional
	SeedOnCreate bool `json:"seedOnCreate,omitempty"`
	// SyncBack requests copying local volume changes back to the external
	// source. It is recorded as intent only in this API slice.
	// +optional
	SyncBack bool `json:"syncBack,omitempty"`
	// Schedule is an implementation hint for future periodic sync workers, such
	// as a cron expression or interval string. It is not executed today.
	// +optional
	Schedule string `json:"schedule,omitempty"`
	// CredentialsSecretRef references a namespace-local Secret containing
	// provider credentials. The operator does not read the Secret in this slice.
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
	// S3 describes the object-store location when provider=s3.
	// +optional
	S3 *ExternalVolumeSyncS3Spec `json:"s3,omitempty"`
	// Notes documents migration, retention, or operator expectations.
	// +optional
	Notes string `json:"notes,omitempty"`
}

type ExternalVolumeSyncS3Spec struct {
	// Bucket is the target S3-compatible bucket name.
	// +optional
	Bucket string `json:"bucket,omitempty"`
	// Prefix is the object key prefix within the bucket.
	// +optional
	Prefix string `json:"prefix,omitempty"`
	// Region is the provider region hint.
	// +optional
	Region string `json:"region,omitempty"`
	// Endpoint optionally selects an S3-compatible endpoint.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
}

type ExternalVolumeSyncStatus struct {
	Phase ExternalVolumeSyncPhase `json:"phase,omitempty"`
	// +optional
	Provider ExternalVolumeSyncProvider `json:"provider,omitempty"`
	// +optional
	Direction ExternalVolumeSyncDirection `json:"direction,omitempty"`
	// +optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`
	// +optional
	LastError string `json:"lastError,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type VolumeProfileSpec struct {
	// ApplicationRef optionally scopes this profile to one opaque application key.
	// +optional
	ApplicationRef *ApplicationReferenceSpec `json:"applicationRef,omitempty"`
	// Description documents the reusable storage shape in human terms.
	// +optional
	Description string `json:"description,omitempty"`
	// CapacityPolicy records placement and sizing intent. It does not replace
	// concrete PVC storage requests.
	// +optional
	CapacityPolicy *VolumeProfileCapacityPolicySpec `json:"capacityPolicy,omitempty"`
	// Volumes are named reusable volume entries.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Volumes []VolumeProfileVolumeSpec `json:"volumes"`
	// Notes documents operational intent, migration, or cleanup expectations.
	// +optional
	Notes string `json:"notes,omitempty"`
}

type VolumeProfileCapacityPolicySpec struct {
	// MaxNodeAllocatableEphemeralStoragePercent is an operator guardrail for
	// local-path-backed grouped PVCs. Kubernetes PVCs still receive concrete
	// storage requests.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=90
	// +optional
	MaxNodeAllocatableEphemeralStoragePercent *int32 `json:"maxNodeAllocatableEphemeralStoragePercent,omitempty"`
}

type VolumeProfileVolumeSpec struct {
	// Name is the stable profile entry name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Purpose describes how this volume should be used, such as cargo-cache,
	// target-cache, workspace, or agent-home.
	// +optional
	Purpose string `json:"purpose,omitempty"`
	// MountPath is the default container path for consumers that inherit this
	// volume profile entry.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// SubPath optionally mounts only one subdirectory of the claim by default.
	// +optional
	SubPath string `json:"subPath,omitempty"`
	// StorageClassName is the default StorageClass for concrete PVCs.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// Size records concrete and advisory sizing intent.
	// +optional
	Size VolumeProfileVolumeSizeSpec `json:"size,omitempty"`
	// AccessModes are the default PVC access modes.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	// NodeSelector describes required placement for local-path or host-local
	// storage.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// ExtraEnv provides non-secret path environment defaults for AgentRuns that
	// attach an AgentDataVolume inheriting this profile entry.
	// +optional
	ExtraEnv []AgentDataVolumePathEnvVar `json:"extraEnv,omitempty"`
	// ExternalSync declares reusable sync intent for this volume entry. Concrete
	// volumes may inherit, override, or disable it.
	// +optional
	ExternalSync *ExternalVolumeSyncSpec `json:"externalSync,omitempty"`
	// Notes documents operational intent, migration, or cleanup expectations.
	// +optional
	Notes string `json:"notes,omitempty"`
}

type VolumeProfileVolumeSizeSpec struct {
	// Request is the concrete PVC storage request to apply when this entry is
	// used as a default.
	// +optional
	Request resource.Quantity `json:"request,omitempty"`
	// MaxNodeAllocatableEphemeralStoragePercent is an advisory per-entry
	// percentage guardrail. It is not directly applied to Kubernetes PVCs.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=90
	// +optional
	MaxNodeAllocatableEphemeralStoragePercent *int32 `json:"maxNodeAllocatableEphemeralStoragePercent,omitempty"`
}

type VolumeProfileStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              VolumeProfilePhase `json:"phase,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// VolumeCount is the number of profile entries observed by the controller.
	// +optional
	VolumeCount int32 `json:"volumeCount,omitempty"`
	// TotalRequestedStorage is the sum of concrete spec.volumes[].size.request
	// values.
	// +optional
	TotalRequestedStorage string `json:"totalRequestedStorage,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=name
	Volumes []VolumeProfileVolumeStatus `json:"volumes,omitempty"`
	// LastError summarizes the current blocking validation error, when any.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

type VolumeProfileVolumeStatus struct {
	Name string `json:"name"`
	// +optional
	Purpose string `json:"purpose,omitempty"`
	// +optional
	RequestedStorage string `json:"requestedStorage,omitempty"`
	// +optional
	ExternalSync *ExternalVolumeSyncStatus `json:"externalSync,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=volumeprofiles,scope=Namespaced,shortName=volprof
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Volumes",type="integer",JSONPath=".status.volumeCount"
// +kubebuilder:printcolumn:name="Requested",type="string",JSONPath=".status.totalRequestedStorage"
type VolumeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VolumeProfileSpec   `json:"spec,omitempty"`
	Status VolumeProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type VolumeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VolumeProfile `json:"items"`
}
