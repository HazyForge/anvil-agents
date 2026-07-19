package main

import (
	"flag"
	"os"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/hazyforge/anvil-agents/internal/controller"
)

func main() {
	options := controller.DefaultOptions()
	var platformDocs string
	var adverseSourceGVKs string
	var adverseSourcesJSON string
	var githubAPIAllowedHosts string
	flag.StringVar(&options.MetricsBindAddress, "metrics-bind-address", options.MetricsBindAddress, "Metrics bind address.")
	flag.StringVar(&options.HealthProbeBindAddress, "health-probe-bind-address", options.HealthProbeBindAddress, "Health probe bind address.")
	flag.BoolVar(&options.LeaderElection, "leader-elect", options.LeaderElection, "Enable leader election.")
	flag.StringVar(&options.LeaderElectionID, "leader-election-id", options.LeaderElectionID, "Leader election resource name.")
	flag.StringVar(&options.WatchNamespaces, "watch-namespaces", options.WatchNamespaces, "Comma-separated namespaces to watch; empty watches all namespaces.")
	flag.StringVar(&options.AgentRunArchiveDatabaseURL, "archive-database-url", options.AgentRunArchiveDatabaseURL, "Optional Postgres URL for terminal AgentRun archives.")
	flag.DurationVar(&options.AgentRunTerminalRetention, "terminal-retention", options.AgentRunTerminalRetention, "Retention after successful archive; zero disables pruning.")
	flag.StringVar(&options.PlatformRepository, "platform-repository", options.PlatformRepository, "Repository name exposed to agent harnesses for operator context.")
	flag.StringVar(&options.PlatformRepositoryURL, "platform-repository-url", options.PlatformRepositoryURL, "Repository URL exposed to agent harnesses for operator context.")
	flag.StringVar(&platformDocs, "platform-docs", strings.Join(options.PlatformDocs, ","), "Comma-separated operator documentation paths exposed to agent harnesses.")
	flag.IntVar(&options.ApplicationMaxConcurrentRuns, "application-max-concurrent-runs", options.ApplicationMaxConcurrentRuns, "Default maximum active direct runs sharing an opaque application scope.")
	flag.StringVar(&options.DefaultStorageClass, "default-storage-class", options.DefaultStorageClass, "Optional default StorageClass for new AgentDataVolume claims; empty uses the cluster default.")
	flag.StringVar(&adverseSourceGVKs, "adverse-source-gvks", "", "Optional comma-separated apiVersion/kind values watched as adverse sources.")
	flag.StringVar(&adverseSourcesJSON, "adverse-sources-json", "", "Optional JSON array of administrator-owned adverse source integrations.")
	flag.StringVar(&githubAPIAllowedHosts, "github-api-allowed-hosts", strings.Join(options.GitHubAPIAllowedHosts, ","), "Comma-separated GitHub API hosts allowed for remote skill sources.")
	flag.BoolVar(&options.AllowInsecureGitHubAPI, "allow-insecure-github-api", options.AllowInsecureGitHubAPI, "Allow HTTP for allowlisted GitHub API hosts; intended only for local tests.")
	flag.StringVar(&options.CodexRunnerImage, "runner-image-codex", options.CodexRunnerImage, "Default image for Codex AgentRuns that do not set spec.harness.backend.image.")
	flag.StringVar(&options.OpenCodeRunnerImage, "runner-image-opencode", options.OpenCodeRunnerImage, "Default image for OpenCode AgentRuns that do not set spec.harness.backend.image.")
	flag.StringVar(&options.HermesAgentRunnerImage, "runner-image-hermes-agent", options.HermesAgentRunnerImage, "Default image for Hermes Agent AgentRuns that do not set spec.harness.backend.image.")
	flag.StringVar(&options.OpenClawRunnerImage, "runner-image-openclaw", options.OpenClawRunnerImage, "Default image for OpenClaw AgentRuns that do not set spec.harness.backend.image.")
	flag.StringVar(&options.GrokBuildRunnerImage, "runner-image-grok-build", options.GrokBuildRunnerImage, "Default image for Grok Build AgentRuns that do not set spec.harness.backend.image.")
	flag.StringVar(&options.PiAgentRunnerImage, "runner-image-pi-agent", options.PiAgentRunnerImage, "Default image for Pi Agent AgentRuns that do not set spec.harness.backend.image.")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	options.PlatformDocs = splitCSV(platformDocs)
	options.AdverseSourceGVKs = splitCSV(adverseSourceGVKs)
	var err error
	options.AdverseSources, err = controller.ParseAdverseSourcesJSON(adverseSourcesJSON)
	if err != nil {
		ctrl.Log.Error(err, "invalid adverse source configuration")
		os.Exit(2)
	}
	options.GitHubAPIAllowedHosts = splitCSV(githubAPIAllowedHosts)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	ctx := ctrl.SetupSignalHandler()
	if err := controller.Run(ctx, options); err != nil {
		ctrl.Log.Error(err, "controller stopped")
		os.Exit(1)
	}
}

func splitCSV(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}
