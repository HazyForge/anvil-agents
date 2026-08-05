package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type AgentRunPhase string

const (
	AgentRunPhasePending    AgentRunPhase = "Pending"
	AgentRunPhaseRunning    AgentRunPhase = "Running"
	AgentRunPhaseNeedsHuman AgentRunPhase = "NeedsHuman"
	AgentRunPhaseSucceeded  AgentRunPhase = "Succeeded"
	AgentRunPhaseFailed     AgentRunPhase = "Failed"
)

type AgentRunPurpose string

const (
	AgentRunPurposeManual               AgentRunPurpose = "manual"
	AgentRunPurposeAdverseSituation     AgentRunPurpose = "adverseSituation"
	AgentRunPurposeScheduledHealthCheck AgentRunPurpose = "scheduledHealthCheck"
)

type AgentRunIntent string

const (
	AgentRunIntentObserve       AgentRunIntent = "observe"
	AgentRunIntentFixTransient  AgentRunIntent = "fixTransient"
	AgentRunIntentProposeChange AgentRunIntent = "proposeChange"
	AgentRunIntentCleanup       AgentRunIntent = "cleanup"
)

type AgentRunHarnessBackendKind string

const (
	AgentRunHarnessBackendCodex       AgentRunHarnessBackendKind = "codex"
	AgentRunHarnessBackendOpenCode    AgentRunHarnessBackendKind = "openCode"
	AgentRunHarnessBackendHermesAgent AgentRunHarnessBackendKind = "hermesAgent"
	AgentRunHarnessBackendOpenClaw    AgentRunHarnessBackendKind = "openClaw"
	AgentRunHarnessBackendGrokBuild   AgentRunHarnessBackendKind = "grokBuild"
	AgentRunHarnessBackendPiAgent     AgentRunHarnessBackendKind = "piAgent"
	AgentRunHarnessBackendCustom      AgentRunHarnessBackendKind = "custom"
)

type AgentRunModelProvider string

const (
	AgentRunModelProviderOpenAICodex AgentRunModelProvider = "openai-codex"
	AgentRunModelProviderOpenAI      AgentRunModelProvider = "openai"
	AgentRunModelProviderXAI         AgentRunModelProvider = "xai"
	AgentRunModelProviderDeepSeek    AgentRunModelProvider = "deepseek"
)

type AgentRunProviderAuthMode string

const (
	AgentRunProviderAuthModeAPIKey AgentRunProviderAuthMode = "apiKey"
	AgentRunProviderAuthModeOAuth  AgentRunProviderAuthMode = "oauth"
)

type AgentRunSourceRef struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

type AgentRunTriggerSnapshot struct {
	Phase              string                 `json:"phase,omitempty"`
	ConditionType      string                 `json:"conditionType,omitempty"`
	ConditionStatus    metav1.ConditionStatus `json:"conditionStatus,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
	ObservedGeneration int64                  `json:"observedGeneration,omitempty"`
	ResourceVersion    string                 `json:"resourceVersion,omitempty"`
	DetectedAt         *metav1.Time           `json:"detectedAt,omitempty"`
}

type AgentRunScopeSpec struct {
	// Summary describes the area the agent should inspect, such as a cluster,
	// namespace, product surface, or operational concern.
	// +optional
	Summary string `json:"summary,omitempty"`
	// ApplicationRef identifies the workload or product that owns this run. It
	// is opaque metadata and does not require an Application CRD.
	// +optional
	ApplicationRef *ApplicationReferenceSpec `json:"applicationRef,omitempty"`
	// ApplicationTargetRef identifies the target or environment this run should
	// inspect. It is opaque metadata and does not require another CRD.
	// +optional
	ApplicationTargetRef *ApplicationTargetReferenceSpec `json:"applicationTargetRef,omitempty"`
	// Namespaces optionally limits Kubernetes inspection to known namespaces.
	// Empty means the selected service account and prompt define the boundary.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`
	// ResourceKinds optionally calls out important resource kinds to inspect.
	// +optional
	ResourceKinds []string `json:"resourceKinds,omitempty"`
	// Repository scopes git checkout and branch policy for this run. When set,
	// the controller injects ANVIL_AGENT_RUN_REPOSITORY* environment variables
	// used by runner images to clone and check out work. Branch fields restrict
	// which refs the agent may treat as workspace heads and PR bases.
	// +optional
	Repository *AgentRunRepositorySpec `json:"repository,omitempty"`
}

// AgentRunRepositorySpec describes the git repository and branch policy for a
// run. DestinationBranch is the integration/PR base. AllowedBranches limits
// which branches the agent may check out, analyze as heads, or push to.
// Empty AllowedBranches with DestinationBranch set means only that branch is
// in scope. Empty AllowedBranches and empty DestinationBranch preserves legacy
// unrestricted ref behavior via Ref or harness extraEnv.
type AgentRunRepositorySpec struct {
	// Name is the owner/name repository identity, for example HazyForge/hazy-trade.
	// +optional
	Name string `json:"name,omitempty"`
	// URL is the clone URL. Empty defaults to https://github.com/<name>.git when
	// Name is set.
	// +optional
	URL string `json:"url,omitempty"`
	// Ref is the workspace checkout ref (branch, tag, or commit). Empty defaults
	// to DestinationBranch when that field is set.
	// +optional
	Ref string `json:"ref,omitempty"`
	// DestinationBranch is the only allowed pull-request base / integration
	// branch for this agent. Agents must open PRs targeting this branch when set.
	// +optional
	DestinationBranch string `json:"destinationBranch,omitempty"`
	// AllowedBranches restricts which remote branches the agent may check out,
	// analyze as heads, or push feature work from. Empty means only
	// DestinationBranch (when set) or unrestricted (legacy).
	// +optional
	// +listType=set
	AllowedBranches []string `json:"allowedBranches,omitempty"`
}

type AgentRunDocsPolicy string

const (
	AgentRunDocsPolicyDisabled AgentRunDocsPolicy = "Disabled"
	AgentRunDocsPolicyReview   AgentRunDocsPolicy = "Review"
	AgentRunDocsPolicyRequired AgentRunDocsPolicy = "Required"
)

type AgentRunDocsSpec struct {
	// Policy controls how strongly the run must check that docs and runtime
	// match. Empty defaults to Review in the generated prompt.
	// +kubebuilder:validation:Enum=Disabled;Review;Required
	// +optional
	Policy AgentRunDocsPolicy `json:"policy,omitempty"`
	// Paths lists operator-facing docs, samples, runbooks, or docs-site files
	// that should be checked or updated when behavior changes.
	// +optional
	Paths []string `json:"paths,omitempty"`
	// RuntimePaths lists runtime source, CRDs, manifests, or generated files
	// whose behavior should be checked against the docs.
	// +optional
	RuntimePaths []string `json:"runtimePaths,omitempty"`
	// Notes gives run-specific docs/runtime consistency instructions.
	// +optional
	Notes string `json:"notes,omitempty"`
}

type AgentRunIssueTrackingProvider string

const (
	AgentRunIssueTrackingProviderGitHub AgentRunIssueTrackingProvider = "github"
)

type AgentRunIssueUpdatePolicy string

const (
	AgentRunIssueUpdatePolicyReadOnly AgentRunIssueUpdatePolicy = "ReadOnly"
	AgentRunIssueUpdatePolicyComment  AgentRunIssueUpdatePolicy = "Comment"
	AgentRunIssueUpdatePolicyTriage   AgentRunIssueUpdatePolicy = "Triage"
)

type AgentRunIssueRef struct {
	// Repository is an owner/name GitHub repository. Empty defaults to the
	// AgentRun issueTracking.repository value.
	// +optional
	Repository string `json:"repository,omitempty"`
	// Number is the issue number in Repository.
	// +kubebuilder:validation:Minimum=1
	Number int `json:"number"`
}

type AgentRunIssueTrackingSpec struct {
	// Provider selects the issue tracker. The first implementation documents
	// GitHub because the Codex image includes gh and accepts GH_TOKEN.
	// +kubebuilder:validation:Enum=github
	// +optional
	Provider AgentRunIssueTrackingProvider `json:"provider,omitempty"`
	// Repository is the default owner/name repository for issue refs and
	// searches. Empty defaults to the operator platform repository configured by
	// ANVIL_AGENTS_PLATFORM_REPOSITORY.
	// +optional
	Repository string `json:"repository,omitempty"`
	// Issues lists concrete tickets this run should read before acting.
	// +optional
	Issues []AgentRunIssueRef `json:"issues,omitempty"`
	// SearchQuery is an optional GitHub issue search query for finding current
	// related tickets before deciding what work is already in progress.
	// +optional
	SearchQuery string `json:"searchQuery,omitempty"`
	// UpdatePolicy controls whether the run may update issues. Empty defaults
	// to ReadOnly. Comment permits concise progress/final comments only. Triage
	// permits evidence-backed issue creation and stale issue closure when the
	// run prompt explicitly assigns that work.
	// +kubebuilder:validation:Enum=ReadOnly;Comment;Triage
	// +optional
	UpdatePolicy AgentRunIssueUpdatePolicy `json:"updatePolicy,omitempty"`
}

type AgentRunCodexBackendSpec struct {
	// Model selects the Codex model. Empty uses the worker's Codex default.
	// +optional
	Model string `json:"model,omitempty"`
	// ReasoningEffort is passed through as Codex config when set.
	// +optional
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// Verbosity is passed through as Codex model_verbosity when set.
	// +kubebuilder:validation:Enum=low;medium;high
	// +optional
	Verbosity string `json:"verbosity,omitempty"`
	// ServiceTier selects the Codex service tier. Empty uses the worker default;
	// "default" is regular speed and "priority" is the faster tier when
	// available.
	// +optional
	ServiceTier string `json:"serviceTier,omitempty"`
	// GoalMode enables the Codex adapter's non-interactive goal contract. Use
	// it for long-running self-healing work that should continue until the
	// objective is complete or a hard blocker is proven.
	// +optional
	GoalMode bool `json:"goalMode,omitempty"`
	// Goal supplies Codex-specific goal text. The immutable image prompt still
	// takes precedence over this operator-provided goal.
	// +optional
	Goal string `json:"goal,omitempty"`
	// Sandbox selects the Codex sandbox. Empty defaults to read-only for
	// observe-only runs and workspace-write for mutation-capable intents.
	// +kubebuilder:validation:Enum=read-only;workspace-write;danger-full-access
	// +optional
	Sandbox string `json:"sandbox,omitempty"`
	// AdditionalArgs appends raw arguments to `codex exec`.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunOpenCodeBackendSpec struct {
	// Model selects a provider-qualified OpenCode model, for example
	// opencode/big-pickle or openai/gpt-5.4. Empty uses OpenCode configuration.
	// +optional
	Model string `json:"model,omitempty"`
	// Agent selects a configured primary OpenCode agent. Empty uses the default
	// build agent.
	// +optional
	Agent string `json:"agent,omitempty"`
	// Variant selects a provider-specific model variant or reasoning effort.
	// +optional
	Variant string `json:"variant,omitempty"`
	// Format controls OpenCode run output. Empty defaults to json so Job logs
	// retain machine-readable events.
	// +kubebuilder:validation:Enum=default;json
	// +optional
	Format string `json:"format,omitempty"`
	// Auto approves permission requests that are not explicitly denied by
	// OpenCode configuration. Leave false unless the selected Pod identity,
	// credentials, mounts, and egress are intentionally scoped for mutation.
	// +optional
	Auto *bool `json:"auto,omitempty"`
	// Pure controls whether OpenCode disables external plugins. Empty defaults
	// to true in the built-in runner for deterministic execution.
	// +optional
	Pure *bool `json:"pure,omitempty"`
	// AdditionalArgs appends runner-allowlisted presentation and logging
	// arguments to opencode run. Execution, permission, workspace, session,
	// prompt, provider, and model flags are rejected by the runner.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunHermesBackendSpec struct {
	// Model selects the Hermes model profile. Empty uses the adapter default.
	// Hermes Agent should usually be configured for Codex app-server runtime.
	// +optional
	Model string `json:"model,omitempty"`
	// ReasoningEffort selects the model reasoning effort when the Hermes
	// provider supports it.
	// +optional
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// ServiceTier selects the model service tier when the Hermes provider
	// supports it. Empty uses the adapter default; "default" is regular speed.
	// +optional
	ServiceTier string `json:"serviceTier,omitempty"`
	// Profile selects a Hermes profile or persona when the adapter supports
	// profile-mode homes. Empty uses the adapter default profile.
	// +optional
	Profile string `json:"profile,omitempty"`
	// UseCodexAppServer asks the Hermes adapter to route OpenAI turns through
	// Codex app-server. Empty/false leaves the image default in control.
	// +optional
	UseCodexAppServer bool `json:"useCodexAppServer,omitempty"`
	// AdditionalArgs appends raw arguments to the Hermes headless command.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunOpenClawBackendSpec struct {
	// AgentID selects the OpenClaw agent identity. Empty uses the adapter
	// default, usually "anvil".
	// +optional
	AgentID string `json:"agentId,omitempty"`
	// Model selects the OpenClaw model reference, for example openai/gpt-5.5.
	// Empty uses the adapter default.
	// +optional
	Model string `json:"model,omitempty"`
	// Thinking selects the OpenClaw thinking/reasoning setting when supported.
	// +optional
	Thinking string `json:"thinking,omitempty"`
	// ServiceTier selects the model service tier when the OpenClaw adapter
	// supports it. Empty uses the adapter default; "default" is regular speed.
	// +optional
	ServiceTier string `json:"serviceTier,omitempty"`
	// Local forces embedded local execution instead of a gateway when the
	// adapter supports both paths.
	// +optional
	Local *bool `json:"local,omitempty"`
	// AdditionalArgs appends raw arguments to the OpenClaw headless command.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunGrokBuildBackendSpec struct {
	// Model selects the Grok Build model or model profile. Empty uses the
	// adapter default.
	// +optional
	Model string `json:"model,omitempty"`
	// ReasoningEffort selects the Grok reasoning effort when the adapter
	// supports it.
	// +optional
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// ServiceTier selects the model service tier when the Grok Build adapter
	// supports it. Empty uses the adapter default.
	// +optional
	ServiceTier string `json:"serviceTier,omitempty"`
	// Profile selects the Grok Build profile or durable backend identity. Empty
	// uses the adapter default profile.
	// +optional
	Profile string `json:"profile,omitempty"`
	// Command overrides the executable used inside the Grok Build adapter
	// image. Empty lets the image choose grok-build or grok.
	// +optional
	Command string `json:"command,omitempty"`
	// AdditionalArgs appends raw arguments to the Grok Build headless command.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunPiBackendSpec struct {
	// Provider selects the concrete Pi provider name. Empty derives from
	// modelProvider/providerAuthMode; xAI OAuth maps to xai-auth.
	// +optional
	Provider string `json:"provider,omitempty"`
	// Model selects the Pi model ID, for example grok-4.5 or
	// grok-composer-2.5-fast. Empty uses the adapter default.
	// +optional
	Model string `json:"model,omitempty"`
	// Thinking selects Pi's thinking level when the selected provider/model
	// supports it, such as low, medium, high, xhigh, or max.
	// +optional
	Thinking string `json:"thinking,omitempty"`
	// Mode selects Pi output mode. Empty defaults to text; json is useful for
	// debugging event streams but text remains the normal AgentRun log mode.
	// +kubebuilder:validation:Enum=text;json
	// +optional
	Mode string `json:"mode,omitempty"`
	// NoSession runs Pi with --no-session for fully ephemeral checks. Empty
	// keeps Pi sessions in the attached data volume.
	// +optional
	NoSession bool `json:"noSession,omitempty"`
	// AdditionalArgs appends raw arguments to the Pi command.
	// +optional
	AdditionalArgs []string `json:"additionalArgs,omitempty"`
}

type AgentRunCustomBackendSpec struct {
	// Command overrides the container entrypoint for a custom backend image.
	// +optional
	Command []string `json:"command,omitempty"`
	// Args overrides the container arguments for a custom backend image.
	// +optional
	Args []string `json:"args,omitempty"`
}

type AgentRunHarnessBackendSpec struct {
	// Kind selects the harness backend adapter.
	// +kubebuilder:validation:Enum=codex;openCode;hermesAgent;openClaw;grokBuild;piAgent;custom
	// +optional
	Kind AgentRunHarnessBackendKind `json:"kind,omitempty"`
	// Image selects the agent container image. When empty, built-in backends use
	// the matching controller-configured runner image; custom requires a value.
	// +optional
	Image string `json:"image,omitempty"`
	// ModelProvider selects the model/tool-caller provider for provider-aware
	// harness adapters such as Hermes Agent, OpenClaw, or Pi. It does not select the
	// persistent backend identity; use kind=grokBuild and AgentDataVolume
	// backend=grokBuild for Grok Build's own durable backend. Select concrete
	// models through backend-specific model fields such as hermesAgent.model,
	// openClaw.model, grokBuild.model, piAgent.model, or codex.model. OpenCode
	// does not use this shared selector; set openCode.model to its native
	// provider/model value and inject provider-native authentication instead.
	// deepseek selects the Hermes Agent DeepSeek provider (apiKey mode;
	// DEEPSEEK_API_KEY env).
	// +kubebuilder:validation:Enum=openai-codex;openai;xai;deepseek
	// +optional
	ModelProvider AgentRunModelProvider `json:"modelProvider,omitempty"`
	// ProviderAuthMode selects how a provider-aware adapter should authenticate
	// to the selected model provider. For example, modelProvider=xai with
	// providerAuthMode=oauth maps Hermes Agent, OpenClaw, Grok Build, or Pi to
	// subscription-backed OAuth homes while keeping the concrete model selectable
	// through backend-specific model fields such as hermesAgent.model,
	// openClaw.model, grokBuild.model, or piAgent.model. OpenCode uses its native
	// provider/model selector and provider-native environment variables instead.
	// +kubebuilder:validation:Enum=apiKey;oauth
	// +optional
	ProviderAuthMode AgentRunProviderAuthMode `json:"providerAuthMode,omitempty"`
	// ImagePullPolicy controls how Kubernetes pulls the selected image.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// Codex configures the first proof-of-concept backend.
	// +optional
	Codex *AgentRunCodexBackendSpec `json:"codex,omitempty"`
	// OpenCode configures the provider-native OpenCode CLI adapter.
	// +optional
	OpenCode *AgentRunOpenCodeBackendSpec `json:"openCode,omitempty"`
	// HermesAgent configures the Hermes Agent adapter.
	// +optional
	HermesAgent *AgentRunHermesBackendSpec `json:"hermesAgent,omitempty"`
	// OpenClaw configures the OpenClaw adapter.
	// +optional
	OpenClaw *AgentRunOpenClawBackendSpec `json:"openClaw,omitempty"`
	// GrokBuild configures the Grok Build adapter and its durable backend.
	// +optional
	GrokBuild *AgentRunGrokBuildBackendSpec `json:"grokBuild,omitempty"`
	// PiAgent configures the Pi coding agent adapter.
	// +optional
	PiAgent *AgentRunPiBackendSpec `json:"piAgent,omitempty"`
	// Custom configures an operator-owned container adapter.
	// +optional
	Custom *AgentRunCustomBackendSpec `json:"custom,omitempty"`
}

type AgentRunSkillInjectionSpec struct {
	// Name is a stable, human-readable skill identifier. It is also used to
	// derive the mounted skill prompt file name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Description briefly explains when the skill should be applied.
	// +optional
	Description string `json:"description,omitempty"`
	// Content is inline Markdown instruction text injected into the harness
	// prompt layers. Keep durable, broadly reusable skills in repo files and
	// use this field for the run-specific selection or checklist.
	// +optional
	Content string `json:"content,omitempty"`
	// SourceRefs points at remote skill content that the controller resolves
	// when it creates the AgentRun payload ConfigMap. Use this for protected
	// skill files that should be updated independently of anvil-agents operator
	// releases.
	// +optional
	SourceRefs []AgentRunSkillSourceRef `json:"sourceRefs,omitempty"`
	// Paths lists repo-relative skill, docs, runbook, or source files the
	// harness should read before applying this skill.
	// +optional
	Paths []string `json:"paths,omitempty"`
}

type AgentSkillCompositionMode string

const (
	// AgentSkillCompositionAppend retains profile-selected sets and appends the
	// current layer's refs. It is the default composition mode.
	AgentSkillCompositionAppend AgentSkillCompositionMode = "Append"
	// AgentSkillCompositionReplace discards the inherited profile skill-set
	// composition, including its refs and overrides, before resolving the
	// current layer.
	AgentSkillCompositionReplace AgentSkillCompositionMode = "Replace"
)

type AgentSkillOverrideOperation string

const (
	AgentSkillOverrideAdd     AgentSkillOverrideOperation = "Add"
	AgentSkillOverrideAugment AgentSkillOverrideOperation = "Augment"
	AgentSkillOverrideReplace AgentSkillOverrideOperation = "Replace"
	AgentSkillOverrideDisable AgentSkillOverrideOperation = "Disable"
)

// AgentSkillOverrideSpec changes one resolved skill for a profile or run
// without mutating the referenced AgentSkillSet. Explicit operations avoid the
// ambiguous null and list semantics of generic JSON merge patches.
type AgentSkillOverrideSpec struct {
	// Name is the stable skill identity to add, augment, replace, or disable.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Operation defines how the supplied fields affect the named skill.
	// +kubebuilder:validation:Enum=Add;Augment;Replace;Disable
	Operation AgentSkillOverrideOperation `json:"operation"`
	// Description replaces the description for Augment and supplies it for Add
	// or Replace. Disable rejects all content fields.
	// +optional
	Description string `json:"description,omitempty"`
	// Content supplies complete content for Add or Replace and is appended under
	// a local-override heading for Augment.
	// +optional
	Content string `json:"content,omitempty"`
	// SourceRefs replace the source list for Replace and append for Add or
	// Augment.
	// +optional
	SourceRefs []AgentRunSkillSourceRef `json:"sourceRefs,omitempty"`
	// Paths replace the path list for Replace and append uniquely for Add or
	// Augment.
	// +optional
	Paths []string `json:"paths,omitempty"`
}

// AgentSkillCompositionSpec selects ordered reusable capability packs and
// applies profile- or run-local named skill overrides.
type AgentSkillCompositionSpec struct {
	// Mode controls how this layer combines refs with inherited profile refs.
	// Empty defaults to Append. Replace is useful for a run that needs a clean
	// skill-set swap while retaining the same non-skill role/profile policy.
	// +kubebuilder:validation:Enum=Append;Replace
	// +optional
	Mode AgentSkillCompositionMode `json:"mode,omitempty"`
	// Refs selects AgentSkillSets in declaration order. References must resolve
	// in the consuming AgentRun namespace.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Refs []NamespacedObjectReference `json:"refs,omitempty"`
	// Overrides are applied in declaration order after selected sets resolve.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Overrides []AgentSkillOverrideSpec `json:"overrides,omitempty"`
	// ExcludeGlobal skips namespace-global AgentSkillSets (spec.global=true)
	// that would otherwise attach automatically. Profile and run layers may set
	// this; either layer opting out disables globals for the run.
	// +optional
	ExcludeGlobal bool `json:"excludeGlobal,omitempty"`
}

type AgentRunSkillSourceRef struct {
	// GitHub downloads one file from the GitHub Contents API.
	// +optional
	GitHub *AgentRunGitHubSkillSourceSpec `json:"github,omitempty"`
}

type AgentRunGitHubSkillSourceSpec struct {
	// Repository is the owner/name GitHub repository, for example
	// example/agent-skills.
	// +kubebuilder:validation:MinLength=1
	Repository string `json:"repository"`
	// Ref is the full immutable Git commit object ID used to resolve the source.
	// +kubebuilder:validation:Pattern=`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`
	Ref string `json:"ref"`
	// Path is the repository-relative file path to download.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
	// APIBaseURL overrides the GitHub API base URL for GitHub Enterprise.
	// Empty defaults to https://api.github.com. The operator must explicitly
	// allowlist the host and requires HTTPS unless test-only insecure access is
	// enabled at process startup.
	// +optional
	APIBaseURL string `json:"apiBaseURL,omitempty"`
}

// AgentRunGitHubSkillCredential binds a GitHub API host to a token selected by
// the trusted harness execution envelope. AgentSkillSet authors cannot select
// credentials through remote content references.
type AgentRunGitHubSkillCredential struct {
	// APIHost is the exact GitHub or GitHub Enterprise API host, including an
	// optional port and without a URL scheme or path.
	// +kubebuilder:validation:MinLength=1
	APIHost string `json:"apiHost"`
	// TokenSecretRef selects a same-namespace Secret key containing the token.
	TokenSecretRef SecretKeyReference `json:"tokenSecretRef"`
}

type AgentRunSubagentSpec struct {
	// Name is a stable workstream or persona identifier.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Description summarizes the delegated responsibility.
	// +optional
	Description string `json:"description,omitempty"`
	// When explains when the harness should use this subagent/persona.
	// +optional
	When string `json:"when,omitempty"`
	// ToolNames lists tools this subagent/persona is expected to use.
	// +optional
	ToolNames []string `json:"toolNames,omitempty"`
	// SystemPrompt gives persona-specific instructions. Backends that do not
	// support real subagents should run this as a separately labeled pass.
	// +optional
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

// AgentRunToolImageInitializerSpec initializes a tool from an immutable OCI
// image before the agent container starts. The image's entrypoint, or the
// optional command and args override, must copy executable content into the
// shared /opt/anvil/tools directory. The initializer receives no harness
// envSecretRefs, data volumes, payload files, or SPIFFE socket. Its image must
// declare a numeric non-zero USER unless the harness security context sets a
// numeric non-root runAsUser.
type AgentRunToolImageInitializerSpec struct {
	// Image is an OCI image reference pinned by canonical sha256 digest. Tags
	// alone are rejected so an accepted AgentRun composition always identifies
	// the exact initializer bytes.
	// +kubebuilder:validation:MinLength=73
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^[^[:space:]@]+@sha256:[0-9a-f]{64}$`
	Image string `json:"image"`
	// Command optionally replaces the image entrypoint. It follows Kubernetes
	// container command semantics and must write the tool into
	// /opt/anvil/tools before exiting successfully.
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MinLength=1
	// +optional
	Command []string `json:"command,omitempty"`
	// Args optionally replaces the image arguments. It follows Kubernetes
	// container args semantics.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Args []string `json:"args,omitempty"`
}

type AgentRunToolSpec struct {
	// Name is a stable tool identifier such as kbctl.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Description explains what this tool is for during the run.
	// +optional
	Description string `json:"description,omitempty"`
	// SetupScript is an optional shell script run by compatible backend
	// adapters after repository checkout and before the agent runtime starts.
	// The script runs in the agent workdir as the agent container user. Keep it
	// idempotent, install into writable agent data volumes or the workdir, and
	// pass credentials through envSecretRefs instead of inline script text.
	// +optional
	SetupScript string `json:"setupScript,omitempty"`
	// ImageInitializer optionally materializes the tool from an immutable OCI
	// image into /opt/anvil/tools before the agent container starts. This is
	// independent of the selected runner image. Use SetupScript to add the fixed
	// directory to PATH when the runner does not already search it.
	// +optional
	ImageInitializer *AgentRunToolImageInitializerSpec `json:"imageInitializer,omitempty"`
	// VerifyCommand is an optional argv array run after SetupScript to prove
	// the tool is available, for example ["kbctl", "--version"].
	// +optional
	VerifyCommand []string `json:"verifyCommand,omitempty"`
}

type AgentRunDataVolumeRef struct {
	// Name references an AgentDataVolume in the AgentRun namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Namespace must be empty or the AgentRun namespace. Cross-namespace
	// volume mounts are intentionally not supported.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// MountPath overrides the AgentDataVolume default mount path for this run.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
	// SubPath optionally mounts a subdirectory of the AgentDataVolume PVC.
	// +optional
	SubPath string `json:"subPath,omitempty"`
	// ReadOnly mounts the durable data volume read-only for this run.
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
}

// AgentRunExternalSecretRefreshRef names an ExternalSecret and the
// same-namespace Kubernetes Secret that it must refresh before the Job starts.
type AgentRunExternalSecretRefreshRef struct {
	// Name is the ExternalSecret resource name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// TargetSecretRef is the target Secret created by the ExternalSecret. It
	// must be in the AgentRun namespace and also appear in EnvSecretRefs.
	TargetSecretRef NamespacedObjectReference `json:"targetSecretRef"`
}

type AgentRunSpiffeWorkloadAPISpec struct {
	// Enabled mounts the SPIFFE CSI Workload API socket into the agent pod.
	// The selected service account and pod labels must match a separately
	// managed ClusterSPIFFEID; enabling this field does not create identity.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// SPIFFEID is the exact X509-SVID the workload should select from the
	// Workload API. It is required when Enabled so a pod cannot silently use a different
	// SVID exposed by a co-located agent.
	// +optional
	SPIFFEID string `json:"spiffeId,omitempty"`
}

type AgentRunHarnessExecutionSpec struct {
	// ServiceAccountName selects the Kubernetes service account for the
	// agent pod. Use this to grant internal API and Kubernetes read access.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// EnvSecretRefs point at credentials and environment variables for the
	// selected backend. Each secret is projected with envFrom.
	// +optional
	EnvSecretRefs []NamespacedObjectReference `json:"envSecretRefs,omitempty"`
	// SkillSourceCredentials authorizes private remote skill reads by exact API
	// host. Credential selection belongs to the harness execution envelope, not
	// reusable AgentSkillSet content.
	// +optional
	SkillSourceCredentials []AgentRunGitHubSkillCredential `json:"skillSourceCredentials,omitempty"`
	// ExternalSecretRefreshRefs names ExternalSecrets and their target Secrets
	// that must be reconciled from an external store before this run's Job is
	// created. Each target Secret must also appear in EnvSecretRefs so the
	// preflight cannot refresh credentials that the Job does not consume.
	// +optional
	ExternalSecretRefreshRefs []AgentRunExternalSecretRefreshRef `json:"externalSecretRefreshRefs,omitempty"`
	// ExtraEnv adds static, container-specific environment values known when
	// the AgentRun is created. Do not put credentials here; use EnvSecretRefs.
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`
	// DataVolumeRefs attach intentional AgentDataVolume CRs that own or
	// reference persistent backend memory, sessions, caches, and tool state.
	// The Job remains ephemeral; only these volumes survive.
	// +optional
	DataVolumeRefs []AgentRunDataVolumeRef `json:"dataVolumeRefs,omitempty"`
	// SpiffeWorkloadAPI opts this run into the SPIFFE CSI Workload API mount.
	// Authorization remains separately controlled by the SVID's configured
	// permissions and workload scope.
	// +optional
	SpiffeWorkloadAPI AgentRunSpiffeWorkloadAPISpec `json:"spiffeWorkloadAPI,omitempty"`
	// Resources bounds the CPU, memory, and ephemeral storage assigned to the
	// agent container and every OCI tool initializer. Cluster policy may require
	// explicit requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// NodeSelector constrains the agent pod to selected nodes. It is merged with
	// placement requirements from attached AgentDataVolumes.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Affinity configures Kubernetes pod affinity/anti-affinity/node affinity.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// Tolerations configures Kubernetes tolerations for the agent pod.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// ImagePullSecrets lists same-namespace registry credentials for the
	// selected backend image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// PodSecurityContext configures pod-level security settings such as
	// fsGroup for writable local-path PVCs.
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	// SecurityContext configures the agent container security settings.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`
	// Workdir is the working directory inside the agent container.
	// +optional
	Workdir string `json:"workdir,omitempty"`
	// TimeoutSeconds bounds the child agent Job.
	// +kubebuilder:validation:Minimum=0
	// +optional
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// TTLSecondsAfterFinished controls Job cleanup. Empty keeps the Job until
	// ordinary garbage collection or manual cleanup.
	// +kubebuilder:validation:Minimum=0
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

type AgentRunHarnessSpec struct {
	// Intent tells the backend whether it may only observe, fix a transient
	// issue, prepare a durable pull request, or perform bounded cleanup.
	// +kubebuilder:validation:Enum=observe;fixTransient;proposeChange;cleanup
	// +optional
	Intent AgentRunIntent `json:"intent,omitempty"`
	// Backend selects the swappable harness adapter.
	// +optional
	Backend AgentRunHarnessBackendSpec `json:"backend,omitempty"`
	// Execution selects where the backend runs.
	// +optional
	Execution AgentRunHarnessExecutionSpec `json:"execution,omitempty"`
	// SkillInjections are backend-neutral instruction packs selected for this
	// run. The controller mounts them as prompt files for adapters that support
	// prompt layering. They may add expertise and checklists, but they must not
	// weaken immutable image safety prompts.
	// +optional
	SkillInjections []AgentRunSkillInjectionSpec `json:"skillInjections,omitempty"`
	// Subagents describes optional delegated workstreams or personas for
	// backends that can spawn workers. Backends without worker support should
	// treat each entry as a separately labeled pass in the same run.
	// +optional
	Subagents []AgentRunSubagentSpec `json:"subagents,omitempty"`
	// Tools describes app- or repo-specific commands needed by this run. Every
	// built-in adapter runs setup scripts and verify commands before its agent
	// runtime starts and exposes tool metadata to the prompt.
	// +optional
	Tools []AgentRunToolSpec `json:"tools,omitempty"`
	// SystemPrompt appends standing system-level instructions to the generated
	// run prompt. Use spec.prompt for one-off operator requests and keep durable
	// policy in the controller default or profile.
	// +optional
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

type AgentRunTelegramNotificationSpec struct {
	BotTokenRef SecretKeyReference `json:"botTokenRef"`
	ChatIDRef   SecretKeyReference `json:"chatIdRef"`
	// +optional
	APIBaseURL string `json:"apiBaseURL,omitempty"`
}

type AgentRunDiscordNotificationSpec struct {
	WebhookURLRef SecretKeyReference `json:"webhookURLRef"`
}

type AgentRunNotificationSpec struct {
	// MobileOps requests projection by an optional external mobile-operations
	// integration. anvil-agents records the field but does not provide one.
	// +optional
	MobileOps bool `json:"mobileOps,omitempty"`
	// +optional
	Telegram *AgentRunTelegramNotificationSpec `json:"telegram,omitempty"`
	// +optional
	Discord *AgentRunDiscordNotificationSpec `json:"discord,omitempty"`
}

type AgentRunSpec struct {
	// Purpose explains why this run exists.
	// +kubebuilder:validation:Enum=manual;adverseSituation;scheduledHealthCheck
	// +optional
	Purpose          AgentRunPurpose         `json:"purpose,omitempty"`
	SourceRef        AgentRunSourceRef       `json:"sourceRef,omitempty"`
	SourceUID        string                  `json:"sourceUID,omitempty"`
	SourceGeneration int64                   `json:"sourceGeneration,omitempty"`
	Trigger          AgentRunTriggerSnapshot `json:"trigger,omitempty"`
	// Prompt is the one-off operator request for this AgentRun. Profiles supply
	// durable standing instructions and defaults; this field supplies what this
	// run should do now. Empty means the profile, schedule, source, or trigger
	// context defines the work without an additional operator request.
	// +optional
	Prompt string `json:"prompt,omitempty"`
	// ProfileRef points at a same-namespace AgentRunProfile whose role, scope,
	// policy, composition, prompts, and notifications are resolved as defaults
	// before this AgentRun executes.
	// Fields set directly on this AgentRun override profile defaults; list
	// fields append profile entries first and run-local entries second.
	// +optional
	ProfileRef *NamespacedObjectReference `json:"profileRef,omitempty"`
	// HarnessProfileRef selects a reusable same-namespace runtime envelope.
	// A run-local ref atomically replaces the profile-selected harness runtime;
	// inline spec.harness fields remain explicit compatibility overrides.
	// +optional
	HarnessProfileRef *NamespacedObjectReference `json:"harnessProfileRef,omitempty"`
	// SkillSets selects reusable backend-neutral instruction packs and named
	// overrides. Run-local refs append to profile refs unless mode is Replace.
	// +optional
	SkillSets *AgentSkillCompositionSpec `json:"skillSets,omitempty"`
	// ToolSets selects reusable external tool contracts. Run-local refs append
	// to profile refs unless mode is Replace.
	// +optional
	ToolSets *AgentToolCompositionSpec `json:"toolSets,omitempty"`
	// CouncilRef optionally associates this run with a same-namespace
	// AgentCouncil. A non-nil run ref overrides a profile-level councilRef;
	// omission inherits the profile association. There is no run-level clear
	// signal in v1alpha1. A selected council's non-empty prompt is injected as a
	// reserved skill only after its member profile references validate.
	// +optional
	CouncilRef *NamespacedObjectReference `json:"councilRef,omitempty"`
	Scope      AgentRunScopeSpec          `json:"scope,omitempty"`
	// Docs tells the harness which docs/runtime surfaces must be kept aligned.
	// +optional
	Docs *AgentRunDocsSpec `json:"docs,omitempty"`
	// IssueTracking tells the harness which GitHub issues to read and whether
	// it may comment with progress or final status.
	// +optional
	IssueTracking *AgentRunIssueTrackingSpec `json:"issueTracking,omitempty"`
	// ScheduleRef attaches this run to the AgentSchedule that created it.
	// +optional
	ScheduleRef *NamespacedObjectReference `json:"scheduleRef,omitempty"`
	// SituationRef attaches this run to a reusable adverse-situation stream.
	// When set, the harness should watch that stream for new errors and avoid
	// creating independent response loops for each source event.
	// +optional
	SituationRef  *NamespacedObjectReference `json:"situationRef,omitempty"`
	Harness       AgentRunHarnessSpec        `json:"harness,omitempty"`
	Notifications *AgentRunNotificationSpec  `json:"notifications,omitempty"`
}

type AgentRunDecisionStatus struct {
	Classification string `json:"classification,omitempty"`
	Action         string `json:"action,omitempty"`
	Summary        string `json:"summary,omitempty"`
	ResidualRisk   string `json:"residualRisk,omitempty"`
}

type AgentRunStatusReport struct {
	Type           string       `json:"type,omitempty"`
	ObservedAt     *metav1.Time `json:"observedAt,omitempty"`
	Level          string       `json:"level,omitempty"`
	Stage          string       `json:"stage,omitempty"`
	Classification string       `json:"classification,omitempty"`
	Action         string       `json:"action,omitempty"`
	Summary        string       `json:"summary,omitempty"`
	Detail         string       `json:"detail,omitempty"`
	PullRequestURL string       `json:"pullRequestURL,omitempty"`
	ResidualRisk   string       `json:"residualRisk,omitempty"`
	NeedsHuman     bool         `json:"needsHuman,omitempty"`
	HumanFollowUp  string       `json:"humanFollowUp,omitempty"`
}

type AgentRunDataVolumeStatus struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	ClaimName string `json:"claimName,omitempty"`
	MountPath string `json:"mountPath,omitempty"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// AgentRunExternalSecretRefreshStatus records the fresh external-secret
// observation that allowed the controller to create the harness Job.
type AgentRunExternalSecretRefreshStatus struct {
	Name                string       `json:"name,omitempty"`
	Namespace           string       `json:"namespace,omitempty"`
	TargetSecret        string       `json:"targetSecret,omitempty"`
	RequestedAt         *metav1.Time `json:"requestedAt,omitempty"`
	PreviousRefreshTime *metav1.Time `json:"previousRefreshTime,omitempty"`
	RefreshedAt         *metav1.Time `json:"refreshedAt,omitempty"`
}

// AgentRunArchiveStatus records the controller-owned durable archive outcome
// for a terminal run. A retention policy may prune an AgentRun CR only after
// this status proves the terminal record was archived.
type AgentRunArchiveStatus struct {
	Store      string       `json:"store,omitempty"`
	ArchivedAt *metav1.Time `json:"archivedAt,omitempty"`
	Digest     string       `json:"digest,omitempty"`
	Error      string       `json:"error,omitempty"`
}

// AgentRunResolvedObjectReferenceStatus records the exact object version used
// to materialize a run without copying object content or Secret references into
// status.
type AgentRunResolvedObjectReferenceStatus struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	UID             string `json:"uid,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Digest          string `json:"digest,omitempty"`
	// Global is true when this skill/tool set was attached because
	// AgentSkillSet/AgentToolSet.spec.global is set (namespace default),
	// rather than only via an explicit profile/run ref.
	// +optional
	Global bool `json:"global,omitempty"`
}

// AgentRunResolvedScopeStatus records the opaque workload names inherited by
// the effective run without copying a mutable profile or any runtime fields.
type AgentRunResolvedScopeStatus struct {
	Application       string   `json:"application,omitempty"`
	ApplicationTarget string   `json:"applicationTarget,omitempty"`
	Repository        string   `json:"repository,omitempty"`
	RepositoryRef     string   `json:"repositoryRef,omitempty"`
	DestinationBranch string   `json:"destinationBranch,omitempty"`
	AllowedBranches   []string `json:"allowedBranches,omitempty"`
}

// AgentRunResolvedCompositionStatus records the reusable inputs accepted for a
// single execution. EffectiveDigest covers the complete resolved in-memory
// AgentRun spec; PayloadDigest covers the final mounted payload including
// remote skill bytes.
type AgentRunResolvedCompositionStatus struct {
	ResolvedAt        *metav1.Time                           `json:"resolvedAt,omitempty"`
	ProfileRef        *AgentRunResolvedObjectReferenceStatus `json:"profileRef,omitempty"`
	HarnessProfileRef *AgentRunResolvedObjectReferenceStatus `json:"harnessProfileRef,omitempty"`
	// CouncilRef records the exact AgentCouncil object version and spec digest
	// accepted for this run without copying its prompt or member profiles.
	// +optional
	CouncilRef      *AgentRunResolvedObjectReferenceStatus  `json:"councilRef,omitempty"`
	SkillSetRefs    []AgentRunResolvedObjectReferenceStatus `json:"skillSetRefs,omitempty"`
	ToolSetRefs     []AgentRunResolvedObjectReferenceStatus `json:"toolSetRefs,omitempty"`
	Scope           *AgentRunResolvedScopeStatus            `json:"scope,omitempty"`
	EffectiveDigest string                                  `json:"effectiveDigest,omitempty"`
	PayloadDigest   string                                  `json:"payloadDigest,omitempty"`
}

type AgentRunStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Phase              AgentRunPhase      `json:"phase,omitempty"`
	Backend            string             `json:"backend,omitempty"`
	// Model is the resolved backend model id/profile (for example gpt-5.5 or
	// grok-4.5) from the effective harness after composition. Empty when the
	// backend uses its runner default or does not select a model.
	// +optional
	Model                   string                                `json:"model,omitempty"`
	Intent                  string                                `json:"intent,omitempty"`
	Image                   string                                `json:"image,omitempty"`
	PlannedJobRef           *NamespacedObjectReference            `json:"plannedJobRef,omitempty"`
	JobCreateAttemptedAt    *metav1.Time                          `json:"jobCreateAttemptedAt,omitempty"`
	JobRef                  *NamespacedObjectReference            `json:"jobRef,omitempty"`
	JobUID                  string                                `json:"jobUID,omitempty"`
	JobSpecDigest           string                                `json:"jobSpecDigest,omitempty"`
	PayloadRef              *NamespacedObjectReference            `json:"payloadRef,omitempty"`
	PayloadUID              string                                `json:"payloadUID,omitempty"`
	RunnerPodRef            *NamespacedObjectReference            `json:"runnerPodRef,omitempty"`
	RunnerPodUID            string                                `json:"runnerPodUID,omitempty"`
	RunnerNode              string                                `json:"runnerNode,omitempty"`
	DataVolumes             []AgentRunDataVolumeStatus            `json:"dataVolumes,omitempty"`
	ExternalSecretRefreshes []AgentRunExternalSecretRefreshStatus `json:"externalSecretRefreshes,omitempty"`
	Archive                 *AgentRunArchiveStatus                `json:"archive,omitempty"`
	ResolvedComposition     *AgentRunResolvedCompositionStatus    `json:"resolvedComposition,omitempty"`
	StartedAt               *metav1.Time                          `json:"startedAt,omitempty"`
	CompletedAt             *metav1.Time                          `json:"completedAt,omitempty"`
	PromptHash              string                                `json:"promptHash,omitempty"`
	Decision                *AgentRunDecisionStatus               `json:"decision,omitempty"`
	Reports                 []AgentRunStatusReport                `json:"reports,omitempty"`
	Result                  runtime.RawExtension                  `json:"result,omitempty"`
	Output                  string                                `json:"output,omitempty"`
	PullRequestURL          string                                `json:"pullRequestURL,omitempty"`
	Error                   string                                `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=agentruns,scope=Namespaced,shortName=agrun
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".status.backend"
// +kubebuilder:printcolumn:name="Model",type="string",JSONPath=".status.model"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.sourceRef.name"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".spec.trigger.reason"
type AgentRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="AgentRun spec is immutable; create a new run instead"
	Spec   AgentRunSpec   `json:"spec,omitempty"`
	Status AgentRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRun `json:"items"`
}
