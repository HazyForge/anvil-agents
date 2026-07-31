package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentDataVolumeCopyPhase is the controller-reported lifecycle of one volume copy.
// +kubebuilder:validation:Enum=Pending;WaitingForIdle;PreparingDestination;Streaming;Verifying;Succeeded;Failed
type AgentDataVolumeCopyPhase string

const (
	AgentDataVolumeCopyPhasePending              AgentDataVolumeCopyPhase = "Pending"
	AgentDataVolumeCopyPhaseWaitingForIdle       AgentDataVolumeCopyPhase = "WaitingForIdle"
	AgentDataVolumeCopyPhasePreparingDestination AgentDataVolumeCopyPhase = "PreparingDestination"
	AgentDataVolumeCopyPhaseStreaming            AgentDataVolumeCopyPhase = "Streaming"
	AgentDataVolumeCopyPhaseVerifying            AgentDataVolumeCopyPhase = "Verifying"
	AgentDataVolumeCopyPhaseSucceeded            AgentDataVolumeCopyPhase = "Succeeded"
	AgentDataVolumeCopyPhaseFailed               AgentDataVolumeCopyPhase = "Failed"
)

// AgentDataVolumeCopyMethod selects how bytes move between claims.
// Stream copies across nodes by tar+TCP between a source Job and destination Job.
// SameNode mounts both claims in one Job when they share a node (rare for local-path).
// +kubebuilder:validation:Enum=Stream
type AgentDataVolumeCopyMethod string

const (
	AgentDataVolumeCopyMethodStream AgentDataVolumeCopyMethod = "Stream"
)

// AgentDataVolumeCopyDestination describes the target AgentDataVolume identity and
// placement. claimName is never reused from the source: a new claim is always used.
type AgentDataVolumeCopyDestination struct {
	// Name is the same-namespace destination AgentDataVolume. Created when missing.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// NodeSelector pins destination pods and local-path first-consumer binding.
	// Required for Stream copies so WaitForFirstConsumer binds on the intended node.
	// +kubebuilder:validation:MinProperties=1
	NodeSelector map[string]string `json:"nodeSelector"`
	// StorageClassName overrides the source when creating the destination claim.
	// Empty inherits the source storage class.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// Size overrides the source size when creating the destination claim.
	// Empty inherits the source size (or status capacity when size is unset).
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`
	// Backend overrides the destination backend label. Empty inherits the source.
	// +kubebuilder:validation:Enum=codex;openCode;hermesAgent;openClaw;grokBuild;piAgent;custom
	// +optional
	Backend AgentRunHarnessBackendKind `json:"backend,omitempty"`
	// Notes are stored on the destination AgentDataVolume when the controller creates it.
	// +optional
	Notes string `json:"notes,omitempty"`
}

// AgentDataVolumeCopySpec is the immutable intent for one volume copy operation.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="AgentDataVolumeCopy spec is immutable; create a new copy instead"
// +kubebuilder:validation:XValidation:rule="self.sourceRef.name != self.destination.name",message="source and destination AgentDataVolume names must differ"
type AgentDataVolumeCopySpec struct {
	// SourceRef names the same-namespace AgentDataVolume to copy from.
	SourceRef corev1.LocalObjectReference `json:"sourceRef"`
	// Destination describes the target AgentDataVolume and node placement.
	Destination AgentDataVolumeCopyDestination `json:"destination"`
	// Method selects the transfer strategy. Defaults to Stream.
	// +optional
	// +kubebuilder:default=Stream
	Method AgentDataVolumeCopyMethod `json:"method,omitempty"`
	// AllowNonEmptyDestination permits overwriting a destination that already has files.
	// Default false: destination root must be empty (or only lost+found).
	// +optional
	AllowNonEmptyDestination bool `json:"allowNonEmptyDestination,omitempty"`
	// Verify runs a post-copy file-count check between source and destination.
	// Defaults to true.
	// +optional
	// +kubebuilder:default=true
	Verify *bool `json:"verify,omitempty"`
	// TimeoutSeconds bounds the combined transfer Jobs. Defaults to 1800.
	// +optional
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=14400
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
}

// AgentDataVolumeCopyStatus records progress and terminal outcome.
type AgentDataVolumeCopyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// +kubebuilder:validation:Enum=Pending;WaitingForIdle;PreparingDestination;Streaming;Verifying;Succeeded;Failed
	Phase AgentDataVolumeCopyPhase `json:"phase,omitempty"`

	SourceRef      *NamespacedObjectReference `json:"sourceRef,omitempty"`
	SourceUID      string                     `json:"sourceUID,omitempty"`
	SourceClaimRef *NamespacedObjectReference `json:"sourceClaimRef,omitempty"`
	SourceClaimUID string                     `json:"sourceClaimUID,omitempty"`
	SourceNode     string                     `json:"sourceNode,omitempty"`

	DestinationRef      *NamespacedObjectReference `json:"destinationRef,omitempty"`
	DestinationUID      string                     `json:"destinationUID,omitempty"`
	DestinationClaimRef *NamespacedObjectReference `json:"destinationClaimRef,omitempty"`
	DestinationClaimUID string                     `json:"destinationClaimUID,omitempty"`
	DestinationNode     string                     `json:"destinationNode,omitempty"`

	SourceJobRef      *NamespacedObjectReference `json:"sourceJobRef,omitempty"`
	SourceJobUID      string                     `json:"sourceJobUID,omitempty"`
	DestinationJobRef *NamespacedObjectReference `json:"destinationJobRef,omitempty"`
	DestinationJobUID string                     `json:"destinationJobUID,omitempty"`
	ServiceRef        *NamespacedObjectReference `json:"serviceRef,omitempty"`

	ActiveConsumerRuns []string     `json:"activeConsumerRuns,omitempty"`
	BytesCopiedHint    string       `json:"bytesCopiedHint,omitempty"`
	FileCountSource    *int64       `json:"fileCountSource,omitempty"`
	FileCountDest      *int64       `json:"fileCountDest,omitempty"`
	StartedAt          *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time `json:"completedAt,omitempty"`
	LastError          string       `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentdatavolumecopies,scope=Namespaced,shortName=agdvcopy
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.sourceRef.name"
// +kubebuilder:printcolumn:name="Destination",type="string",JSONPath=".spec.destination.name"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentDataVolumeCopy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentDataVolumeCopySpec   `json:"spec,omitempty"`
	Status AgentDataVolumeCopyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentDataVolumeCopyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentDataVolumeCopy `json:"items"`
}

// AgentDataVolumeCopyIsTerminal reports whether the copy has finished.
func AgentDataVolumeCopyIsTerminal(phase AgentDataVolumeCopyPhase) bool {
	switch phase {
	case AgentDataVolumeCopyPhaseSucceeded, AgentDataVolumeCopyPhaseFailed:
		return true
	default:
		return false
	}
}

// AgentDataVolumeCopyVerifyEnabled returns whether post-copy verification is requested.
func AgentDataVolumeCopyVerifyEnabled(spec AgentDataVolumeCopySpec) bool {
	if spec.Verify == nil {
		return true
	}
	return *spec.Verify
}
