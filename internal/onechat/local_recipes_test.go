package onechat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installRecipeScript(t *testing.T, kind string) {
	t.Helper()
	binary := "claude"
	output := `{"result":"final reply","session_id":"11111111-1111-4111-8111-111111111111","is_error":false}`
	if kind == "codex" {
		binary = "codex"
		output = "{\"type\":\"thread.started\",\"thread_id\":\"11111111-1111-4111-8111-111111111111\"}\n" +
			"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"progress only\"}}\n" +
			"{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"final reply\"}}\n" +
			"{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":8,\"output_tokens\":4,\"cached_input_tokens\":3}}"
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ntest -z \"$KUBECONFIG$OIDC_TOKEN\" || exit 9\nprintf '%s\\n' \"$@\" > args.txt\ncat > prompt.txt\ncat <<'REPLY'\n" + output + "\nREPLY\n"
	if err := os.WriteFile(filepath.Join(dir, binary), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOneChatLocalRecipesNeverBypassPermissions(t *testing.T) {
	cases := []Setup{
		{Harness: "claudeCode", PermissionMode: "acceptEdits"},
		{Harness: "claudeCode", PermissionMode: "default"},
		{Harness: "claudeCode", PermissionMode: "plan"},
		{Harness: "codex", Sandbox: "read-only"},
		{Harness: "codex", Sandbox: "workspace-write"},
	}
	for _, setup := range cases {
		t.Run(setup.Harness+setup.PermissionMode+setup.Sandbox, func(t *testing.T) {
			installRecipeScript(t, setup.Harness)
			t.Setenv("KUBECONFIG", "must-not-inherit")
			t.Setenv("OIDC_TOKEN", "must-not-inherit")
			config := testConfig(t)
			setup.ID = "local"
			setup.Location = "local"
			config.Setups = []Setup{setup}
			backend := &LocalBackend{Timeout: time.Second}
			manager, err := NewManager(config, &fakeDecider{}, backend, nil)
			if err != nil {
				t.Fatal(err)
			}
			id := ""
			for turn := 0; turn < 2; turn++ {
				action := Action{Type: "send", SessionID: id, Message: "continue safely"}
				if turn == 0 {
					action = Action{Type: "create", SetupID: "local", Title: "safe work", Workdir: config.Local.WorkdirRoots[0], Message: "start safely"}
				}
				result := manager.execute(context.Background(), action)
				if result.Error != "" || result.Reply != "final reply" || result.Receipt.Local.ExitCode == nil || *result.Receipt.Local.ExitCode != 0 {
					t.Fatalf("execution: %+v", result)
				}
				id = result.SessionID
				raw, err := os.ReadFile(filepath.Join(config.Local.WorkdirRoots[0], "args.txt"))
				if err != nil {
					t.Fatal(err)
				}
				args := string(raw)
				for _, forbidden := range []string{"--always-approve", "--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "bypassPermissions", "danger-full-access", "--yolo"} {
					if strings.Contains(args, forbidden) {
						t.Fatalf("turn %d contains forbidden argv %q: %s", turn, forbidden, args)
					}
				}
				if setup.Harness == "codex" {
					if result.Usage.InputTokens == nil || *result.Usage.InputTokens != 8 || *result.Usage.OutputTokens != 4 || *result.Usage.CacheReadInputTokens != 3 || result.Usage.CostUSD != nil {
						t.Fatal("incorrect Codex usage")
					}
					want := "--sandbox\n" + setup.Sandbox + "\n-C\n" + config.Local.WorkdirRoots[0]
					if turn == 1 {
						want = "-c\nsandbox_mode=\"" + setup.Sandbox + "\""
						if !strings.HasPrefix(args, "exec\nresume\n11111111-1111-4111-8111-111111111111\n") {
							t.Fatalf("missing resume id: %s", args)
						}
					}
					if !strings.Contains(args, want) {
						t.Fatalf("sandbox missing: %s", args)
					}
				} else {
					if !strings.Contains(args, "--strict-mcp-config\n--permission-mode\n"+setup.PermissionMode) {
						t.Fatalf("Claude mode missing: %s", args)
					}
					if turn == 1 && !strings.Contains(args, "--resume\n11111111-1111-4111-8111-111111111111") {
						t.Fatalf("Claude resume missing: %s", args)
					}
				}
				prompt, err := os.ReadFile(filepath.Join(config.Local.WorkdirRoots[0], "prompt.txt"))
				if err != nil || string(prompt) != action.Message || strings.Contains(args, action.Message) {
					t.Fatal("prompt was not sent solely on stdin")
				}
				// Reopen between calls to test durable resume and recorded sandbox.
				manager, err = NewManager(config, &fakeDecider{}, backend, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOneChatLocalRecipesConfigRestrictions(t *testing.T) {
	cases := []struct {
		setup Setup
		want  string
	}{
		{Setup{Harness: "claudeCode", PermissionMode: "bypassPermissions"}, "permissionMode"},
		{Setup{Harness: "claudeCode", PermissionMode: "auto"}, "permissionMode"},
		{Setup{Harness: "claudeCode", PermissionMode: "manual"}, "permissionMode"},
		{Setup{Harness: "codex", Sandbox: "danger-full-access"}, "sandbox"},
		{Setup{Harness: "codex", Sandbox: "unknown"}, "sandbox"},
		{Setup{Harness: "claudeCode"}, ""},
		{Setup{Harness: "codex"}, ""},
	}
	for _, kind := range []string{"grokBuild", "openCode", "primeAgent", "agy", "unknown"} {
		cases = append(cases, struct {
			setup Setup
			want  string
		}{Setup{Harness: kind}, "no safe local recipe yet for " + kind})
	}
	for _, tc := range cases {
		t.Run(tc.setup.Harness+tc.setup.PermissionMode+tc.setup.Sandbox, func(t *testing.T) {
			config := testConfig(t)
			setup := tc.setup
			setup.ID = "local"
			setup.Location = "local"
			config.Setups = []Setup{setup}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig(path)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if setup.Harness == "codex" && loaded.Setups[0].Sandbox != "read-only" {
				t.Fatal("Codex default is not read-only")
			}
			if setup.Harness == "claudeCode" && loaded.Setups[0].PermissionMode != "acceptEdits" {
				t.Fatal("Claude default changed")
			}
		})
	}
}

func TestOneChatLocalRecipesClaudeAllowedTools(t *testing.T) {
	for _, tool := range []string{"Bash", "Bash(*)", "Bash(:*)", "Bash(go test; rm -rf /:*)", "WebFetch", "Write"} {
		setup := Setup{Harness: "claudeCode", AllowedTools: []string{tool}}
		if err := localSettings(&setup); err == nil || !strings.Contains(err.Error(), "allowedTools") {
			t.Fatalf("%q was accepted: %v", tool, err)
		}
	}
	codex := Setup{Harness: "codex", AllowedTools: []string{"Read"}}
	if err := localSettings(&codex); err == nil {
		t.Fatal("allowedTools was accepted for Codex")
	}
	setup := Setup{Harness: "claudeCode", AllowedTools: []string{"Read", "Bash(go test:*)", "Bash(python3 -m unittest:*)"}}
	if err := localSettings(&setup); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(localRecipes["claudeCode"](setup, Session{}), "\n")
	if !strings.Contains(args, "--allowedTools\nRead,Bash(go test:*),Bash(python3 -m unittest:*)") {
		t.Fatalf("allowedTools missing: %s", args)
	}
}

func TestOneChatLocalRecipesResumeCannotWidenSandbox(t *testing.T) {
	installRecipeScript(t, "codex")
	config := testConfig(t)
	config.Setups[0].Harness = "codex"
	config.Setups[0].Sandbox = "read-only"
	backend := &LocalBackend{Timeout: time.Second}
	manager, err := NewManager(config, &fakeDecider{}, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := manager.execute(context.Background(), Action{Type: "create", SetupID: "local", Title: "review", Workdir: config.Local.WorkdirRoots[0], Message: "review"})
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	config.Setups[0].Sandbox = "workspace-write"
	manager, err = NewManager(config, &fakeDecider{}, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := manager.execute(context.Background(), Action{Type: "send", SessionID: first.SessionID, Message: "continue"})
	args, err := os.ReadFile(filepath.Join(config.Local.WorkdirRoots[0], "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Error != "" || second.Receipt.Local.Sandbox != "read-only" || !strings.Contains(string(args), `sandbox_mode="read-only"`) || strings.Contains(string(args), "workspace-write") {
		t.Fatalf("resume widened sandbox: %+v %s", second, args)
	}
}

func TestOneChatLocalRecipesRejectFlagAsResumeID(t *testing.T) {
	installRecipeScript(t, "claudeCode")
	backend := &LocalBackend{Timeout: time.Second}
	result, err := backend.Send(context.Background(), Setup{Harness: "claudeCode"}, Session{ID: "local", Workdir: t.TempDir(), NativeSessionID: "--dangerously-skip-permissions"}, "continue")
	if err == nil || result.HarnessCalled {
		t.Fatal("a flag-shaped saved resume id reached the local executable")
	}
}
