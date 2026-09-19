package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/jev"
	"github.com/hazyforge/anvil-agents/internal/runapi"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "/etc/anvil-agents-api/config.yaml", "Path to the AgentRun API configuration file.")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	log := ctrl.Log.WithName("agent-run-api")

	config, err := runapi.LoadConfig(configPath)
	if err != nil {
		log.Error(err, "load API configuration")
		os.Exit(1)
	}
	// Slice-2 standing turn gate: explicit opt-in only, off by default. The
	// environment variable can enable config-file standing.liveEnabled but
	// never disables it; no chart values and no Primaris sync changes.
	if enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("ANVIL_AGENTS_STANDING_LIVE"))); err == nil && enabled {
		config.Standing.LiveEnabled = true
	}
	// Jev intent routing gate: explicit opt-in only, off by default. The
	// environment variable can enable config-file chat.jevIntentEnabled but
	// never disables it. Jev only classifies intent; it never generates
	// chat text. See docs/jev-intent-routing.md.
	if enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(runapi.JevIntentEnvVar))); err == nil && enabled {
		config.Chat.JevIntentEnabled = true
	}
	// Jev serving-model pin: a non-empty ANVIL_AGENTS_JEV_MODEL overrides
	// config-file chat.jevModel. Empty (the default) tracks the jev-latest
	// alias. Jev only classifies intent; it never generates chat text.
	// See docs/jev-intent-routing.md.
	if model := strings.TrimSpace(os.Getenv(runapi.JevModelEnvVar)); model != "" {
		config.Chat.JevModel = model
	}
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "load Kubernetes configuration")
		os.Exit(1)
	}
	// Status polling stays responsive under the documented per-replica stream cap.
	restConfig.QPS = 50
	restConfig.Burst = 100
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "add Kubernetes scheme")
		os.Exit(1)
	}
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		log.Error(err, "add AgentRun scheme")
		os.Exit(1)
	}
	runClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "create AgentRun client")
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Error(err, "create Kubernetes clientset")
		os.Exit(1)
	}
	authenticator, err := runapi.NewOIDCAuthenticator(config.OIDC, config.Authorization, log.WithName("oidc"))
	if err != nil {
		log.Error(err, "configure OIDC authentication")
		os.Exit(1)
	}
	var chatStore chat.Store
	if config.Chat.Enabled {
		databaseURL := strings.TrimSpace(os.Getenv("ANVIL_AGENTS_CHAT_DATABASE_URL"))
		if databaseURL == "" {
			log.Error(fmt.Errorf("ANVIL_AGENTS_CHAT_DATABASE_URL is required when chat.enabled=true"), "configure standing-chat store")
			os.Exit(1)
		}
		openCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		store, err := chat.OpenPostgresStore(openCtx, databaseURL)
		cancel()
		if err != nil {
			log.Error(err, "open standing-chat store")
			os.Exit(1)
		}
		defer store.Close()
		chatStore = store
	}
	server, err := runapi.NewServer(config, authenticator, runClient, runapi.KubernetesLogSource{Client: clientset}, log)
	if err != nil {
		log.Error(err, "configure AgentRun API")
		os.Exit(1)
	}
	// Slice-3b standing process backend: when the live gate is on, live
	// standing turns execute through a real harness subprocess (PATH-resolved
	// CLI, filtered env, no Secret access) instead of the FakeBackend-only
	// test path. Gate off leaves no backend attached, so today's Job /
	// NeedsHuman hold behavior is unchanged. A missing CLI on PATH or any
	// harness failure falls back to that same hold behavior per turn.
	if config.Standing.LiveEnabled {
		server.SetStandingBackend(standing.NewProcessBackend(nil))
		log.Info("standing live process backend enabled", "supported", standing.SupportedProcessKinds())
	}
	// Jev intent routing backend: when the gate is on and TYPESAFE_API_KEY
	// is set, turns classify through the live System One endpoint. Without
	// the key no backend attaches and every turn falls back to today's
	// behavior — never a hard fail. The key comes from the process
	// environment only; the API gains no Secret access for this path.
	if config.Chat.JevIntentEnabled {
		if client, ok := jev.ClientFromEnv(); ok {
			server.SetJevBackend(client)
			model := strings.TrimSpace(config.Chat.JevModel)
			if model == "" {
				model = jev.DefaultModel
			}
			log.Info("jev intent classification enabled", "model", model)
		} else {
			log.Info("jev intent gate is on but TYPESAFE_API_KEY is not set; chat turns keep today's behavior")
		}
	}
	if chatStore != nil {
		server.SetChatStore(chatStore)
	}
	if err := server.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "AgentRun API stopped")
		os.Exit(1)
	}
}
