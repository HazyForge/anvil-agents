package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentSchedulePhase string

const (
	AgentSchedulePhasePending   AgentSchedulePhase = "Pending"
	AgentSchedulePhaseScheduled AgentSchedulePhase = "Scheduled"
	AgentSchedulePhaseRunning   AgentSchedulePhase = "Running"
	AgentSchedulePhaseSuspended AgentSchedulePhase = "Suspended"
	AgentSchedulePhaseBlocked   AgentSchedulePhase = "Blocked"
)

type AgentScheduleConcurrencyPolicy string

const (
	AgentScheduleConcurrencyForbid AgentScheduleConcurrencyPolicy = "Forbid"
	AgentScheduleConcurrencyAllow  AgentScheduleConcurrencyPolicy = "Allow"
	AgentScheduleConcurrencyQueue  AgentScheduleConcurrencyPolicy = "Queue"
)

const (
	// AgentScheduleRunNowAnnotation is a replay-safe manual nudge. Change the
	// annotation value to a new token to request one immediate AgentRun.
	AgentScheduleRunNowAnnotation = "control.anvil.hazyforge.io/run-now"
	// AgentScheduleRunTemplateAnnotation optionally selects a named
	// spec.runTemplates entry for the next manual nudge.
	AgentScheduleRunTemplateAnnotation = "control.anvil.hazyforge.io/run-template"
)

type AgentScheduleTemplateSelectionPolicy string

const (
	AgentScheduleTemplateSelectionSequential AgentScheduleTemplateSelectionPolicy = "Sequential"
)

type AgentScheduleRunTemplateSpec struct {
	// Name is a stable selector for manual nudges and status.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Template is copied into each AgentRun created from this template.
	Template AgentRunSpec `json:"template"`
}

type AgentScheduleBackoffSpec struct {
	// FailedSeconds delays automatic interval runs after the newest run fails.
	// +kubebuilder:validation:Minimum=0
	// +optional
	FailedSeconds int `json:"failedSeconds,omitempty"`
	// NeedsHumanSeconds delays automatic interval runs after the newest run
	// requires human action.
	// +kubebuilder:validation:Minimum=0
	// +optional
	NeedsHumanSeconds int `json:"needsHumanSeconds,omitempty"`
}

type AgentScheduleSpec struct {
	// ApplicationRef explicitly identifies the opaque workload or product owned
	// by every run template in this schedule. The controller copies it into a
	// child AgentRun scope when the selected template does not set one. Legacy
	// schedules may omit this and resolve the application through template scope
	// or AgentRunProfile scope.
	// +optional
	ApplicationRef *ApplicationReferenceSpec `json:"applicationRef,omitempty"`
	// Suspend prevents new AgentRuns from being created while retaining status.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
	// IntervalSeconds is the period between successful schedule ticks. The
	// first run is due immediately unless InitialDelaySeconds is set.
	// +kubebuilder:validation:Minimum=1
	IntervalSeconds int `json:"intervalSeconds"`
	// InitialDelaySeconds delays the first run after creation.
	// +kubebuilder:validation:Minimum=0
	// +optional
	InitialDelaySeconds int `json:"initialDelaySeconds,omitempty"`
	// Backoff delays automatic interval runs after the newest scheduled run
	// reaches a configured terminal phase. Manual run-now nudges bypass it.
	// +optional
	Backoff *AgentScheduleBackoffSpec `json:"backoff,omitempty"`
	// ConcurrencyPolicy controls whether a new interval tick may create a run
	// while the prior run is still active. Queue creates every due run but only
	// lets the oldest non-terminal run for the schedule launch a Job. Empty
	// defaults to Forbid.
	// +kubebuilder:validation:Enum=Forbid;Allow;Queue
	// +optional
	ConcurrencyPolicy AgentScheduleConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`
	// MaxConcurrentRuns caps concurrently non-terminal child AgentRuns for
	// Allow and the number of queued AgentRuns that may launch Jobs for Queue.
	// Empty defaults to 1 for Queue and unlimited for Allow. Forbid always
	// behaves as a one-at-a-time schedule.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxConcurrentRuns int `json:"maxConcurrentRuns,omitempty"`
	// RunTemplate is copied into each scheduled AgentRun. The controller fills
	// purpose, sourceRef, trigger, and scheduleRef defaults when they are empty.
	// +optional
	RunTemplate AgentRunSpec `json:"runTemplate,omitempty"`
	// RunTemplates is an ordered set of named templates. When set, interval
	// runs rotate through the list and manual runs may select one by setting
	// the control.anvil.hazyforge.io/run-template annotation.
	// +optional
	RunTemplates []AgentScheduleRunTemplateSpec `json:"runTemplates,omitempty"`
	// TemplateSelectionPolicy controls interval selection when runTemplates is
	// set. Empty defaults to Sequential.
	// +kubebuilder:validation:Enum=Sequential
	// +optional
	TemplateSelectionPolicy AgentScheduleTemplateSelectionPolicy `json:"templateSelectionPolicy,omitempty"`
}

type AgentScheduleStatus struct {
	ObservedGeneration int64                      `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition         `json:"conditions,omitempty"`
	Phase              AgentSchedulePhase         `json:"phase,omitempty"`
	LastRunAt          *metav1.Time               `json:"lastRunAt,omitempty"`
	NextRunAt          *metav1.Time               `json:"nextRunAt,omitempty"`
	ActiveRunRef       *NamespacedObjectReference `json:"activeRunRef,omitempty"`
	ActiveRunCount     int                        `json:"activeRunCount,omitempty"`
	LastRunRef         *NamespacedObjectReference `json:"lastRunRef,omitempty"`
	LastRunPhase       AgentRunPhase              `json:"lastRunPhase,omitempty"`
	LastRunTemplate    string                     `json:"lastRunTemplate,omitempty"`
	LastManualRunToken string                     `json:"lastManualRunToken,omitempty"`
	LastError          string                     `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentschedules,scope=Namespaced,shortName=agsched
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Interval",type="integer",JSONPath=".spec.intervalSeconds"
// +kubebuilder:printcolumn:name="Next Run",type="string",JSONPath=".status.nextRunAt"
// +kubebuilder:printcolumn:name="Active Run",type="string",JSONPath=".status.activeRunRef.name"
type AgentSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentScheduleSpec   `json:"spec,omitempty"`
	Status AgentScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSchedule `json:"items"`
}
