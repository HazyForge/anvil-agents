package desktop

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

// Owns the browser -> real local route endpoint -> real subprocess boundary.
// Classifier judgments and the two harmless executables are explicit fixtures;
// no HTTP responses, dispatch receipts, or saved conversations are fabricated.
func TestLocalManagerBrowser(t *testing.T) {
	if os.Getenv("ANVIL_LOCAL_MANAGER_BROWSER") != "1" {
		t.Skip("set ANVIL_LOCAL_MANAGER_BROWSER=1 after building web/desktop")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(repoRoot, "web", "desktop", "dist")
	if _, err = os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Fatal("build web/desktop before the local manager browser flow")
	}
	binDir, configDir, launchDir, cameraDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	// The host may itself run inside WSL while the operator explicitly chooses
	// native PATH. The browser must honor the host's resolved target.
	t.Setenv("WSL_DISTRO_NAME", "Browser-Fixture")
	for binary, envelope := range map[string]string{
		"prime-agent": `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"Launch fixture finished in its own folder."}]}}`,
		"codex":       `{"type":"item.completed","item":{"type":"agent_message","text":"Camera fixture finished in its own folder."}}`,
	} {
		writeExec(t, filepath.Join(binDir, binary), fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then printf 'fixture 1.0\n'; exit 0; fi
cat > manager-prompt.txt
printf 'accepted\n' >> manager-invocations.txt
printf '%%s\n' '%s'
`, envelope))
	}
	server, err := NewServer(Options{
		Listen: "127.0.0.1:0", ConfigDir: configDir, UIDir: dist,
		HarnessTarget:         HarnessTargetNative,
		Discoverer:            Discoverer{Path: binDir, InsideWSL: func() bool { return true }, WSL: WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }}},
		SubjectRoutingEnabled: true, SubjectDecisionProvider: localBrowserDecider{},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(server.Handler())
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(repoRoot, "hack", "desktop-local-manager-live.browser-test.mjs"))
	command.Dir = repoRoot
	command.Env = append(os.Environ(), "ANVIL_LOCAL_MANAGER_ORIGIN="+host.URL, "ANVIL_LOCAL_MANAGER_LAUNCH_DIR="+launchDir, "ANVIL_LOCAL_MANAGER_CAMERA_DIR="+cameraDir)
	output, err := command.CombinedOutput()
	t.Logf("browser output:\n%s", output)
	if err != nil {
		t.Fatalf("browser driver: %v", err)
	}
	for _, proof := range []struct {
		dir, ownHistory, forbiddenHistory string
		wantInvocations                   int
	}{
		{launchDir, "Original launch brief", "Original camera brief", 1},
		{cameraDir, "Original camera brief", "Original launch brief", 1},
	} {
		prompt, readErr := os.ReadFile(filepath.Join(proof.dir, "manager-prompt.txt"))
		if readErr != nil || !strings.Contains(string(prompt), proof.ownHistory) || strings.Contains(string(prompt), proof.forbiddenHistory) {
			t.Fatalf("target transcript was not isolated: %s: %v", proof.dir, readErr)
		}
		calls, readErr := os.ReadFile(filepath.Join(proof.dir, "manager-invocations.txt"))
		if readErr != nil || strings.Count(string(calls), "accepted\n") != proof.wantInvocations {
			t.Fatalf("unexpected execution count in target folder: %s: %v", proof.dir, readErr)
		}
	}
}

type localBrowserDecider struct{}

func (localBrowserDecider) Decide(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	// Make the classifying stage observable before this explicit fixture responds.
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return subjectrouting.Decision{}, ctx.Err()
	case <-timer.C:
	}
	choice, intent := "launch", subjectrouting.IntentAmendRunning
	if strings.Contains(strings.ToLower(input.Content), "camera") {
		choice = "camera"
	}
	if strings.HasPrefix(input.Content, "How is ") {
		intent = subjectrouting.IntentStatus
	}
	subjects := map[string]float64{subjectrouting.NewSubject: 0, subjectrouting.NoSubject: 0}
	for _, candidate := range input.Candidates {
		subjects[candidate.ID] = 0
	}
	subjects[choice] = 1
	intents := map[string]float64{"question": 0, "amend_running": 0, "new_work": 0, "status": 0, "decide": 0, "unclear": 0}
	intents[intent] = 1
	return subjectrouting.Decision{SubjectChoice: choice, SubjectConfidence: 1, SubjectProbabilities: subjects, Intent: intent, IntentConfidence: 1, IntentProbabilities: intents, Model: "browser-fixture"}, nil
}
