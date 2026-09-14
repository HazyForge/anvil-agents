package desktop

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgyNativeInputPreservesPrompt(t *testing.T) {
	prompt := "line one\nline two with \"quotes\", $(shell) and \\ escapes"
	encoded, err := io.ReadAll(agyPromptInput(prompt))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), "\n") != 1 {
		t.Fatalf("expected one NDJSON event: %q", encoded)
	}
	var event struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(encoded, &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "user" || event.Message.Content != prompt {
		t.Fatalf("prompt changed: %#v", event)
	}
	tool, ok := catalogTool("agy")
	if !ok || !tool.Delegatable() || tool.Invoke.Mode != PromptAgyStream {
		t.Fatal("AGY native stream recipe missing")
	}
	for _, arg := range tool.Invoke.Args {
		if arg == "-p" || arg == "--thinking" {
			t.Fatalf("unsupported AGY argument: %s", arg)
		}
	}
}

func TestAgyTurnKeepsStdinUntilResult(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	writeExec(t, bin, `#!/bin/bash
set -eu
IFS= read -r prompt
if IFS= read -r -t 0.1 extra; then
  echo "unexpected second input" >&2; exit 8
else
  status=$?
  if [[ "$status" -eq 1 ]]; then echo "stdin closed before result" >&2; exit 9; fi
fi
printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"AGY_READY"}}'
cat >/dev/null
`)
	tool, _ := catalogTool("agy")
	result, err := runDelegate(context.Background(), Discoverer{}, tool, invokeTarget{Mode: HarnessTargetNative, Bin: bin}, "hello", 3*time.Second)
	if err != nil || result.ExitCode != 0 || result.TimedOut || !strings.Contains(result.Stdout, "AGY_READY") {
		t.Fatalf("native result %#v: %v", result, err)
	}
	d := fakeWSLDiscoverer(t, dir)
	result, err = d.runDelegateWSL(context.Background(), tool, "Ubuntu-24.04", "hello", 3*time.Second)
	if err != nil || result.ExitCode != 0 || result.TimedOut || !strings.Contains(result.Stdout, "AGY_READY") {
		t.Fatalf("WSL result %#v: %v", result, err)
	}
}
