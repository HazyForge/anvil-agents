package controller

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMetricsBindAddress       = ":8080"
	defaultHealthProbeBindAddress   = ":8081"
	defaultLeaderElectionID         = "anvil-agents.control.anvil.hazyforge.io"
	defaultPlatformRepository       = "HazyForge/anvil-agents"
	defaultPlatformRepositoryURL    = "https://github.com/HazyForge/anvil-agents.git"
	defaultApplicationConcurrency   = 1
	archiveDatabaseURLEnv           = "ANVIL_AGENTS_ARCHIVE_DATABASE_URL"
	terminalRetentionEnv            = "ANVIL_AGENTS_TERMINAL_RETENTION"
	platformRepositoryEnv           = "ANVIL_AGENTS_PLATFORM_REPOSITORY"
	platformRepositoryURLEnv        = "ANVIL_AGENTS_PLATFORM_REPOSITORY_URL"
	platformDocsEnv                 = "ANVIL_AGENTS_PLATFORM_DOCS"
	applicationMaxConcurrentRunsEnv = "ANVIL_AGENTS_APPLICATION_MAX_CONCURRENT_RUNS"
	defaultStorageClassEnv          = "ANVIL_AGENTS_DEFAULT_STORAGE_CLASS"
	githubAPIAllowedHostsEnv        = "ANVIL_AGENTS_GITHUB_API_ALLOWED_HOSTS"
	allowInsecureGitHubAPIEnv       = "ANVIL_AGENTS_ALLOW_INSECURE_GITHUB_API"
	codexRunnerImageEnv             = "ANVIL_AGENTS_RUNNER_IMAGE_CODEX"
	openCodeRunnerImageEnv          = "ANVIL_AGENTS_RUNNER_IMAGE_OPENCODE"
	hermesAgentRunnerImageEnv       = "ANVIL_AGENTS_RUNNER_IMAGE_HERMES_AGENT"
	openClawRunnerImageEnv          = "ANVIL_AGENTS_RUNNER_IMAGE_OPENCLAW"
	grokBuildRunnerImageEnv         = "ANVIL_AGENTS_RUNNER_IMAGE_GROK_BUILD"
	piAgentRunnerImageEnv           = "ANVIL_AGENTS_RUNNER_IMAGE_PI_AGENT"
	primeAgentRunnerImageEnv        = "ANVIL_AGENTS_RUNNER_IMAGE_PRIME_AGENT"
	agyRunnerImageEnv               = "ANVIL_AGENTS_RUNNER_IMAGE_AGY"
	substrateActorsEnabledEnv       = "ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED"
	substrateEndpointEnv            = "ANVIL_AGENTS_SUBSTRATE_ENDPOINT"
	substrateAtespaceEnv            = "ANVIL_AGENTS_SUBSTRATE_ATESPACE"
	substrateActorTemplateEnv       = "ANVIL_AGENTS_SUBSTRATE_TEMPLATE"
	substrateShimEndpointEnv        = "ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT"
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
	// SubstrateEndpoint is the atenet-router origin backing the live
	// substrate.Client (Kind-local for the spike, for example
	// http://localhost:8000 via port-forward or the in-cluster router
	// Service). Empty disables live dispatch even when the gate flag is set.
	SubstrateEndpoint string
	// SubstrateAtespace selects the Substrate tenancy for chat actors. Empty
	// means the substrate package default ("agents").
	SubstrateAtespace string
	// SubstrateActorTemplate is the optional `namespace/name` ActorTemplate
	// used for CreateActor through the control shim.
	SubstrateActorTemplate string
	// SubstrateShimEndpoint is the optional cmd/substrate-ate-shim origin
	// backing create/suspend/pause. Empty keeps those operations failing
	// closed while router resume/probe still works.
	SubstrateShimEndpoint string
}

func DefaultOptions() *Options {
	return &Options{
		MetricsBindAddress:           defaultMetricsBindAddress,
		HealthProbeBindAddress:       defaultHealthProbeBindAddress,
		LeaderElectionID:             defaultLeaderElectionID,
		AgentRunTerminalRetention:    durationEnv(terminalRetentionEnv),
		PlatformRepository:           firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryEnv)), defaultPlatformRepository),
		PlatformRepositoryURL:        firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryURLEnv)), defaultPlatformRepositoryURL),
		PlatformDocs:                 csvOrDefault(os.Getenv(platformDocsEnv), defaultPlatformDocs),
		ApplicationMaxConcurrentRuns: positiveIntEnv(applicationMaxConcurrentRunsEnv, defaultApplicationConcurrency),
		DefaultStorageClass:          strings.TrimSpace(os.Getenv(defaultStorageClassEnv)),
		GitHubAPIAllowedHosts:        csvOrDefault(os.Getenv(githubAPIAllowedHostsEnv), defaultGitHubAPIAllowedHosts),
		AllowInsecureGitHubAPI:       boolEnv(allowInsecureGitHubAPIEnv),
		CodexRunnerImage:             firstNonEmpty(strings.TrimSpace(os.Getenv(codexRunnerImageEnv)), agentRunDefaultCodexImage),
		OpenCodeRunnerImage:          firstNonEmpty(strings.TrimSpace(os.Getenv(openCodeRunnerImageEnv)), agentRunDefaultOpenCodeImage),
		HermesAgentRunnerImage:       firstNonEmpty(strings.TrimSpace(os.Getenv(hermesAgentRunnerImageEnv)), agentRunDefaultHermesAgentImage),
		OpenClawRunnerImage:          firstNonEmpty(strings.TrimSpace(os.Getenv(openClawRunnerImageEnv)), agentRunDefaultOpenClawImage),
		GrokBuildRunnerImage:         firstNonEmpty(strings.TrimSpace(os.Getenv(grokBuildRunnerImageEnv)), agentRunDefaultGrokBuildImage),
		PiAgentRunnerImage:           firstNonEmpty(strings.TrimSpace(os.Getenv(piAgentRunnerImageEnv)), agentRunDefaultPiAgentImage),
		PrimeAgentRunnerImage:        firstNonEmpty(strings.TrimSpace(os.Getenv(primeAgentRunnerImageEnv)), agentRunDefaultPrimeAgentImage),
		AgyRunnerImage:               firstNonEmpty(strings.TrimSpace(os.Getenv(agyRunnerImageEnv)), agentRunDefaultAgyImage),
		SubstrateActorsEnabled:       boolEnv(substrateActorsEnabledEnv),
		SubstrateEndpoint:            strings.TrimSpace(os.Getenv(substrateEndpointEnv)),
		SubstrateAtespace:            strings.TrimSpace(os.Getenv(substrateAtespaceEnv)),
		SubstrateActorTemplate:       strings.TrimSpace(os.Getenv(substrateActorTemplateEnv)),
		SubstrateShimEndpoint:        strings.TrimSpace(os.Getenv(substrateShimEndpointEnv)),
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
