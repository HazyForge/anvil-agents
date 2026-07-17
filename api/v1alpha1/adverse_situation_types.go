package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdverseSituationPhase string

const (
	AdverseSituationPhasePending  AdverseSituationPhase = "Pending"
	AdverseSituationPhaseOpen     AdverseSituationPhase = "Open"
	AdverseSituationPhaseQuieting AdverseSituationPhase = "Quieting"
	AdverseSituationPhaseResolved AdverseSituationPhase = "Resolved"
)

type AdverseSituationBufferSpec struct {
	// QuietPeriodSeconds is the amount of time with no new adverse events
	// required before the situation may resolve.
	// +kubebuilder:validation:Minimum=0
	// +optional
	QuietPeriodSeconds int `json:"quietPeriodSeconds,omitempty"`
	// DedupeWindowSeconds groups repeated reports of the same source/reason
	// into one event counter.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DedupeWindowSeconds int `json:"dedupeWindowSeconds,omitempty"`
	// PullRequestQuietPeriodSeconds holds the situation open after a responder
	// reports a pull request, giving the stream time to show whether the PR
	// actually quiets the adverse events.
	// +kubebuilder:validation:Minimum=0
	// +optional
	PullRequestQuietPeriodSeconds int `json:"pullRequestQuietPeriodSeconds,omitempty"`
	// MaxEvents caps the status event ring buffer. Empty uses the controller
	// default.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxEvents int `json:"maxEvents,omitempty"`
}

type AdverseSituationAgentRunResponderSpec struct {
	// Enabled controls whether the situation should create an AgentRun
	// responder. Nil defaults to false; default adverse streams buffer events
	// until an operator supplies a runnable harness contract.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
	// ProfileRef points at a same-namespace AgentRunProfile copied onto the
	// created AgentRun. The AgentRun controller resolves the profile before it
	// creates the harness Job.
	// +optional
	ProfileRef *NamespacedObjectReference `json:"profileRef,omitempty"`
	// Prompt is copied onto the created AgentRun as its one-off operator
	// request. Use profile and harness system prompts for durable standing
	// instructions.
	// +optional
	Prompt string `json:"prompt,omitempty"`
	// Harness supplies the agent backend, image, credentials, and intent.
	// +optional
	Harness AgentRunHarnessSpec `json:"harness,omitempty"`
	// Scope is copied onto the created AgentRun so adverse responders can stay
	// bounded to a specific application, target, namespace, or operational
	// surface.
	// +optional
	Scope AgentRunScopeSpec `json:"scope,omitempty"`
	// Docs is copied onto the created AgentRun so adverse responders can keep
	// docs and runtime behavior aligned when they create durable fixes.
	// +optional
	Docs *AgentRunDocsSpec `json:"docs,omitempty"`
	// IssueTracking is copied onto the created AgentRun so adverse responders
	// can read linked GitHub tickets and optionally comment status.
	// +optional
	IssueTracking *AgentRunIssueTrackingSpec `json:"issueTracking,omitempty"`
	// Notifications are copied onto the created AgentRun.
	// +optional
	Notifications *AgentRunNotificationSpec `json:"notifications,omitempty"`
}

type AdverseSituationRespondersSpec struct {
	// AgentRun configures the AI agent responder for this stream.
	// +optional
	AgentRun *AdverseSituationAgentRunResponderSpec `json:"agentRun,omitempty"`
}

type AdverseSituationSpec struct {
	// GroupKey identifies the aggregation stream. The first slice uses one
	// default stream per namespace so duplicate and flurry failures share one
	// controller.
	// +optional
	GroupKey string `json:"groupKey,omitempty"`
	// Buffer controls duplicate suppression and quiet-time resolution.
	// +optional
	Buffer AdverseSituationBufferSpec `json:"buffer,omitempty"`
	// Responders declares reusable reactions attached to the adverse stream.
	// +optional
	Responders AdverseSituationRespondersSpec `json:"responders,omitempty"`
}

type AdverseSituationEvent struct {
	ID               string                 `json:"id,omitempty"`
	SourceRef        AgentRunSourceRef      `json:"sourceRef"`
	SourceUID        string                 `json:"sourceUID,omitempty"`
	SourceGeneration int64                  `json:"sourceGeneration,omitempty"`
	Phase            string                 `json:"phase,omitempty"`
	ConditionType    string                 `json:"conditionType,omitempty"`
	ConditionStatus  metav1.ConditionStatus `json:"conditionStatus,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	Message          string                 `json:"message,omitempty"`
	ResourceVersion  string                 `json:"resourceVersion,omitempty"`
	FirstSeenAt      *metav1.Time           `json:"firstSeenAt,omitempty"`
	LastSeenAt       *metav1.Time           `json:"lastSeenAt,omitempty"`
	Count            int32                  `json:"count,omitempty"`
}

type AdverseSituationStatus struct {
	ObservedGeneration    int64                      `json:"observedGeneration,omitempty"`
	Conditions            []metav1.Condition         `json:"conditions,omitempty"`
	Phase                 AdverseSituationPhase      `json:"phase,omitempty"`
	Sequence              int64                      `json:"sequence,omitempty"`
	Events                []AdverseSituationEvent    `json:"events,omitempty"`
	EventCount            int32                      `json:"eventCount,omitempty"`
	DuplicateCount        int32                      `json:"duplicateCount,omitempty"`
	LastEventAt           *metav1.Time               `json:"lastEventAt,omitempty"`
	QuietUntil            *metav1.Time               `json:"quietUntil,omitempty"`
	ResolvedAt            *metav1.Time               `json:"resolvedAt,omitempty"`
	PullRequestObservedAt *metav1.Time               `json:"pullRequestObservedAt,omitempty"`
	PullRequestQuietUntil *metav1.Time               `json:"pullRequestQuietUntil,omitempty"`
	ActiveResponderRef    *NamespacedObjectReference `json:"activeResponderRef,omitempty"`
	PullRequestURL        string                     `json:"pullRequestURL,omitempty"`
	Summary               string                     `json:"summary,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=adversesituations,scope=Namespaced,shortName=adverse
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.groupKey"
// +kubebuilder:printcolumn:name="Events",type="integer",JSONPath=".status.eventCount"
// +kubebuilder:printcolumn:name="Quiet Until",type="date",JSONPath=".status.quietUntil"
type AdverseSituation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AdverseSituationSpec   `json:"spec,omitempty"`
	Status AdverseSituationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AdverseSituationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AdverseSituation `json:"items"`
}
