package onechat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestOneChatClaudeResumeUsageAndEnvironment(t *testing.T) {
	fakeClaude(t, `test -z "$KUBE_TEST$KUBECONFIG$OIDC_TOKEN$ANVIL_API_ACCESS_TOKEN$DATABASE_URL$WSLENV" || exit 9
printf '%s\n' "$@" > args.txt
cat > prompt.txt
printf '%s\n' '{"result":"Updated invoice","session_id":"native-claude-123","is_error":false,"total_cost_usd":0.031,"usage":{"input_tokens":12,"output_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":2}}'
`)
	for _, key := range []string{"KUBE_TEST", "KUBECONFIG", "OIDC_TOKEN", "ANVIL_API_ACCESS_TOKEN", "DATABASE_URL", "WSLENV"} {
		t.Setenv(key, "must-not-inherit")
	}
	c := testConfig(t)
	backend := &LocalBackend{Timeout: time.Second}
	m, err := NewManager(c, &fakeDecider{}, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := m.execute(context.Background(), Action{Type: "create", SetupID: "local", Title: "invoice", Workdir: c.Local.WorkdirRoots[0], Message: "first turn"})
	if first.Error != "" || first.Reply != "Updated invoice" || first.Receipt.Local.NativeSessionID != "native-claude-123" || first.Receipt.Local.ExitCode == nil || *first.Receipt.Local.ExitCode != 0 || first.Usage.CostUSD == nil || *first.Usage.CostUSD != 0.031 || *first.Usage.InputTokens != 12 || *first.Usage.OutputTokens != 5 || *first.Usage.CacheReadInputTokens != 7 || *first.Usage.CacheCreationInputTokens != 2 {
		t.Fatalf("missing native evidence %+v", first)
	}
	// Resume must survive reopening the state, not just an in-memory second call.
	m, err = NewManager(c, &fakeDecider{}, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := m.execute(context.Background(), Action{Type: "send", SessionID: first.SessionID, Message: "second turn"})
	if second.Error != "" {
		t.Fatal(second.Error)
	}
	args, err := os.ReadFile(filepath.Join(c.Local.WorkdirRoots[0], "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"-p\n--output-format\njson", "--permission-mode\nacceptEdits", "--strict-mcp-config", "--resume\nnative-claude-123"} {
		if !strings.Contains(string(args), part) {
			t.Fatalf("missing Claude contract %q in %s", part, args)
		}
	}
	prompt, err := os.ReadFile(filepath.Join(c.Local.WorkdirRoots[0], "prompt.txt"))
	if err != nil || string(prompt) != "second turn" {
		t.Fatalf("prompt must be stdin: %s %v", prompt, err)
	}
	if strings.Contains(string(args), "second turn") {
		t.Fatal("prompt leaked onto argv")
	}
}

func TestOneChatClaudeErrorTimeoutAndOutputCap(t *testing.T) {
	cases := []struct {
		name, body, want string
		timeout          time.Duration
	}{
		{"is error", `printf '%s' '{"result":"provider failure","session_id":"known-session","is_error":true,"total_cost_usd":0,"usage":{"input_tokens":0}}'`, "is_error", time.Second},
		{"exit failure", `printf '%s' '{"result":"partial","total_cost_usd":0.01,"usage":{"input_tokens":2}}'; exit 4`, "code 4", time.Second},
		{"timeout", `while :; do :; done`, "deadline", 20 * time.Millisecond},
		{"cap", `head -c 1100000 /dev/zero`, "exceeds", time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClaude(t, tc.body)
			backend := &LocalBackend{Timeout: tc.timeout}
			result, err := backend.Send(context.Background(), Setup{Harness: "claudeCode"}, Session{ID: "s", Workdir: t.TempDir()}, "prompt")
			if err == nil || !strings.Contains(err.Error(), tc.want) || result.State != "failed" || result.Reply != "" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.name == "is error" && (result.Usage.InputTokens == nil || *result.Usage.InputTokens != 0 || result.Usage.CostUSD == nil || *result.Usage.CostUSD != 0) {
				t.Fatal("reported zero became unknown")
			}
			if tc.name == "exit failure" && (result.Receipt.Local.ExitCode == nil || *result.Receipt.Local.ExitCode != 4 || result.Usage.InputTokens == nil) {
				t.Fatal("lost exit failure evidence")
			}
		})
	}
}

func TestOneChatClaudeDeciderStructuredOutput(t *testing.T) {
	dir := fakeClaude(t, `test "$(ls -A | wc -l)" -eq 0 || exit 8
case "$*" in *--no-session-persistence*--strict-mcp-config*--permission-mode\ manual*--model\ sonnet*) ;; *) exit 9 ;; esac
input=$(cat)
case "$input" in
  *'"outcomes"'*) printf '%s' '{"structured_output":{"answer":"Actual answer"},"usage":{"output_tokens":4},"total_cost_usd":0.02}' ;;
  *) printf '%s' '{"structured_output":{"reasoning_summary":"Greeting only.","confidence":1,"actions":[{"type":"answer","session_id":"","setup_id":"","title":"","workdir":"","message":"","text":"Hello"}]},"result":"ignored plain result","usage":{"input_tokens":6},"total_cost_usd":0.01}' ;;
esac
`)
	_ = dir
	d := &ClaudeDecider{Timeout: time.Second}
	decision, usage, err := d.Decide(context.Background(), DecideInput{Instructions: ManagerInstructions, Message: "hello"})
	if err != nil || decision.Actions[0].Text != "Hello" || usage.Model != "sonnet" || *usage.InputTokens != 6 || !json.Valid(decision.Raw) {
		t.Fatalf("decision=%+v usage=%+v err=%v", decision, usage, err)
	}
	answer, usage, err := d.Compose(context.Background(), ComposeInput{Message: "hello", Decision: decision, Outcomes: []ActionResult{{Reply: "Hello"}}})
	if err != nil || answer != "Actual answer" || *usage.OutputTokens != 4 {
		t.Fatalf("compose %q %+v %v", answer, usage, err)
	}
}

func TestOneChatManagerPromptBytes(t *testing.T) {
	// The user supplied this draft with an explicit byte-preservation contract.
	// codex-runs is ignored and is not available in a clean checkout.
	if fmt.Sprintf("%x", sha256.Sum256([]byte(ManagerInstructions))) != "aaa801d4f1bb202ed177bf787e31aaa352320b6ef1d2ad51ec2494ca675b6558" {
		t.Fatal("embedded manager draft changed")
	}
}
