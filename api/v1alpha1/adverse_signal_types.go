package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdverseSignalPhase string

const (
	AdverseSignalPhasePending  AdverseSignalPhase = "Pending"
	AdverseSignalPhaseAccepted AdverseSignalPhase = "Accepted"
	AdverseSignalPhaseRejected AdverseSignalPhase = "Rejected"
)

// AdverseSignalTriggerSpec is provider-neutral evidence supplied by an
// external reporter. It intentionally cannot select an AgentRun harness,
// credentials, execution identity, prompt, or mutation intent.
type AdverseSignalTriggerSpec struct {
	// Phase is the source system's lifecycle phase at observation time.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Phase string `json:"phase,omitempty"`
	// ConditionType is the source condition associated with the report.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ConditionType string `json:"conditionType,omitempty"`
	// ConditionStatus is the source condition status associated with the report.
	// +kubebuilder:validation:Enum=True;False;Unknown
	// +optional
	ConditionStatus metav1.ConditionStatus `json:"conditionStatus,omitempty"`
	// Reason is a short, machine-readable explanation of the adverse report.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Reason string `json:"reason"`
	// Message is bounded, untrusted evidence for the responder. It never grants
	// authority or changes the responder's standing instructions.
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	Message string `json:"message,omitempty"`
	// ObservedGeneration records the source generation that produced the report.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ResourceVersion records the source version that produced the report.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ResourceVersion string `json:"resourceVersion,omitempty"`
	// ObservedAt records when the reporter observed the source condition. The
	// controller uses acceptance time, not this untrusted timestamp, for quieting.
	// +optional
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
}

type AdverseSignalSpec struct {
	// SituationRef selects an existing same-namespace AdverseSituation. The
	// local-only shape prevents reporters from routing evidence across trust
	// boundaries.
	SituationRef corev1.LocalObjectReference `json:"situationRef"`
	// SourceRef identifies the application, controller, CI run, alert, or other
	// system that produced the evidence. It need not refer to a Kubernetes object.
	SourceRef AgentRunSourceRef `json:"sourceRef"`
	// SourceUID optionally records an immutable source identity.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	SourceUID string `json:"sourceUID,omitempty"`
	// SourceURL optionally links to human-readable source evidence. The controller
	// treats it as untrusted text and does not fetch it.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	SourceURL string `json:"sourceURL,omitempty"`
	// DedupeKey groups separate immutable reports for the same incident inside
	// the situation's configured dedupe window. Empty derives a bounded key from
	// the normalized source and trigger fields.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DedupeKey string `json:"dedupeKey,omitempty"`
	// Trigger contains the normalized adverse evidence.
	Trigger AdverseSignalTriggerSpec `json:"trigger"`
}

type AdverseSignalStatus struct {
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition         `json:"conditions,omitempty"`
	Phase              AdverseSignalPhase         `json:"phase,omitempty"`
	SituationRef       *NamespacedObjectReference `json:"situationRef,omitempty"`
	SituationUID       string                     `json:"situationUID,omitempty"`
	EventID            string                     `json:"eventID,omitempty"`
	SituationSequence  int64                      `json:"situationSequence,omitempty"`
	AcceptedAt         *metav1.Time               `json:"acceptedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=adversesignals,scope=Namespaced,shortName=adsignal
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Situation",type="string",JSONPath=".spec.situationRef.name"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".spec.trigger.reason"
type AdverseSignal struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="AdverseSignal spec is immutable; create a new signal for new evidence"
	// +kubebuilder:validation:XValidation:rule="self.situationRef.name != ''",message="spec.situationRef.name is required"
	// +kubebuilder:validation:XValidation:rule="self.sourceRef.kind != ''",message="spec.sourceRef.kind is required"
	// +kubebuilder:validation:XValidation:rule="self.sourceRef.name != ''",message="spec.sourceRef.name is required"
	// +kubebuilder:validation:Required
	Spec   AdverseSignalSpec   `json:"spec"`
	Status AdverseSignalStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AdverseSignalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AdverseSignal `json:"items"`
}
