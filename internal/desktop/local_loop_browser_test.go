package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

// This owns the browser-close and durable-decision UI boundary. The executable
// fixture only fails, waits for a file, or emits a public reply; the real host
// must detect the failure, ask once, admit a retry, queue work, and verify it.
// Backend tests own host restart and request-id races; this is not model-accuracy
// evidence and does not substitute a fabricated HTTP response for the runtime.
func TestLocalManagerLoopBrowser(t *testing.T) {
	if os.Getenv("ANVIL_LOCAL_LOOP_BROWSER") != "1" {
		t.Skip("set ANVIL_LOCAL_LOOP_BROWSER=1 after building web/desktop")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(repoRoot, "web", "desktop", "dist")
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Fatal("build web/desktop before the manager-loop browser flow")
	}
	binDir, failureDir, sharedDir := t.TempDir(), t.TempDir(), t.TempDir()
	failureAlias := filepath.Join(t.TempDir(), "parser-workspace")
	if err := os.Symlink(failureDir, failureAlias); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(failureDir, "fail-once"): "fail the first native invocation\n",
		filepath.Join(sharedDir, "hold"):       "release only after closing the browser page\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeExec(t, filepath.Join(binDir, "prime-agent"), `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then printf 'fixture 1.0\n'; exit 0; fi
cat > current-prompt.txt
printf 'started\n' >> invocations.txt
invocation=$(wc -l < invocations.txt | tr -d ' ')
mv current-prompt.txt "prompt-$invocation.txt"
if [ -f fail-once ]; then rm fail-once; exit 23; fi
while [ -f hold ]; do sleep 0.05; done
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"Fixture turn completed with a public reply."}]}}'
`)
	listening := make(chan string, 1)
	server, err := NewServer(Options{
		Listen: "127.0.0.1:0", ConfigDir: t.TempDir(), UIDir: dist,
		HarnessTarget:         HarnessTargetNative,
		Discoverer:            Discoverer{Path: binDir, InsideWSL: func() bool { return false }, WSL: WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }}},
		SubjectRoutingEnabled: true, SubjectDecisionProvider: loopBrowserDecider{},
		OnListen: func(address string) { listening <- address },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- server.Start(ctx) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("Desktop host did not stop after browser proof")
		}
	}()
	var address string
	select {
	case address = <-listening:
	case err := <-stopped:
		t.Fatalf("start Desktop host: %v", err)
	case <-ctx.Done():
		t.Fatal("Desktop host did not listen")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repoRoot, "hack", "desktop-local-loop.browser-test.mjs"))
	command.Dir = repoRoot
	command.Env = append(os.Environ(), "ANVIL_LOCAL_LOOP_ORIGIN=http://"+address, "ANVIL_LOCAL_LOOP_FAILURE_DIR="+failureAlias, "ANVIL_LOCAL_LOOP_SHARED_DIR="+sharedDir)
	output, err := command.CombinedOutput()
	t.Logf("browser output:\n%s", output)
	if err != nil {
		t.Fatalf("browser driver: %v", err)
	}
	for _, proof := range []struct {
		dir   string
		count int
	}{{failureDir, 4}, {sharedDir, 2}} {
		calls, err := os.ReadFile(filepath.Join(proof.dir, "invocations.txt"))
		if err != nil || strings.Count(string(calls), "started\n") != proof.count {
			t.Fatalf("wanted exactly %d actual native invocations in %s, got %q: %v", proof.count, proof.dir, calls, err)
		}
	}
	for _, proof := range []struct{ dir, file, required, forbidden string }{
		{failureDir, "prompt-1.txt", "Original parser history", "Original gated history"},
		{failureDir, "prompt-2.txt", "Original parser history", "Original gated history"},
		{failureDir, "prompt-3.txt", "Finish the failed parser task", "Original gated history"},
		{failureDir, "prompt-4.txt", "Direct Chat bridge note", "Original gated history"},
		{sharedDir, "prompt-1.txt", "Original gated history", "Original queued history"},
		{sharedDir, "prompt-2.txt", "Original queued history", "Original gated history"},
	} {
		prompt, err := os.ReadFile(filepath.Join(proof.dir, proof.file))
		if err != nil || !strings.Contains(string(prompt), proof.required) || strings.Contains(string(prompt), proof.forbidden) {
			t.Fatalf("native transcript boundary failed for %s: %v", proof.file, err)
		}
	}
}

type loopBrowserDecider struct{}

func (loopBrowserDecider) Decide(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	choice := "failure"
	switch {
	case strings.Contains(input.Content, "gated"):
		choice = "gated"
	case strings.Contains(input.Content, "queued"):
		choice = "queued"
	}
	subjects := map[string]float64{subjectrouting.NewSubject: 0, subjectrouting.NoSubject: 0}
	for _, candidate := range input.Candidates {
		subjects[candidate.ID] = 0
	}
	if _, ok := subjects[choice]; !ok {
		return subjectrouting.Decision{}, fmt.Errorf("fixture target was not offered")
	}
	subjects[choice] = 1
	intent := subjectrouting.IntentNewWork
	if input.Content == "retry it" {
		intent = subjectrouting.IntentDecide
	}
	intents := map[string]float64{"question": 0, "amend_running": 0, "new_work": 0, "status": 0, "decide": 0, "unclear": 0}
	intents[intent] = 1
	return subjectrouting.Decision{SubjectChoice: choice, SubjectConfidence: 1, SubjectProbabilities: subjects, Intent: intent, IntentConfidence: 1, IntentProbabilities: intents, Model: "loop-browser-fixture"}, nil
}
