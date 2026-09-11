package desktop

import "strings"

// Catalog of workstation CLIs the desktop host will look for on PATH.
// Binary names match the built-in runner images (codex, grok, openclaw,
// opencode, hermes, pi) plus well-known workstation agents. Kubernetes
// clients are not part of this product: Anvil Agents Desktop talks to
// the anvil-agents OIDC API, not a kube-apiserver.

const (
	KindHarness     = "harness"
	KindWorkstation = "workstation"

	PromptStdin = "stdin"
	PromptFile  = "file"
)

// Invoke is a constant argv recipe for delegating a prompt to a catalog CLI.
// The OIDC access token is never placed in argv, env, or the prompt file.
type Invoke struct {
	Mode     string   // stdin | file; empty means inventory-only
	Args     []string // catalog constants; never derived from user input
	FileFlag string   // e.g. --prompt-file (file mode only)
}

// Tool describes one discoverable local client.
type Tool struct {
	ID           string
	DisplayName  string
	Kind         string
	Backend      string
	Binaries     []string
	VersionArgs  [][]string
	AuthFileHint string
	Notes        string
	Invoke       Invoke
}

func (t Tool) Delegatable() bool {
	return t.Kind == KindHarness && (t.Invoke.Mode == PromptStdin || t.Invoke.Mode == PromptFile)
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
			Notes:        "Workstation Codex CLI. Cluster AgentRuns use the Codex runner image, not this binary. Local auth stays in the CLI auth file.",
			Invoke:       Invoke{Mode: PromptStdin, Args: []string{"exec", "--skip-git-repo-check"}},
		},
		{
			ID:           "grok",
			DisplayName:  "xAI Grok",
			Kind:         KindHarness,
			Backend:      "grokBuild",
			Binaries:     []string{"grok"},
			VersionArgs:  [][]string{{"--version"}, {"version"}},
			AuthFileHint: "~/.grok/auth.json",
			Notes:        "xAI Grok Build CLI. Prompt is written to a 0600 temp file and passed as --prompt-file.",
			Invoke:       Invoke{Mode: PromptFile, FileFlag: "--prompt-file"},
		},
		{
			ID:          "openclaw",
			DisplayName: "OpenClaw",
			Kind:        KindHarness,
			Backend:     "openClaw",
			Binaries:    []string{"openclaw"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "OpenClaw agent CLI. Prompt is passed as --message-file. Durable cluster state still belongs on an AgentDataVolume.",
			Invoke:      Invoke{Mode: PromptFile, Args: []string{"agent"}, FileFlag: "--message-file"},
		},
		{
			ID:          "opencode",
			DisplayName: "OpenCode",
			Kind:        KindHarness,
			Backend:     "openCode",
			Binaries:    []string{"opencode"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "OpenCode CLI. Prompt is passed on stdin to `opencode run`.",
			Invoke:      Invoke{Mode: PromptStdin, Args: []string{"run"}},
		},
		{
			ID:          "hermes",
			DisplayName: "Hermes Agent",
			Kind:        KindHarness,
			Backend:     "hermesAgent",
			Binaries:    []string{"hermes"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Nous Hermes Agent CLI. Inventory only until a constant, prompt-file-safe invoke is documented.",
		},
		{
			ID:          "pi",
			DisplayName: "Pi",
			Kind:        KindHarness,
			Backend:     "piAgent",
			Binaries:    []string{"pi"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Pi coding agent CLI. Inventory only until a constant, prompt-file-safe invoke is documented.",
		},
		{
			ID:          "claude",
			DisplayName: "Claude Code",
			Kind:        KindWorkstation,
			Binaries:    []string{"claude"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Workstation client. Inventory only — not a wrapper-delegatable harness.",
		},
		{
			ID:          "cursor",
			DisplayName: "Cursor",
			Kind:        KindWorkstation,
			Binaries:    []string{"cursor", "cursor-agent"},
			VersionArgs: [][]string{{"--version"}, {"version"}},
			Notes:       "Workstation client. Inventory only — not a wrapper-delegatable harness.",
		},
	}
}

func catalogTool(id string) (Tool, bool) {
	id = strings.TrimSpace(id)
	for _, tool := range Catalog() {
		if tool.ID == id {
			return tool, true
		}
	}
	return Tool{}, false
}
