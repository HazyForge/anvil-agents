package desktop

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	operatorGroupVersion = "control.anvil.hazyforge.io/v1alpha1"
	probeTimeout         = 2500 * time.Millisecond
)

// ClusterProbe talks to a Kubernetes apiserver for operator presence.
type ClusterProbe func(ctx context.Context, kubeconfig, contextName string) OperatorStatus

// ConsoleProbe checks that a console origin answers /healthz.
type ConsoleProbe func(ctx context.Context, origin string) ConsoleStatus

// ContextInfo is a kubeconfig context the operator can select.
type ContextInfo struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster,omitempty"`
	User      string `json:"user,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Current   bool   `json:"current"`
}

// OperatorStatus is a bounded probe of the anvil-agents API group.
type OperatorStatus struct {
	Reachable       bool   `json:"reachable"`
	APIGroupPresent bool   `json:"apiGroupPresent"`
	GroupVersion    string `json:"groupVersion,omitempty"`
	Message         string `json:"message,omitempty"`
}

// ConsoleStatus is a health check of the wrapped Anvil Agents Console origin.
type ConsoleStatus struct {
	URL       string `json:"url,omitempty"`
	Reachable bool   `json:"reachable"`
	Message   string `json:"message,omitempty"`
}

// ClusterSnapshot is kubeconfig + operator + console state for the UI.
type ClusterSnapshot struct {
	Kubeconfig      string         `json:"kubeconfig,omitempty"`
	CurrentContext  string         `json:"currentContext,omitempty"`
	SelectedContext string         `json:"selectedContext,omitempty"`
	Namespace       string         `json:"namespace,omitempty"`
	Contexts        []ContextInfo  `json:"contexts"`
	Operator        OperatorStatus `json:"operator"`
	Console         ConsoleStatus  `json:"console"`
	Message         string         `json:"message,omitempty"`
}

func loadKubeAPIConfig(kubeconfig string) (*clientcmdapi.Config, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path := strings.TrimSpace(kubeconfig); path != "" {
		rules.ExplicitPath = path
	}
	apiConfig, err := rules.Load()
	path := strings.TrimSpace(rules.GetExplicitFile())
	if path == "" {
		path = rules.GetDefaultFilename()
	}
	if err != nil {
		return nil, path, err
	}
	return apiConfig, path, nil
}

func listContexts(apiConfig *clientcmdapi.Config) []ContextInfo {
	names := make([]string, 0, len(apiConfig.Contexts))
	for name := range apiConfig.Contexts {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]ContextInfo, 0, len(names))
	for _, name := range names {
		ctx := apiConfig.Contexts[name]
		item := ContextInfo{Name: name, Current: name == apiConfig.CurrentContext}
		if ctx != nil {
			item.Cluster = ctx.Cluster
			item.User = ctx.AuthInfo
			item.Namespace = ctx.Namespace
		}
		out = append(out, item)
	}
	return out
}

func contextNamespace(apiConfig *clientcmdapi.Config, name string) string {
	if apiConfig == nil {
		return ""
	}
	ctx := apiConfig.Contexts[name]
	if ctx == nil {
		return ""
	}
	return ctx.Namespace
}

func defaultClusterProbe() ClusterProbe {
	return func(ctx context.Context, kubeconfig, contextName string) OperatorStatus {
		if err := ctx.Err(); err != nil {
			return OperatorStatus{Message: err.Error()}
		}
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		if path := strings.TrimSpace(kubeconfig); path != "" {
			rules.ExplicitPath = path
		}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(contextName)}
		loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
		restConfig, err := loader.ClientConfig()
		if err != nil {
			return OperatorStatus{Message: "kubeconfig is not usable: " + err.Error()}
		}
		restConfig.Timeout = probeTimeout
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return OperatorStatus{Message: "create Kubernetes client: " + err.Error()}
		}
		_, err = clientset.Discovery().ServerVersion()
		if err != nil {
			return OperatorStatus{Message: "apiserver is unreachable: " + err.Error()}
		}
		status := OperatorStatus{Reachable: true, GroupVersion: operatorGroupVersion}
		resources, err := clientset.Discovery().ServerResourcesForGroupVersion(operatorGroupVersion)
		if err != nil {
			status.Message = "cluster is reachable, but " + operatorGroupVersion + " is not installed"
			return status
		}
		for _, resource := range resources.APIResources {
			if resource.Kind == "AgentRun" || resource.Name == "agentruns" {
				status.APIGroupPresent = true
				status.Message = "anvil-agents operator API is present (" + agentsv1alpha1.GroupVersion.String() + ")"
				return status
			}
		}
		status.Message = "group " + operatorGroupVersion + " is present but AgentRun is missing"
		return status
	}
}

func defaultConsoleProbe() ConsoleProbe {
	return func(ctx context.Context, origin string) ConsoleStatus {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return ConsoleStatus{Message: "Set a console origin to wrap the Anvil Agents Console."}
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return ConsoleStatus{URL: origin, Message: "console URL must be http(s) with a host"}
		}
		if parsed.User != nil {
			return ConsoleStatus{URL: origin, Message: "console URL must not include credentials"}
		}
		health := strings.TrimRight(origin, "/") + "/healthz"
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, health, nil)
		if err != nil {
			return ConsoleStatus{URL: origin, Message: err.Error()}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ConsoleStatus{URL: origin, Message: "console /healthz failed: " + err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return ConsoleStatus{URL: origin, Message: fmt.Sprintf("console /healthz returned HTTP %d", resp.StatusCode)}
		}
		return ConsoleStatus{URL: origin, Reachable: true, Message: "console origin is healthy"}
	}
}

func sortStrings(values []string) {
	sort.Strings(values)
}
