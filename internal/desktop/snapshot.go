package desktop

import (
	"context"
	"strings"
)

// ChatView is a pointer to standing-chat and future per-council chat.
// The desktop does not implement a second chat stack; it wraps the console.
type ChatView struct {
	StandingChatPath string `json:"standingChatPath"`
	CouncilChat      string `json:"councilChat"`
	Message          string `json:"message"`
}

// Snapshot is the payload for GET /local/v1/snapshot.
type Snapshot struct {
	ProductTitle string          `json:"productTitle"`
	ListenAddr   string          `json:"listenAddr,omitempty"`
	Prefs        Prefs           `json:"prefs"`
	Harnesses    []Discovered    `json:"harnesses"`
	Cluster      ClusterSnapshot `json:"cluster"`
	Chat         ChatView        `json:"chat"`
}

func defaultChatView() ChatView {
	return ChatView{
		StandingChatPath: "/chat",
		CouncilChat:      "future",
		Message:          "Standing chat lands in the cluster console (/chat). Per-council chat is a later design. This desktop app wraps those surfaces instead of forking a second chat UI.",
	}
}

func (s *Server) snapshot(ctx context.Context) Snapshot {
	prefs := s.prefs
	kubeconfig := strings.TrimSpace(prefs.Kubeconfig)
	if kubeconfig == "" {
		kubeconfig = strings.TrimSpace(s.opts.Kubeconfig)
	}

	cluster := ClusterSnapshot{
		Kubeconfig:      kubeconfig,
		SelectedContext: strings.TrimSpace(prefs.Context),
		Console:         ConsoleStatus{URL: strings.TrimSpace(prefs.ConsoleURL)},
		Contexts:        []ContextInfo{},
	}

	apiConfig, loadedPath, err := loadKubeAPIConfig(kubeconfig)
	if loadedPath != "" {
		cluster.Kubeconfig = loadedPath
	}
	if err != nil {
		cluster.Message = "kubeconfig is not available: " + err.Error()
	} else if apiConfig != nil {
		cluster.CurrentContext = apiConfig.CurrentContext
		cluster.Contexts = listContexts(apiConfig)
		if cluster.SelectedContext == "" {
			cluster.SelectedContext = apiConfig.CurrentContext
		}
		if cluster.SelectedContext != "" && apiConfig.Contexts[cluster.SelectedContext] == nil {
			cluster.Message = "selected kubecontext " + cluster.SelectedContext + " is not in kubeconfig"
		} else {
			cluster.Namespace = contextNamespace(apiConfig, cluster.SelectedContext)
		}
	}

	if cluster.SelectedContext != "" && cluster.Message == "" {
		probe := s.opts.ClusterProbe
		if probe == nil {
			probe = defaultClusterProbe()
		}
		cluster.Operator = probe(ctx, kubeconfig, cluster.SelectedContext)
	} else if cluster.SelectedContext == "" {
		cluster.Operator = OperatorStatus{Message: "select a kubecontext to probe the anvil-agents operator"}
	}

	consoleProbe := s.opts.ConsoleProbe
	if consoleProbe == nil {
		consoleProbe = defaultConsoleProbe()
	}
	cluster.Console = consoleProbe(ctx, prefs.ConsoleURL)

	return Snapshot{
		ProductTitle: "Anvil Agents Desktop",
		ListenAddr:   s.opts.Listen,
		Prefs:        prefs,
		Harnesses:    s.opts.Discoverer.Discover(ctx),
		Cluster:      cluster,
		Chat:         defaultChatView(),
	}
}
