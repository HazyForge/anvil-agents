package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func Run(ctx context.Context, options *Options) error {
	if options == nil {
		options = DefaultOptions()
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add Kubernetes scheme: %w", err)
	}
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add agent scheme: %w", err)
	}

	managerOptions := ctrl.Options{
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
	}

	config, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes config: %w", err)
	}
	mgr, err := ctrl.NewManager(config, managerOptions)
	if err != nil {
		return fmt.Errorf("create controller manager: %w", err)
	}
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
	registrations := []struct {
		name  string
		setup func(ctrl.Manager) error
	}{
		{"AgentDataVolume", (&AgentDataVolumeReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), DefaultStorageClass: options.DefaultStorageClass}).SetupWithManager},
		{"VolumeProfile", (&VolumeProfileReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AgentRunControl", (&AgentRunControlReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
		{"AgentRun", (&AgentRunReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), CommonReconcilerOptions: common, AgentRunArchive: archiveStore}).SetupWithManager},
		{"AgentSchedule", (&AgentScheduleReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager},
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
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("register readiness check: %w", err)
	}
	return mgr.Start(ctx)
}
