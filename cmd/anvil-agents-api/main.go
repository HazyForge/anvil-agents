package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
	"github.com/hazyforge/anvil-agents/internal/runapi"
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
	var archiveStore archive.AgentRunArchiveStore
	if databaseURL := strings.TrimSpace(os.Getenv("ANVIL_AGENTS_ARCHIVE_DATABASE_URL")); databaseURL != "" {
		openCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		store, err := archive.OpenPostgresAgentRunArchiveStore(openCtx, databaseURL)
		cancel()
		if err != nil {
			log.Error(err, "open AgentRun archive database; archive list/purge verify disabled")
		} else {
			archiveStore = store
			defer store.Close()
			log.Info("AgentRun PostgreSQL archive store enabled for API")
		}
	}
	server, err := runapi.NewServerWithArchive(config, authenticator, runClient, runapi.KubernetesLogSource{Client: clientset}, archiveStore, log)
	if err != nil {
		log.Error(err, "configure AgentRun API")
		os.Exit(1)
	}
	if err := server.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "AgentRun API stopped")
		os.Exit(1)
	}
}
