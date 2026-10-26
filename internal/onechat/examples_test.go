package onechat

import (
	"os"
	"path/filepath"
	"testing"
)

// The example configs and scenarios are one chat inputs, not Kubernetes
// manifests, so internal/examples skips them; this pins their shape instead.
func TestOneChatExampleConfigsLoad(t *testing.T) {
	examples := filepath.Join("..", "..", "examples", "onechat")
	configs := map[string]Config{}
	for _, name := range []string{"onechat.yaml", "onechat.prod.yaml", "onechat.scenarios.yaml"} {
		config, err := LoadConfig(filepath.Join(examples, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		configs[name] = config
	}

	setups := map[string]bool{}
	for _, setup := range configs["onechat.scenarios.yaml"].Setups {
		setups[setup.ID] = true
	}
	paths, err := filepath.Glob(filepath.Join(examples, "scenarios", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no example scenarios found: %v", err)
	}
	for _, path := range paths {
		scenario, err := LoadScenario(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if scenario.Name == "" || len(scenario.Turns) == 0 {
			t.Fatalf("%s: a scenario needs a name and at least one turn", path)
		}
		labels := map[string]bool{}
		for _, step := range scenario.Setup {
			if !setups[step.Session.Setup] {
				t.Fatalf("%s: session %q uses setup %q, which onechat.scenarios.yaml does not define", path, step.Session.Label, step.Session.Setup)
			}
			if step.Session.Workspace != "" {
				workspace, ok := scenario.Workspaces[step.Session.Workspace]
				if !ok {
					t.Fatalf("%s: session %q names an undefined workspace %q", path, step.Session.Label, step.Session.Workspace)
				}
				// Fixture paths are relative to the repository root.
				if info, err := os.Stat(filepath.Join("..", "..", workspace.Fixture)); err != nil || !info.IsDir() {
					t.Fatalf("%s: workspace %q fixture %q is not a directory", path, step.Session.Workspace, workspace.Fixture)
				}
			}
			labels[step.Session.Label] = true
		}
		for _, turn := range scenario.Turns {
			if turn.Expect != nil && turn.Expect.Session != "" && !labels[turn.Expect.Session] {
				t.Fatalf("%s: turn %q expects unknown session %q", path, turn.Say, turn.Expect.Session)
			}
			if turn.Expect != nil && turn.Expect.Setup != "" && !setups[turn.Expect.Setup] {
				t.Fatalf("%s: turn %q expects unknown setup %q", path, turn.Say, turn.Expect.Setup)
			}
		}
	}
}
