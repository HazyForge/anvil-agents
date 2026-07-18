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
	hermesAgentRunnerImageEnv       = "ANVIL_AGENTS_RUNNER_IMAGE_HERMES_AGENT"
	openClawRunnerImageEnv          = "ANVIL_AGENTS_RUNNER_IMAGE_OPENCLAW"
	grokBuildRunnerImageEnv         = "ANVIL_AGENTS_RUNNER_IMAGE_GROK_BUILD"
	piAgentRunnerImageEnv           = "ANVIL_AGENTS_RUNNER_IMAGE_PI_AGENT"
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
	GitHubAPIAllowedHosts        []string
	AllowInsecureGitHubAPI       bool
	CodexRunnerImage             string
	HermesAgentRunnerImage       string
	OpenClawRunnerImage          string
	GrokBuildRunnerImage         string
	PiAgentRunnerImage           string
}

func DefaultOptions() *Options {
	return &Options{
		MetricsBindAddress:           defaultMetricsBindAddress,
		HealthProbeBindAddress:       defaultHealthProbeBindAddress,
		LeaderElectionID:             defaultLeaderElectionID,
		AgentRunArchiveDatabaseURL:   strings.TrimSpace(os.Getenv(archiveDatabaseURLEnv)),
		AgentRunTerminalRetention:    durationEnv(terminalRetentionEnv),
		PlatformRepository:           firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryEnv)), defaultPlatformRepository),
		PlatformRepositoryURL:        firstNonEmpty(strings.TrimSpace(os.Getenv(platformRepositoryURLEnv)), defaultPlatformRepositoryURL),
		PlatformDocs:                 csvOrDefault(os.Getenv(platformDocsEnv), defaultPlatformDocs),
		ApplicationMaxConcurrentRuns: positiveIntEnv(applicationMaxConcurrentRunsEnv, defaultApplicationConcurrency),
		DefaultStorageClass:          strings.TrimSpace(os.Getenv(defaultStorageClassEnv)),
		GitHubAPIAllowedHosts:        csvOrDefault(os.Getenv(githubAPIAllowedHostsEnv), defaultGitHubAPIAllowedHosts),
		AllowInsecureGitHubAPI:       boolEnv(allowInsecureGitHubAPIEnv),
		CodexRunnerImage:             firstNonEmpty(strings.TrimSpace(os.Getenv(codexRunnerImageEnv)), agentRunDefaultCodexImage),
		HermesAgentRunnerImage:       firstNonEmpty(strings.TrimSpace(os.Getenv(hermesAgentRunnerImageEnv)), agentRunDefaultHermesAgentImage),
		OpenClawRunnerImage:          firstNonEmpty(strings.TrimSpace(os.Getenv(openClawRunnerImageEnv)), agentRunDefaultOpenClawImage),
		GrokBuildRunnerImage:         firstNonEmpty(strings.TrimSpace(os.Getenv(grokBuildRunnerImageEnv)), agentRunDefaultGrokBuildImage),
		PiAgentRunnerImage:           firstNonEmpty(strings.TrimSpace(os.Getenv(piAgentRunnerImageEnv)), agentRunDefaultPiAgentImage),
	}
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
