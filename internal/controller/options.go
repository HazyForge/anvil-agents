package controller

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMetricsBindAddress        = ":8080"
	defaultHealthProbeBindAddress    = ":8081"
	defaultLeaderElectionID          = "anvil-agents.control.anvil.hazyforge.io"
	defaultPlatformRepository        = "HazyForge/anvil-agents"
	defaultPlatformRepositoryURL     = "https://github.com/HazyForge/anvil-agents.git"
	defaultApplicationConcurrency    = 1
	archiveDatabaseURLEnv            = "ANVIL_AGENTS_ARCHIVE_DATABASE_URL"
	terminalRetentionEnv             = "ANVIL_AGENTS_TERMINAL_RETENTION"
	platformRepositoryEnv            = "ANVIL_AGENTS_PLATFORM_REPOSITORY"
	platformRepositoryURLEnv         = "ANVIL_AGENTS_PLATFORM_REPOSITORY_URL"
	platformDocsEnv                  = "ANVIL_AGENTS_PLATFORM_DOCS"
	applicationMaxConcurrentRunsEnv  = "ANVIL_AGENTS_APPLICATION_MAX_CONCURRENT_RUNS"
	defaultStorageClassEnv           = "ANVIL_AGENTS_DEFAULT_STORAGE_CLASS"
	githubAPIAllowedHostsEnv         = "ANVIL_AGENTS_GITHUB_API_ALLOWED_HOSTS"
	allowInsecureGitHubAPIEnv        = "ANVIL_AGENTS_ALLOW_INSECURE_GITHUB_API"
	codexRunnerImageEnv              = "ANVIL_AGENTS_RUNNER_IMAGE_CODEX"
	openCodeRunnerImageEnv           = "ANVIL_AGENTS_RUNNER_IMAGE_OPENCODE"
	hermesAgentRunnerImageEnv        = "ANVIL_AGENTS_RUNNER_IMAGE_HERMES_AGENT"
	openClawRunnerImageEnv           = "ANVIL_AGENTS_RUNNER_IMAGE_OPENCLAW"
	grokBuildRunnerImageEnv          = "ANVIL_AGENTS_RUNNER_IMAGE_GROK_BUILD"
	piAgentRunnerImageEnv            = "ANVIL_AGENTS_RUNNER_IMAGE_PI_AGENT"
	primeAgentRunnerImageEnv         = "ANVIL_AGENTS_RUNNER_IMAGE_PRIME_AGENT"
	agyRunnerImageEnv                = "ANVIL_AGENTS_RUNNER_IMAGE_AGY"
	substrateActorsEnabledEnv        = "ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED"
	substrateEndpointEnv             = "ANVIL_AGENTS_SUBSTRATE_ENDPOINT"
	substrateTokenEnv                = "ANVIL_AGENTS_SUBSTRATE_TOKEN"
	substrateTokenFileEnv            = "ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE"
	substrateAtespaceEnv             = "ANVIL_AGENTS_SUBSTRATE_ATESPACE"
	substrateTemplateEnv             = "ANVIL_AGENTS_SUBSTRATE_TEMPLATE"
	substrateInsecureEnv             = "ANVIL_AGENTS_SUBSTRATE_INSECURE"
	substrateCAFileEnv               = "ANVIL_AGENTS_SUBSTRATE_CA_FILE"
	substrateCAConfigMapEnv          = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP"
	substrateCAConfigMapNamespaceEnv = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP_NAMESPACE"
	substrateCAConfigMapKeyEnv       = "ANVIL_AGENTS_SUBSTRATE_CA_CONFIGMAP_KEY"
	substrateTLSServerNameEnv        = "ANVIL_AGENTS_SUBSTRATE_TLS_SERVER_NAME"
	substrateGenerateOnActorEnv      = "ANVIL_AGENTS_SUBSTRATE_GENERATE_ON_ACTOR"
	substrateGenerateActorClassesEnv = "ANVIL_AGENTS_SUBSTRATE_GENERATE_ACTOR_CLASSES"
	substrateAtenetEndpointEnv       = "ANVIL_AGENTS_SUBSTRATE_ATENET_ENDPOINT"
)

var defaultGitHubAPIAllowedHosts = []string{"api.github.com"}

var defaultPlatformDocs = []string{
	"docs/agent-run.md",
	"internal/controller/prompts/agent-run-system.md",
	"internal/controller/agent_run_controller.go",
}

type Options struct {
	MetricsBindAddress           string
	HealthProbeBindAddress       string
	LeaderElection               bool
	LeaderElectionID             string
	WatchNamespaces              string
	AgentRunArchiveDatabaseURL   string
	AgentRunTerminalRetention    time.Duration
	PlatformRepository           string
	PlatformRepositoryURL        string
	PlatformDocs                 []string
	ApplicationMaxConcurrentRuns int
	DefaultStorageClass          string
	AdverseSourceGVKs            []string
	AdverseSources               []AdverseSourceConfig
	GitHubAPIAllowedHosts        []string
	AllowInsecureGitHubAPI       bool
	CodexRunnerImage             string
	OpenCodeRunnerImage          string
	HermesAgentRunnerImage       string
	OpenClawRunnerImage          string
	GrokBuildRunnerImage         string
	PiAgentRunnerImage           string
	PrimeAgentRunnerImage        string
	AgyRunnerImage               string
	ExternalTriggersEnabled      bool
	ExternalTriggerHTTPRoute     ExternalTriggerHTTPRouteConfig
	// SubstrateActorsEnabled is the explicit opt-in gate for live Substrate
	// actor dispatch (slice 2 of the standing-chat spike). Off by default:
	// well-formed SubstrateActor runs hold without creating a Job until the
	// operator enables this gate and configures SubstrateEndpoint.
	SubstrateActorsEnabled bool
	// SubstrateEndpoint is the ateapi gRPC target backing the live
	// substrate.Client (Kind-local for the spike, e.g.
	// ate-api-server.ate-system.svc:443). Empty disables live
	// dispatch even when the gate flag is set.
	SubstrateEndpoint string
	// SubstrateToken is the inline ateapi bearer token (env-only, never a
	// flag). Prefer SubstrateTokenFile. Never logged.
	SubstrateToken string
	// SubstrateTokenFile points at the file holding the ateapi bearer token
	// (kubectl-ate --token-file parallel). Preferred over the inline token.
	SubstrateTokenFile string
	// SubstrateAtespace forces every actor into one atespace. Empty maps
	// each Kubernetes namespace to the same-named atespace.
	SubstrateAtespace string
	// SubstrateTemplate is the default ActorTemplate when an actor spec
	// leaves the actor class empty. Required for live dispatch.
	SubstrateTemplate string
	// SubstrateInsecure dials TLS with certificate verification skipped for a
	// local Kind port-forward. Kind-only: the dialer refuses every non-loopback
	// endpoint when set.
	SubstrateInsecure bool
	// SubstrateCAFile is the path to the ateapi jwt CA PEM (ateapi-ca).
	// Never logged as bytes.
	SubstrateCAFile string
	// SubstrateCAConfigMapName names the jwt CA ConfigMap when it is not
	// mounted (default lookup ateapi-ca in ate-system).
	SubstrateCAConfigMapName string
	// SubstrateCAConfigMapNamespace is that ConfigMap's namespace.
	SubstrateCAConfigMapNamespace string
	// SubstrateCAConfigMapKey is that ConfigMap's data key (default ca.crt).
	SubstrateCAConfigMapKey string
	// SubstrateTLSServerName overrides ateapi TLS ServerName.
	SubstrateTLSServerName string
	// SubstrateGenerateOnActor streams the frozen AgentRun prompt through
	// atenet after bind. Off by default. actorsEnabled without this flag
	// keeps the SubstrateActorNotWired hold so Desktop standing-chat is
	// unchanged.
	SubstrateGenerateOnActor bool
	// SubstrateGenerateActorClasses is the exact actorClass allowlist for
	// generate-on-actor. Empty generates for nobody.
	SubstrateGenerateActorClasses []string
	// SubstrateAtenetEndpoint is the atenet-router HTTP target. Empty uses
	// substrate.DefaultAtenetEndpoint when generate is on.
	SubstrateAtenetEndpoint string
}

func DefaultOptions() *Options {
	return &Options{
		MetricsBindAddress:            defaultMetricsBindAddress,
		HealthProbeBindAddress:        defaultHealthProbeBindAddress,
		LeaderElectionID:              defaultLeaderElectionID,
		AgentRunTerminalRetention:     durationEnv(terminalRetentionEnv),
		PlatformRepository:            firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryEnv)), defaultPlatformRepository),
		PlatformRepositoryURL:         firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryURLEnv)), defaultPlatformRepositoryURL),
		PlatformDocs:                  csvOrDefault(os.Getenv(platformDocsEnv), defaultPlatformDocs),
		ApplicationMaxConcurrentRuns:  positiveIntEnv(applicationMaxConcurrentRunsEnv, defaultApplicationConcurrency),
		DefaultStorageClass:           strings.TrimSpace(os.Getenv(defaultStorageClassEnv)),
		GitHubAPIAllowedHosts:         csvOrDefault(os.Getenv(githubAPIAllowedHostsEnv), defaultGitHubAPIAllowedHosts),
		AllowInsecureGitHubAPI:        boolEnv(allowInsecureGitHubAPIEnv),
		CodexRunnerImage:              firstNonEmpty(strings.TrimSpace(os.Getenv(codexRunnerImageEnv)), agentRunDefaultCodexImage),
		OpenCodeRunnerImage:           firstNonEmpty(strings.TrimSpace(os.Getenv(openCodeRunnerImageEnv)), agentRunDefaultOpenCodeImage),
		HermesAgentRunnerImage:        firstNonEmpty(strings.TrimSpace(os.Getenv(hermesAgentRunnerImageEnv)), agentRunDefaultHermesAgentImage),
		OpenClawRunnerImage:           firstNonEmpty(strings.TrimSpace(os.Getenv(openClawRunnerImageEnv)), agentRunDefaultOpenClawImage),
		GrokBuildRunnerImage:          firstNonEmpty(strings.TrimSpace(os.Getenv(grokBuildRunnerImageEnv)), agentRunDefaultGrokBuildImage),
		PiAgentRunnerImage:            firstNonEmpty(strings.TrimSpace(os.Getenv(piAgentRunnerImageEnv)), agentRunDefaultPiAgentImage),
		PrimeAgentRunnerImage:         firstNonEmpty(strings.TrimSpace(os.Getenv(primeAgentRunnerImageEnv)), agentRunDefaultPrimeAgentImage),
		AgyRunnerImage:                firstNonEmpty(strings.TrimSpace(os.Getenv(agyRunnerImageEnv)), agentRunDefaultAgyImage),
		SubstrateActorsEnabled:        substrateBoolEnv(substrateActorsEnabledEnv),
		SubstrateEndpoint:             strings.TrimSpace(os.Getenv(substrateEndpointEnv)),
		SubstrateToken:                strings.TrimSpace(os.Getenv(substrateTokenEnv)),
		SubstrateTokenFile:            strings.TrimSpace(os.Getenv(substrateTokenFileEnv)),
		SubstrateAtespace:             strings.TrimSpace(os.Getenv(substrateAtespaceEnv)),
		SubstrateTemplate:             strings.TrimSpace(os.Getenv(substrateTemplateEnv)),
		SubstrateInsecure:             substrateBoolEnv(substrateInsecureEnv),
		SubstrateCAFile:               strings.TrimSpace(os.Getenv(substrateCAFileEnv)),
		SubstrateCAConfigMapName:      strings.TrimSpace(os.Getenv(substrateCAConfigMapEnv)),
		SubstrateCAConfigMapNamespace: strings.TrimSpace(os.Getenv(substrateCAConfigMapNamespaceEnv)),
		SubstrateCAConfigMapKey:       strings.TrimSpace(os.Getenv(substrateCAConfigMapKeyEnv)),
		SubstrateTLSServerName:        strings.TrimSpace(os.Getenv(substrateTLSServerNameEnv)),
		SubstrateGenerateOnActor:      substrateBoolEnv(substrateGenerateOnActorEnv),
		SubstrateGenerateActorClasses: uniqueCSV(os.Getenv(substrateGenerateActorClassesEnv)),
		SubstrateAtenetEndpoint:       strings.TrimSpace(os.Getenv(substrateAtenetEndpointEnv)),
	}
}

func applySensitiveEnvironment(options *Options) {
	if options == nil || strings.TrimSpace(options.AgentRunArchiveDatabaseURL) != "" {
		return
	}
	options.AgentRunArchiveDatabaseURL = strings.TrimSpace(os.Getenv(archiveDatabaseURLEnv))
}

func ParseWatchNamespaces(raw string) []string {
	return uniqueCSV(raw)
}

func uniqueCSV(raw string) []string {
	seen := map[string]struct{}{}
	var values []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		values = append(values, item)
	}
	return values
}

func csvOrDefault(raw string, fallback []string) []string {
	if values := uniqueCSV(raw); len(values) > 0 {
		return values
	}
	return append([]string(nil), fallback...)
}

func durationEnv(name string) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return 0
	}
	return value
}

func positiveIntEnv(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func boolEnv(name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && value
}

// substrateBoolEnv parses the Substrate gate booleans with the same truthy
// spellings as substrate.GateConfigFromEnv ("1", "true", "yes", "on") so the
// controller and the latency harness agree on whether the gate is set.
func substrateBoolEnv(name string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return false
	}
	if enabled, err := strconv.ParseBool(raw); err == nil {
		return enabled
	}
	return raw == "on" || raw == "yes"
}
