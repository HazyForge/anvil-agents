package standing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Slice-3b process backend tests. The stub Runner path never spawns a model
// or touches credentials; the ExecRunner integration path uses a stub shell
// binary on a temp PATH, never a real harness CLI.

func stubRunner(output string, chunks []string) Runner {
	return RunnerFunc(func(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		for _, chunk := range chunks {
			if err := emit(chunk); err != nil {
				return "", err
			}
		}
		return output, nil
	})
}

func ensureProcessSession(t *testing.T, backend *ProcessBackend, threadID, kind string) SessionHandle {
	t.Helper()
	handle, _, err := EnsureTurnSession(context.Background(), backend, SessionSpec{
		Namespace:   "agents",
		ThreadID:    threadID,
		SessionName: SessionNameForThread(threadID),
		HarnessKind: kind,
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return handle
}

func TestProcessBackendReturnsNativeEnvelope(t *testing.T) {
	t.Parallel()

	backend := NewProcessBackend(stubRunner("out", nil))
	if !backend.ReturnsNativeEnvelope() {
		t.Fatal("ProcessBackend must report native envelopes so runapi skips the Fake wrapper")
	}
}

func TestProcessSupportedKindsLeaveInventoryOnlyFakeOnly(t *testing.T) {
	t.Parallel()

	supported := map[string]bool{}
	for _, kind := range SupportedProcessKinds() {
		supported[kind] = true
	}
	for _, kind := range []string{"codex", "openCode", "openClaw", "grokBuild", "primeAgent", "agy"} {
		if !supported[kind] {
			t.Fatalf("kind %q must have a local process recipe", kind)
		}
	}
	for _, kind := range []string{"hermesAgent", "piAgent", "custom", ""} {
		if supported[kind] {
			t.Fatalf("kind %q must stay Fake-only (no documented prompt-safe local invoke)", kind)
		}
	}
}

func TestGrokBuildRecipeAlwaysApprovesTools(t *testing.T) {
	t.Parallel()

	recipe, ok := processRecipes["grokBuild"]
	if !ok {
		t.Fatal("grokBuild recipe missing")
	}
	found := false
	for _, arg := range recipe.args {
		if arg == "--always-approve" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("grokBuild args = %q, want --always-approve so standing grok can finish inspection without a TTY", recipe.args)
	}
}

func TestProcessBackendWarmsAcrossTurns(t *testing.T) {
	t.Parallel()

	backend := NewProcessBackend(stubRunner("reply", []string{"reply"}))
	first := ensureProcessSession(t, backend, "thread-1", "openCode")
	if first.Warm || !strings.HasPrefix(first.ID, "process-standing-") {
		t.Fatalf("cold handle = %+v, want fresh process session identity", first)
	}
	second, warm, err := EnsureTurnSession(context.Background(), backend, SessionSpec{
		Namespace: "agents", ThreadID: "thread-1",
		SessionName: SessionNameForThread("thread-1"), HarnessKind: "openCode",
	})
	if err != nil || !warm || second.ID != first.ID {
		t.Fatalf("resume = %+v/%v/%v, want warm reuse of %q", second, warm, err, first.ID)
	}
	if backend.Created() != 1 {
		t.Fatalf("created = %d, want exactly one thread session", backend.Created())
	}
}

func TestProcessBackendPeerChildOwnsItsSession(t *testing.T) {
	t.Parallel()

	backend := NewProcessBackend(stubRunner("reply", []string{"reply"}))
	parent := ensureProcessSession(t, backend, "parent-thread", "openCode")
	child, warm, err := EnsureTurnSession(context.Background(), backend, SessionSpec{
		Namespace: "agents", ThreadID: "child-id",
		SessionName: SessionNameForThread("child-id"), HarnessKind: "openCode",
	})
	if err != nil {
		t.Fatalf("child ensure: %v", err)
	}
	if warm || child.ID == parent.ID {
		t.Fatalf("child = %+v warm=%v, want a cold session distinct from %q", child, warm, parent.ID)
	}
}

func TestProcessBackendStreamsNativeOutputVerbatim(t *testing.T) {
	t.Parallel()

	native := `{"type":"text","part":{"text":"hello standing"}}` + "\n"
	backend := NewProcessBackend(stubRunner(native, []string{"{\"type\":\"text\",", "\"part\":{\"text\":\"hello standing\"}}\n"}))
	handle := ensureProcessSession(t, backend, "thread-1", "openCode")

	var events []TokenEvent
	reply, err := backend.StreamTurn(context.Background(), handle, "turn-1", "review the proposal", SinkFunc(func(_ context.Context, event TokenEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if reply != native {
		t.Fatalf("reply = %q, want the native harness output verbatim (no envelope wrap)", reply)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 2 chunks plus a done marker", len(events))
	}
	last := events[len(events)-1]
	if !last.Done || last.Token != "" || last.Seq != 2 {
		t.Fatalf("last event = %+v, want an empty done marker with monotonic seq", last)
	}
	for i, event := range events {
		if event.Seq != i || event.TurnID != "turn-1" || event.ThreadID != "thread-1" {
			t.Fatalf("event %d = %+v, want ordered turn-scoped tokens", i, event)
		}
	}
	var rebuilt strings.Builder
	for _, event := range events[:len(events)-1] {
		rebuilt.WriteString(event.Token)
	}
	if rebuilt.String() != native {
		t.Fatalf("token concatenation = %q, want the native reply %q", rebuilt.String(), native)
	}
	if backend.Turns() != 1 {
		t.Fatalf("turns = %d, want 1", backend.Turns())
	}
	// A cancelled sink aborts the stream and surfaces the error.
	seen := 0
	_, err = backend.StreamTurn(context.Background(), handle, "turn-2", "again", SinkFunc(func(_ context.Context, _ TokenEvent) error {
		seen++
		return context.Canceled
	}))
	if err == nil || seen != 1 {
		t.Fatalf("cancel err = %v seen = %d, want abort at the first token", err, seen)
	}
}

func TestProcessBackendValidatesTurnInput(t *testing.T) {
	t.Parallel()

	backend := NewProcessBackend(stubRunner("reply", []string{"reply"}))
	handle := ensureProcessSession(t, backend, "thread-1", "openCode")
	ctx := context.Background()
	if _, err := backend.StreamTurn(ctx, handle, "", "prompt", nil); err == nil {
		t.Fatal("expected turn ID validation")
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-1", strings.Repeat("p", maxPromptBytes+1), nil); err == nil {
		t.Fatal("expected prompt bound validation")
	}
	unknown := SessionHandle{Namespace: "agents", ThreadID: "ghost", SessionName: SessionNameForThread("ghost"), HarnessKind: "openCode"}
	if _, err := backend.StreamTurn(ctx, unknown, "turn-1", "prompt", nil); err == nil {
		t.Fatal("expected unknown-session error")
	}
	if _, _, err := EnsureTurnSession(ctx, nil, SessionSpec{}); err == nil {
		t.Fatal("expected error without a backend")
	}
}

func TestProcessBackendFakeOnlyKindsFailClosed(t *testing.T) {
	t.Parallel()

	// Default ExecRunner, no stub: kinds without a recipe must fail before
	// any PATH lookup or subprocess, so the turn keeps hold behavior.
	backend := NewProcessBackend(nil)
	for _, kind := range []string{"hermesAgent", "piAgent", "custom", ""} {
		handle := ensureProcessSession(t, backend, "thread-"+kind, kind)
		if _, err := backend.StreamTurn(context.Background(), handle, "turn-1", "hello", nil); err == nil {
			t.Fatalf("kind %q must fail closed (Fake-only, no local recipe)", kind)
		}
	}
}

func TestProcessBackendSuspendIsBestEffort(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := NewProcessBackend(stubRunner("reply", []string{"reply"}))
	name := SessionNameForThread("thread-1")
	if _, _, err := EnsureTurnSession(ctx, backend, SessionSpec{Namespace: "agents", ThreadID: "thread-1", SessionName: name, HarnessKind: "openCode"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	off := false
	if suspended, err := SuspendIdleSession(ctx, backend, "agents", name, &off); err != nil || suspended {
		t.Fatalf("suspend with opt-out = %v/%v, want no-op", suspended, err)
	}
	if suspended, err := SuspendIdleSession(ctx, backend, "agents", name, nil); err != nil || !suspended {
		t.Fatalf("suspend with default = %v/%v, want suspend", suspended, err)
	}
	resumed, warm, err := EnsureTurnSession(ctx, backend, SessionSpec{Namespace: "agents", ThreadID: "thread-1", SessionName: name, HarnessKind: "openCode"})
	if err != nil || !warm || resumed.Suspended {
		t.Fatalf("resume after suspend = %+v/%v/%v, want warm active session", resumed, warm, err)
	}
}

// writeStubBinary installs an executable stub harness CLI that replays canned
// stdout lines, so the ExecRunner integration path spawns a real subprocess
// with no model call and no credentials.
// writeExecutableStub writes script to dir/name with mode 0700 and fsyncs
// before returning the path. WSL/overlay can otherwise surface ETXTBSY on the
// first fork/exec of a just-written stub ("text file busy").
func writeExecutableStub(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeStubBinary(t *testing.T, name, script string) *ExecRunner {
	t.Helper()
	dir := t.TempDir()
	path := writeExecutableStub(t, dir, name, script)
	return &ExecRunner{
		LookPath: func(file string) (string, error) {
			if file == name {
				return path, nil
			}
			return "", os.ErrNotExist
		},
	}
}

func TestExecRunnerStreamsStubBinaryLive(t *testing.T) {
	t.Parallel()

	native := "{\"type\":\"text\",\"part\":{\"text\":\"stub line one\"}}\n{\"type\":\"text\",\"part\":{\"text\":\"stub line two\"}}\n"
	runner := writeStubBinary(t, "opencode", "#!/bin/sh\nprintf '%s' '"+native+"'\n")
	backend := NewProcessBackend(runner)
	handle := ensureProcessSession(t, backend, "thread-1", "openCode")

	var events []TokenEvent
	reply, err := backend.StreamTurn(context.Background(), handle, "turn-1", "hello stub", SinkFunc(func(_ context.Context, event TokenEvent) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if reply != native {
		t.Fatalf("reply = %q, want the stub binary stdout verbatim", reply)
	}
	if len(events) < 2 || !events[len(events)-1].Done {
		t.Fatalf("events = %+v, want live line tokens plus a done marker", events)
	}
}

func TestExecRunnerStdinPromptReachesStubBinary(t *testing.T) {
	t.Parallel()

	runner := writeStubBinary(t, "opencode", "#!/bin/sh\ncat\n")
	backend := NewProcessBackend(runner)
	handle := ensureProcessSession(t, backend, "thread-1", "openCode")
	reply, err := backend.StreamTurn(context.Background(), handle, "turn-1", "frozen prompt bytes", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !strings.Contains(reply, "frozen prompt bytes") {
		t.Fatalf("reply = %q, want the stdin prompt echoed by the stub", reply)
	}
}

func TestExecRunnerFilePromptReachesStubBinary(t *testing.T) {
	t.Parallel()

	// grok recipe uses a 0600 prompt file plus --always-approve; the stub
	// locates --prompt-file the same way the desktop catalog tests do.
	runner := writeStubBinary(t, "grok", `#!/bin/sh
file=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--prompt-file" ]; then file="$2"; shift 2; continue; fi
  shift
done
cat "$file"
`)
	backend := NewProcessBackend(runner)
	handle := ensureProcessSession(t, backend, "thread-1", "grokBuild")
	reply, err := backend.StreamTurn(context.Background(), handle, "turn-1", "file prompt bytes", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !strings.Contains(reply, "file prompt bytes") {
		t.Fatalf("reply = %q, want the prompt-file content echoed by the stub", reply)
	}
}

func TestExecRunnerFailureKeepsHoldBehavior(t *testing.T) {
	t.Parallel()

	failing := writeStubBinary(t, "opencode", "#!/bin/sh\necho boom >&2\nexit 3\n")
	backend := NewProcessBackend(failing)
	handle := ensureProcessSession(t, backend, "thread-1", "openCode")
	if _, err := backend.StreamTurn(context.Background(), handle, "turn-1", "hello", nil); err == nil {
		t.Fatal("expected a non-zero harness exit to fail the turn back to hold")
	}
	missing := NewProcessBackend(&ExecRunner{LookPath: func(string) (string, error) { return "", os.ErrNotExist }})
	missingHandle := SessionHandle{Namespace: "agents", ThreadID: "thread-1", SessionName: SessionNameForThread("thread-1"), HarnessKind: "openCode"}
	if _, _, err := EnsureTurnSession(context.Background(), missing, SessionSpec{Namespace: "agents", ThreadID: "thread-1", SessionName: SessionNameForThread("thread-1"), HarnessKind: "openCode"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := missing.StreamTurn(context.Background(), missingHandle, "turn-1", "hello", nil); err == nil {
		t.Fatal("expected a missing harness CLI to fail the turn back to hold")
	}
}

func TestProcessChildEnvFiltersSensitiveMaterial(t *testing.T) {
	// No t.Parallel: Setenv forbids parallel tests.
	runner := &ExecRunner{Env: nil}
	_ = runner
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBE_TOKEN", "secret")
	t.Setenv("ANVIL_AGENTS_CHAT_DATABASE_URL", "postgres://secret")
	t.Setenv("OIDC_ACCESS_TOKEN", "secret")
	t.Setenv("KUBECONFIG", "/tmp/kubeconfig")
	env := processChildEnv(&ExecRunner{})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, leaked := range []string{"KUBERNETES_SERVICE_HOST", "KUBE_TOKEN", "ANVIL_AGENTS_CHAT_DATABASE_URL", "OIDC_ACCESS_TOKEN", "KUBECONFIG"} {
		if strings.Contains(joined, "\n"+leaked+"=") {
			t.Fatalf("child env leaks %s", leaked)
		}
	}
}
