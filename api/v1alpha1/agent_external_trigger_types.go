package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AgentExternalTriggerPhase string

const (
	AgentExternalTriggerPhasePending   AgentExternalTriggerPhase = "Pending"
	AgentExternalTriggerPhaseReady     AgentExternalTriggerPhase = "Ready"
	AgentExternalTriggerPhaseSuspended AgentExternalTriggerPhase = "Suspended"
	AgentExternalTriggerPhaseBlocked   AgentExternalTriggerPhase = "Blocked"
)

// AgentExternalTriggerSourceKind identifies the inbound event source family.
type AgentExternalTriggerSourceKind string

const (
	AgentExternalTriggerSourceGitHubWebhook AgentExternalTriggerSourceKind = "githubWebhook"
)

// AgentExternalTriggerConcurrencyPolicy mirrors AgentSchedule concurrency
// semantics for inbound deliveries.
type AgentExternalTriggerConcurrencyPolicy string

const (
	AgentExternalTriggerConcurrencyForbid AgentExternalTriggerConcurrencyPolicy = "Forbid"
	AgentExternalTriggerConcurrencyAllow  AgentExternalTriggerConcurrencyPolicy = "Allow"
)

// AgentExternalTriggerTargetKind is a same-namespace delivery target.
type AgentExternalTriggerTargetKind string

const (
	AgentExternalTriggerTargetAgentRunProfile AgentExternalTriggerTargetKind = "AgentRunProfile"
	AgentExternalTriggerTargetAgentCouncil    AgentExternalTriggerTargetKind = "AgentCouncil"
	AgentExternalTriggerTargetAgentRun        AgentExternalTriggerTargetKind = "AgentRun"
)

// AgentExternalTriggerCouncilDelivery selects which council members receive a
// delivery. Empty defaults to allMembers.
type AgentExternalTriggerCouncilDelivery string

const (
	AgentExternalTriggerCouncilDeliveryAllMembers AgentExternalTriggerCouncilDelivery = "allMembers"
)

// AgentExternalTriggerSecretRef names the Secret that holds the GitHub HMAC
// webhook secret and an optional path token. Controllers and the API read
// Secret values only in-process; status, logs, and console responses never
// include Secret bytes.
type AgentExternalTriggerSecretRef struct {
	// Name is the same-namespace Secret that holds webhook credentials.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// WebhookSecretKey is the Secret data key for the GitHub HMAC shared secret.
	// Empty defaults to webhookSecret.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	WebhookSecretKey string `json:"webhookSecretKey,omitempty"`
	// PathTokenKey optionally names a Secret data key whose value must match the
	// X-Anvil-Trigger-Token request header. Empty disables the extra path token.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	PathTokenKey string `json:"pathTokenKey,omitempty"`
}

// AgentExternalTriggerGitHubSpec configures a GitHub webhook source.
type AgentExternalTriggerGitHubSpec struct {
	// Repositories is the owner/name allowlist. At least one entry is required.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Repositories []string `json:"repositories"`
	// Events optionally allowlists GitHub event types such as push,
	// pull_request, issues, and issue_comment. Empty accepts any signed event
	// that reaches the receiver.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Events []string `json:"events,omitempty"`
}

// AgentExternalTriggerSourceSpec selects the inbound event source.
type AgentExternalTriggerSourceSpec struct {
	// Kind identifies the source family. Only githubWebhook is supported in
	// this API version.
	// +kubebuilder:validation:Enum=githubWebhook
	Kind AgentExternalTriggerSourceKind `json:"kind"`
	// GitHub configures the GitHub webhook allowlist when kind is githubWebhook.
	// +optional
	GitHub *AgentExternalTriggerGitHubSpec `json:"github,omitempty"`
}

// AgentExternalTriggerTargetSpec names one same-namespace delivery target.
type AgentExternalTriggerTargetSpec struct {
	// Kind selects the target object family.
	// +kubebuilder:validation:Enum=AgentRunProfile;AgentCouncil;AgentRun
	Kind AgentExternalTriggerTargetKind `json:"kind"`
	// Name is the same-namespace object name. Cross-namespace refs are rejected.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// CouncilDelivery selects council members when kind is AgentCouncil.
	// Empty or allMembers delivers to every member profile. Any other non-empty
	// value is treated as a coordinatorRole that must match a member role.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	CouncilDelivery string `json:"councilDelivery,omitempty"`
}

// AgentExternalTriggerHTTPRouteSpec optionally customizes the controller-owned
// Gateway API HTTPRoute that exposes this webhook. Empty uses the operator
// install hostnames from api.httpRoute / api.externalTriggerHTTPRoute.
type AgentExternalTriggerHTTPRouteSpec struct {
	// Hostname optionally replaces the install-default HTTPRoute hostnames for
	// this trigger. Must be an exact hostname with no wildcards.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`
	Hostname string `json:"hostname,omitempty"`
}

// AgentExternalTriggerHTTPRouteStatus is the observed public Gateway API route
// for this trigger. It never includes Secret material.
type AgentExternalTriggerHTTPRouteStatus struct {
	// Name is the same-namespace HTTPRoute object owned by this trigger.
	// +optional
	Name string `json:"name,omitempty"`
	// PublicURL is https://{hostname}{webhookPath} with no secret material.
	// +optional
	PublicURL string `json:"publicURL,omitempty"`
	// Accepted is the Gateway API HTTPRoute parent Accepted condition when observed.
	// +optional
	Accepted *bool `json:"accepted,omitempty"`
	// Programmed is the Gateway API Programmed condition when a parent reports it.
	// When Programmed is absent, Accepted and ResolvedRefs together are treated
	// as programmed.
	// +optional
	Programmed *bool `json:"programmed,omitempty"`
}

// AgentExternalTriggerSpec declares an inbound external event receiver that can
// create or annotate AgentRuns the same way a schedule creates work.
type AgentExternalTriggerSpec struct {
	// Suspend prevents new deliveries from creating or annotating AgentRuns
	// and detaches the controller-owned public HTTPRoute. receiverID is kept.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
	// Source configures the inbound event family and allowlists.
	Source AgentExternalTriggerSourceSpec `json:"source"`
	// SecretRef names the Secret that holds the HMAC webhook secret.
	SecretRef AgentExternalTriggerSecretRef `json:"secretRef"`
	// Targets lists one or more same-namespace delivery destinations.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Targets []AgentExternalTriggerTargetSpec `json:"targets"`
	// HTTPRoute optionally overrides the install-default public hostname for
	// the controller-owned Gateway API HTTPRoute.
	// +optional
	HTTPRoute *AgentExternalTriggerHTTPRouteSpec `json:"httpRoute,omitempty"`
	// PromptTemplate optionally renders into created AgentRun prompts. Supported
	// placeholders use double-brace names eventType, repository, deliveryID, and
	// summary (documented in docs/external-triggers.md). Avoid raw template
	// delimiters in this comment so Helm-rendered CRDs stay inert.
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	PromptTemplate string `json:"promptTemplate,omitempty"`
	// ConcurrencyPolicy controls whether a new delivery may create runs while
	// prior trigger-created runs are still active. Empty defaults to Forbid.
	// +kubebuilder:validation:Enum=Forbid;Allow
	// +optional
	ConcurrencyPolicy AgentExternalTriggerConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`
	// MaxDeliveriesPerDay caps accepted deliveries for this trigger during a
	// UTC calendar day. Empty disables the cap. Idempotent retries of a seen
	// delivery ID do not consume an additional slot.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxDeliveriesPerDay int `json:"maxDeliveriesPerDay,omitempty"`
}

// AgentExternalTriggerStatus is the observed state of an inbound receiver.
type AgentExternalTriggerStatus struct {
	ObservedGeneration int64                     `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition        `json:"conditions,omitempty"`
	Phase              AgentExternalTriggerPhase `json:"phase,omitempty"`
	// ReceiverID is an opaque public identifier embedded in the webhook URL.
	// It is not a secret.
	// +optional
	ReceiverID string `json:"receiverID,omitempty"`
	// WebhookPath is the path-only receiver URL (no host, no secret material).
	// +optional
	WebhookPath string `json:"webhookPath,omitempty"`
	// LastDeliveryID is the most recently accepted GitHub X-GitHub-Delivery.
	// +optional
	LastDeliveryID string `json:"lastDeliveryID,omitempty"`
	// LastDeliveryAt is when the last delivery was accepted.
	// +optional
	LastDeliveryAt *metav1.Time `json:"lastDeliveryAt,omitempty"`
	// LastEventType is the GitHub X-GitHub-Event of the last accepted delivery.
	// +optional
	LastEventType string `json:"lastEventType,omitempty"`
	// LastError records the most recent non-idempotent delivery failure.
	// +optional
	LastError string `json:"lastError,omitempty"`
	// LastRunRefs lists AgentRuns created or updated by the last delivery.
	// +optional
	LastRunRefs []NamespacedObjectReference `json:"lastRunRefs,omitempty"`
	// DeliveryCount is the number of uniquely accepted deliveries.
	// +optional
	DeliveryCount int64 `json:"deliveryCount,omitempty"`
	// SeenDeliveryIDs is a bounded ring of recently accepted GitHub delivery
	// IDs used for retry idempotency.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	SeenDeliveryIDs []string `json:"seenDeliveryIDs,omitempty"`
	// DeliveriesToday counts uniquely accepted deliveries for the UTC day in
	// DeliveriesTodayDate.
	// +optional
	DeliveriesToday int `json:"deliveriesToday,omitempty"`
	// DeliveriesTodayDate is the UTC calendar day (YYYY-MM-DD) for DeliveriesToday.
	// +optional
	DeliveriesTodayDate string `json:"deliveriesTodayDate,omitempty"`
	// HTTPRoute is the controller-owned Gateway API HTTPRoute that exposes this
	// webhook. Cleared when the trigger is suspended, blocked, or deleted.
	// +optional
	HTTPRoute *AgentExternalTriggerHTTPRouteStatus `json:"httpRoute,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentexternaltriggers,scope=Namespaced,shortName=agtrig
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Receiver",type="string",JSONPath=".status.receiverID"
// +kubebuilder:printcolumn:name="HTTPRoute",type="string",JSONPath=".status.httpRoute.name"
// +kubebuilder:printcolumn:name="Last Event",type="string",JSONPath=".status.lastEventType"
// +kubebuilder:printcolumn:name="Last Delivery",type="string",JSONPath=".status.lastDeliveryAt"
type AgentExternalTrigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentExternalTriggerSpec   `json:"spec,omitempty"`
	Status AgentExternalTriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentExternalTriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentExternalTrigger `json:"items"`
}

// DefaultWebhookSecretKey is used when secretRef.webhookSecretKey is empty.
const DefaultWebhookSecretKey = "webhookSecret"

// AgentExternalTriggerDeliveryAnnotation records the latest accepted external
// delivery on a non-terminal AgentRun (spec is immutable).
const AgentExternalTriggerDeliveryAnnotation = "control.anvil.hazyforge.io/external-trigger-delivery"

// AgentExternalTriggerLabel marks AgentRuns created by an AgentExternalTrigger.
const AgentExternalTriggerLabel = "control.anvil.hazyforge.io/agent-external-trigger"

// AgentExternalTriggerDeliveryIDLabel stores a truncated delivery id for listing.
const AgentExternalTriggerDeliveryIDLabel = "control.anvil.hazyforge.io/github-delivery"
