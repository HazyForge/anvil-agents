package desktop

// Catalog of workstation CLIs the desktop host will look for on PATH.
// Binary names match the built-in runner images (codex, grok, openclaw,
// opencode, hermes, pi) plus cluster clients. Additional well-known
// workstation agents are listed as kind "workstation" so they can be
// managed here without inventing a cluster backend mapping.

const (
	KindHarness     = "harness"
	KindControl     = "controlPlane"
	KindCluster     = "cluster"
	KindWorkstation = "workstation"
)

// Tool describes one discoverable local client.
type Tool struct {
	ID           string
	DisplayName  string
	Kind         string
	Backend      string
	Binaries     []string
	VersionArgs  [][]string
	AuthFileHint string
	ClusterHint  string
	Notes        string
}

// Catalog is the ordered inventory shown in the desktop UI.
func Catalog() []Tool {
	return []Tool{
		{
			ID:           "codex",
			DisplayName:  "OpenAI Codex",
			Kind:         KindHarness,
			Backend:      "codex",
			Binaries:     []string{"codex"},
			VersionArgs:  [][]string{{"--version"}, {"version"}},
			AuthFileHint: "~/.codex/auth.json",
			ClusterHint:  "anvil-agentctl auth codex diagnose --auth-file ~/.codex/auth.json",
			Notes:        "Workstation Codex CLI. Cluster runs use the Codex runner image, not this binary.",
		},
		{
			ID:           "grok",
			DisplayName:  "xAI Grok",
			Kind:         KindHarness,
			Backend:      "grokBuild",
			Binaries:     []string{"grok"},
			VersionArgs:  [][]string{{"--version"}, {"version"}},
			AuthFileHint: "~/.grok/auth.json",
			ClusterHint:  "anvil-agentctl auth grok reauth --auth-file ~/.grok/auth.json",
			Notes:        "xAI Grok Build CLI. Cluster backend kind is grokBuild.",
		},
		{
			ID:          "openclaw",
			DisplayName: "OpenClaw",
			Kind:        KindHarness,
			Backend:     "openClaw",
			Binaries:    []string{"openclaw"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			ClusterHint: "Cluster runs select an AgentHarnessProfile with backend.openClaw.",
			Notes:       "OpenClaw agent CLI. Durable state belongs on an AgentDataVolume, not this laptop home.",
		},
		{
			ID:          "opencode",
			DisplayName: "OpenCode",
			Kind:        KindHarness,
			Backend:     "openCode",
			Binaries:    []string{"opencode"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			ClusterHint: "Cluster runs select an AgentHarnessProfile with backend.openCode.",
			Notes:       "OpenCode CLI. Provider-qualified models stay on the harness profile.",
		},
		{
			ID:          "hermes",
			DisplayName: "Hermes Agent",
			Kind:        KindHarness,
			Backend:     "hermesAgent",
			Binaries:    []string{"hermes"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			ClusterHint: "Cluster runs select an AgentHarnessProfile with backend.hermesAgent.",
			Notes:       "Nous Hermes Agent CLI.",
		},
		{
			ID:          "pi",
			DisplayName: "Pi",
			Kind:        KindHarness,
			Backend:     "piAgent",
			Binaries:    []string{"pi"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			ClusterHint: "Cluster runs select an AgentHarnessProfile with backend.piAgent.",
			Notes:       "Pi coding agent CLI.",
		},
		{
			ID:          "claude",
			DisplayName: "Claude Code",
			Kind:        KindWorkstation,
			Binaries:    []string{"claude"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Workstation client. Not a built-in AgentRun backend; keep it here for inventory only.",
		},
		{
			ID:          "cursor",
			DisplayName: "Cursor",
			Kind:        KindWorkstation,
			Binaries:    []string{"cursor", "cursor-agent"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Workstation client. Not a built-in AgentRun backend.",
		},
		{
			ID:          "anvil-agentctl",
			DisplayName: "anvil-agentctl",
			Kind:        KindControl,
			Binaries:    []string{"anvil-agentctl"},
			VersionArgs: [][]string{{"--help"}},
			ClusterHint: "Uses the selected kubecontext and RBAC. Never send tokens to the OIDC API.",
			Notes:       "Public runtime CLI for append-only AgentRuns, control, schedules, auth, and volumes.",
		},
		{
			ID:          "kubectl",
			DisplayName: "kubectl",
			Kind:        KindCluster,
			Binaries:    []string{"kubectl"},
			VersionArgs: [][]string{{"version", "--client", "--short"}, {"version", "--client"}, {"version"}},
			Notes:       "Cluster client used to resolve kubecontexts. Anvil Agents Desktop also reads kubeconfig directly.",
		},
	}
}
