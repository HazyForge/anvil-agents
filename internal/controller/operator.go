package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

func Run(ctx context.Context, options *Options) error {
	if options == nil {
		options = DefaultOptions()
	}
	applySensitiveEnvironment(options)
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add Kubernetes scheme: %w", err)
	}
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add agent scheme: %w", err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		return fmt.Errorf("add Gateway API scheme: %w", err)
	}
	if options.ExternalTriggerHTTPRoute.Enabled {
		if err := options.ExternalTriggerHTTPRoute.Validate(); err != nil {
			return fmt.Errorf("external trigger HTTPRoute config: %w", err)
		}
		if !options.ExternalTriggersEnabled {
			return fmt.Errorf("external trigger HTTPRoute creation requires api.config.externalTriggers.enabled")
		}
	}

	// Synchronize watched sources on leaders and followers before readiness;
	// controller-runtime still starts reconcilers only after leader election.
	warmup := true
	managerOptions := ctrl.Options{
		Controller:             controllerconfig.Controller{EnableWarmup: &warmup},
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: options.MetricsBindAddress},
		HealthProbeBindAddress: options.HealthProbeBindAddress,
		LeaderElection:         options.LeaderElection,
		LeaderElectionID:       options.LeaderElectionID,
	}
	if namespaces := ParseWatchNamespaces(options.WatchNamespaces); len(namespaces) > 0 {
		managerOptions.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{}}
		for _, namespace := range namespaces {
			managerOptions.Cache.DefaultNamespaces[namespace] = cache.Config{}
		}
		if options.ExternalTriggerHTTPRoute.Enabled {
			routeNS := strings.TrimSpace(options.ExternalTriggerHTTPRoute.RouteNamespace)
			if routeNS == "" {
				routeNS = controllerPodNamespace()
			}
			if routeNS != "" {
				managerOptions.Cache.DefaultNamespaces[routeNS] = cache.Config{}
			}
		}
	}

	config, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes config: %w", err)
	}
	mgr, err := ctrl.NewManager(config, managerOptions)
	if err != nil {
		return fmt.Errorf("create controller manager: %w", err)
	}
	readiness := &managerReadiness{parent: ctx}
	mgr = &readinessManager{Manager: mgr, readiness: readiness}
	archiveStore, err := NewAgentRunArchiveStore(ctx, options)
	if err != nil {
		return fmt.Errorf("configure AgentRun archive: %w", err)
	}
	if archiveStore != nil {
		defer archiveStore.Close()
	}

	common := CommonReconcilerOptions{
		RESTConfig: mgr.GetConfig(),
		APIReader:  mgr.GetAPIReader(),
		Options:    options,
	}
	// Optional live Substrate plane (standing-chat spike, ATE binding). The
	// gate is off by default; with the gate on plus an ateapi endpoint the
	// operator dials ateapi over gRPC (verified TLS before any bearer token:
	// jwt CAFile/ateapi-ca ConfigMap, else podcert ClusterTrustBundle; token
	// from the token file or inline env, ServiceAccount mint fallback;
	// Kind-only skip-verify TLS behind --substrate-insecure for loopback). No token
	// material is ever logged.
	var substrateClient substrate.Client
	if options.SubstrateActorsEnabled && strings.TrimSpace(options.SubstrateEndpoint) != "" {
		ateCfg := substrate.ATEClientConfig{
			Address:              strings.TrimSpace(options.SubstrateEndpoint),
			Atespace:             strings.TrimSpace(options.SubstrateAtespace),
			Template:             strings.TrimSpace(options.SubstrateTemplate),
			TokenFile:            strings.TrimSpace(options.SubstrateTokenFile),
			Token:                strings.TrimSpace(options.SubstrateToken),
			Insecure:             options.SubstrateInsecure,
			CAFile:               strings.TrimSpace(options.SubstrateCAFile),
			CAConfigMapName:      strings.TrimSpace(options.SubstrateCAConfigMapName),
			CAConfigMapNamespace: strings.TrimSpace(options.SubstrateCAConfigMapNamespace),
			CAConfigMapKey:       strings.TrimSpace(options.SubstrateCAConfigMapKey),
			TLSServerName:        strings.TrimSpace(options.SubstrateTLSServerName),
		}
		live, closeConn, err := substrate.DialATEClient(ctx, ateCfg, substrate.ATEDialOptions{})
		if err != nil {
			return fmt.Errorf("dial Substrate ateapi: %w", err)
		}
		defer closeConn()
		substrateClient = live
	}
	registrations := []struct {
		name  string
		setup func(ctrl.Manager) error
	}{
		{"AgentDataVolume", (&AgentDataVolumeReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), DefaultStorageClass: options.DefaultStorageClass}).SetupWithManager},
		{"VolumeProfile", (&VolumeProfileReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AgentAuthSession", (&AgentAuthSessionReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), CommonReconcilerOptions: common}).SetupWithManager},
		{"AgentDataVolumeCopy", (&AgentDataVolumeCopyReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), CommonReconcilerOptions: common}).SetupWithManager},
		{"AgentRunControl", (&AgentRunControlReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AgentRun", (&AgentRunReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), CommonReconcilerOptions: common, AgentRunArchive: archiveStore, SubstrateClient: substrateClient}).SetupWithManager},
		{"AgentSchedule", (&AgentScheduleReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AgentExternalTrigger", (&AgentExternalTriggerReconciler{
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			ExternalTriggersEnabled: options.ExternalTriggersEnabled,
			HTTPRoute:               options.ExternalTriggerHTTPRoute,
		}).SetupWithManager},
		{"AgentChain", (&AgentChainReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AdverseSignal", (&AdverseSignalReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AdverseSituation", (&AdverseSituationReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
	}
	for _, registration := range registrations {
		if err := registration.setup(mgr); err != nil {
			return fmt.Errorf("setup %s controller: %w", registration.name, err)
		}
	}
	if err := SetupAdverseSituationTriggerReconcilers(mgr, options.AdverseSourceGVKs, options.AdverseSources); err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("register health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", readiness.Check); err != nil {
		return fmt.Errorf("register readiness check: %w", err)
	}
	return mgr.Start(ctx)
}
