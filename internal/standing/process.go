package standing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Slice-3b standing process backend.
//
// FakeBackend proves the session, turn, and streaming contract with no live
// harness. ProcessBackend is the same Backend contract backed by a real
// harness process: one subprocess per turn, resolved from PATH with a
// constant argv recipe, prompted with the outbox-frozen intent, and streamed
// back as token events. It deliberately does NOT invent a second protocol:
// the subprocess's native stdout is the turn reply, returned verbatim so the
// existing per-harness reply extractors parse it, and the Fake envelope
// wrapper (standingRunOutput in runapi) is skipped for this backend.
//
// The recipe table mirrors the delegatable entries of the desktop PATH
// catalog (internal/desktop Catalog): same binaries, same constant argv,
// same prompt transport (stdin, 0600 prompt file, or agy stream-json), and
// the same ambient-credential posture — provider credentials stay in the
// harness CLI's own auth home (for example ~/.codex/auth.json), never in a
// Kubernetes Secret. This backend never reads Secrets and gains no Secret
// RBAC; the child environment is filtered the same way the desktop delegate
// filters its own (no KUBE*/KUBERNETES_*, no OIDC/token material, no
// KUBECONFIG).
//
// Turn-based, like every standing slice: one subprocess per accepted message,
// bound to the durable turn identity (thread/turn/run). There is no
// continuously running native CLI session — session continuity across turns
// is the process-local session identity plus warm-reuse reporting, exactly
// like FakeBackend. Persistent native session resume (passing a harness
// session ID across turns) is a later slice; the snappiness win here is
// skipping the Job/Pod cold start, not skipping process start.
//
// Harness kinds without a documented prompt-safe local invoke stay Fake-only:
// hermesAgent and piAgent are inventory-only in the desktop catalog, custom
// has no local binary contract (it is an operator-owned container image),
// and an empty kind names no process at all. StreamTurn for those kinds
// fails closed so the turn path keeps today's hold behavior.

// processPromptMode selects how the frozen prompt reaches the harness CLI.
type processPromptMode string

const (
	processPromptStdin     processPromptMode = "stdin"
	processPromptFile      processPromptMode = "file"
	processPromptAgyStream processPromptMode = "agy-stream-json"
)

// processRecipe is the constant argv recipe for one harness kind. Args are
// catalog constants; nothing is derived from user input.
type processRecipe struct {
	binaries []string
	args     []string
	mode     processPromptMode
	fileFlag string
}

// processRecipes mirrors the delegatable desktop catalog entries
// (internal/desktop Catalog). Kinds absent here are Fake-only.
var processRecipes = map[string]processRecipe{
	"codex": {
		binaries: []string{"codex"},
		args:     []string{"exec", "--skip-git-repo-check"},
		mode:     processPromptStdin,
	},
	"openCode": {
		binaries: []string{"opencode"},
		args:     []string{"run"},
		mode:     processPromptStdin,
	},
	"openClaw": {
		binaries: []string{"openclaw"},
		args:     []string{"agent"},
		mode:     processPromptFile,
		fileFlag: "--message-file",
	},
	"grokBuild": {
		binaries: []string{"grok"},
		mode:     processPromptFile,
		fileFlag: "--prompt-file",
	},
	"primeAgent": {
		binaries: []string{"prime-agent"},
		args:     []string{"--print", "--mode", "json", "--no-session"},
		mode:     processPromptStdin,
	},
	"agy": {
		binaries: []string{"agy"},
		args:     []string{"--dangerously-skip-permissions", "--input-format", "stream-json", "--output-format", "stream-json"},
		mode:     processPromptAgyStream,
	},
}

// SupportedProcessKinds lists the harness kinds a ProcessBackend can execute
// as a local subprocess. Every other kind (hermesAgent, piAgent, custom,
// empty) remains Fake-only: StreamTurn fails closed and the turn keeps
// today's hold behavior.
func SupportedProcessKinds() []string {
	kinds := make([]string, 0, len(processRecipes))
	for kind := range processRecipes {
		kinds = append(kinds, kind)
	}
	return kinds
}

// defaultProcessTimeout bounds one harness subprocess. It matches the desktop
// delegate default: interactive turns should answer on chat timescales, and a
// hung CLI must fail the turn back to hold behavior, never wedge it.
const defaultProcessTimeout = 2 * time.Minute

// maxProcessOutputBytes caps one turn's captured stdout. Native JSONL
// envelopes stay parseable far below this; beyond it the turn fails closed
// so a runaway CLI cannot exhaust the API process.
const maxProcessOutputBytes = 1 << 20

// Runner executes one harness turn as a subprocess (or a test double) and
// reports stdout chunks through emit for live token streaming. It returns
// the full native stdout; the backend frames it into TokenEvents and appends
// the Done marker.
type Runner interface {
	Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error)
}

// RunnerFunc adapts a function to a Runner.
type RunnerFunc func(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error)

// Run implements Runner.
func (fn RunnerFunc) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
	return fn(ctx, harnessKind, prompt, emit)
}

// ExecRunner is the production Runner: it resolves the harness CLI on PATH
// and runs one subprocess per turn with the desktop delegate's posture
// (constant argv, filtered env, capped stderr, prompt via stdin or a 0600
// temp file). Zero value is ready to use.
type ExecRunner struct {
	// LookPath resolves a binary name. Nil uses exec.LookPath.
	LookPath func(file string) (string, error)
	// Timeout bounds one subprocess. Zero uses defaultProcessTimeout.
	Timeout time.Duration
	// Env overrides the child environment (already filtered by the caller in
	// tests). Nil inherits the filtered process environment.
	Env []string
	// Dir sets the child working directory. Empty inherits the API's.
	Dir string
}

func (r *ExecRunner) lookPath(file string) (string, error) {
	if r != nil && r.LookPath != nil {
		return r.LookPath(file)
	}
	return exec.LookPath(file)
}

func (r *ExecRunner) timeout() time.Duration {
	if r != nil && r.Timeout > 0 {
		return r.Timeout
	}
	return defaultProcessTimeout
}

// Run implements Runner.
func (r *ExecRunner) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
	recipe, ok := processRecipes[harnessKind]
	if !ok {
		return "", fmt.Errorf("standing process backend has no local recipe for harness kind %q (Fake-only)", harnessKind)
	}
	var bin string
	for _, name := range recipe.binaries {
		resolved, err := r.lookPath(name)
		if err != nil || strings.TrimSpace(resolved) == "" {
			continue
		}
		bin = resolved
		break
	}
	if bin == "" {
		return "", fmt.Errorf("standing process backend: harness CLI %q is not on PATH", strings.Join(recipe.binaries, "/"))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	args := append([]string(nil), recipe.args...)
	var stdin *strings.Reader
	switch recipe.mode {
	case processPromptStdin:
		stdin = strings.NewReader(prompt)
	case processPromptAgyStream:
		payload, _ := json.Marshal(struct {
			Event   string `json:"event"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{Event: "user", Message: struct {
			Content string `json:"content"`
		}{Content: prompt}})
		stdin = strings.NewReader(string(payload) + "\n")
	case processPromptFile:
		file, err := os.CreateTemp("", "anvil-standing-prompt-*.txt")
		if err != nil {
			return "", fmt.Errorf("standing process backend: create prompt file: %w", err)
		}
		path := file.Name()
		defer func() { _ = os.Remove(path) }()
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("standing process backend: prompt file permissions: %w", err)
		}
		if _, err := file.WriteString(prompt); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("standing process backend: write prompt file: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("standing process backend: close prompt file: %w", err)
		}
		flag := strings.TrimSpace(recipe.fileFlag)
		if flag == "" {
			return "", fmt.Errorf("standing process backend: harness kind %q is missing a prompt file flag", harnessKind)
		}
		args = append(args, flag, path)
		stdin = strings.NewReader("")
	default:
		return "", fmt.Errorf("standing process backend: harness kind %q cannot accept a prompt", harnessKind)
	}

	// #nosec G204 -- bin is a PATH-resolved catalog CLI; args are catalog constants plus a temp file path.
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Stdin = stdin
	if r != nil {
		cmd.Dir = r.Dir
	}
	cmd.Env = processChildEnv(r)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("standing process backend: open stdout pipe: %w", err)
	}
	var stderr cappedProcessBuffer
	stderr.limit = 32 << 10
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("standing process backend: start %q: %w", harnessKind, err)
	}
	var full strings.Builder
	streamErr := func() error {
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				if full.Len()+len(line) > maxProcessOutputBytes {
					return fmt.Errorf("standing process backend: harness output exceeds %d bytes", maxProcessOutputBytes)
				}
				full.WriteString(line)
				if emit != nil {
					if emitErr := emit(line); emitErr != nil {
						return emitErr
					}
				}
			}
			if err != nil {
				break
			}
		}
		return nil
	}()
	waitErr := cmd.Wait()
	if streamErr != nil {
		return "", streamErr
	}
	if runCtx.Err() != nil && runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("standing process backend: harness %q timed out: %w", harnessKind, runCtx.Err())
	}
	if waitErr != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 2048 {
			tail = tail[:2048]
		}
		if tail != "" {
			return "", fmt.Errorf("standing process backend: harness %q exited with error: %v: %s", harnessKind, waitErr, tail)
		}
		return "", fmt.Errorf("standing process backend: harness %q exited with error: %v", harnessKind, waitErr)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return full.String(), nil
}

// cappedProcessBuffer caps captured stderr so a noisy CLI cannot exhaust the
// API process. Excess bytes are dropped, never fail the turn by themselves.
type cappedProcessBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedProcessBuffer) Write(p []byte) (int, error) {
	remain := c.limit - c.buf.Len()
	if remain <= 0 {
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = c.buf.Write(p[:remain])
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedProcessBuffer) String() string {
	return c.buf.String()
}

// processChildEnv builds the harness subprocess environment: the ambient
// process env minus anything that must never cross into a model-facing
// child (cluster credentials, OIDC/token material, kubeconfig), mirroring
// the desktop delegate filter. Tests may override with ExecRunner.Env.
func processChildEnv(r *ExecRunner) []string {
	if r != nil && r.Env != nil {
		return r.Env
	}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if upper == "" {
			continue
		}
		if strings.HasPrefix(upper, "KUBE") || strings.HasPrefix(upper, "KUBERNETES_") {
			continue
		}
		switch upper {
		case "AUTHORIZATION", "ACCESS_TOKEN", "ID_TOKEN", "REFRESH_TOKEN", "OIDC_TOKEN", "BEARER_TOKEN", "KUBECONFIG", "WSLENV":
			continue
		}
		// The API process holds a database URI with embedded credentials in
		// its environment; a model-facing child must never inherit it. The
		// desktop delegate never faces this variable, so this rule is
		// standing-backend specific.
		if strings.Contains(upper, "DATABASE_URL") {
			continue
		}
		if strings.Contains(upper, "TOKEN") && (strings.Contains(upper, "OIDC") || strings.Contains(upper, "ANVIL") || strings.Contains(upper, "ACCESS")) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

type processSession struct {
	spec   SessionSpec
	handle SessionHandle
	turns  int
	// turnMu serializes subprocesses for one thread. Peer child threads own
	// their own sessions, so peer fanout still runs concurrently across
	// threads; the runapi per-turn singleflight additionally serializes
	// queue/read-refresh/recovery races on the same turn.
	turnMu sync.Mutex
}

// ProcessBackend is a standing.Backend that executes each turn as a real
// harness subprocess through a Runner. Session lifecycle (create-once,
// warm resume, suspend-on-idle) is process-local and mirrors FakeBackend so
// EnsureTurnSession behavior is identical for both backends.
type ProcessBackend struct {
	mu       sync.Mutex
	sessions map[string]*processSession
	runner   Runner
}

// NewProcessBackend returns a ProcessBackend. A nil runner uses ExecRunner
// with defaults (PATH-resolved harness CLIs, filtered env).
func NewProcessBackend(runner Runner) *ProcessBackend {
	if runner == nil {
		runner = &ExecRunner{}
	}
	return &ProcessBackend{sessions: map[string]*processSession{}, runner: runner}
}

// ReturnsNativeEnvelope reports that StreamTurn returns the harness's native
// output directly, so the runapi Fake envelope wrapper must be skipped for
// this backend.
func (b *ProcessBackend) ReturnsNativeEnvelope() bool { return true }

func processID(namespace, name string) string {
	return "process-standing-" + SessionKey(namespace, name)
}

// CreateSession creates the session or returns the existing warm session,
// mirroring FakeBackend warm-reuse semantics.
func (b *ProcessBackend) CreateSession(ctx context.Context, spec SessionSpec) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	if err := ValidateSpec(spec); err != nil {
		return SessionHandle{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := SessionKey(spec.Namespace, spec.SessionName)
	if existing, ok := b.sessions[key]; ok {
		handle := existing.handle
		handle.Warm = true
		handle.Suspended = false
		existing.handle = handle
		return handle, nil
	}
	handle := SessionHandle{
		Namespace:   spec.Namespace,
		ThreadID:    spec.ThreadID,
		SessionName: spec.SessionName,
		HarnessKind: spec.HarnessKind,
		ID:          processID(spec.Namespace, spec.SessionName),
	}
	b.sessions[key] = &processSession{spec: spec, handle: handle}
	return handle, nil
}

func (b *ProcessBackend) lookup(namespace, name string) (*processSession, string, error) {
	key := SessionKey(namespace, name)
	session, ok := b.sessions[key]
	if !ok {
		return nil, key, fmt.Errorf("%w: %s", ErrSessionNotFound, key)
	}
	return session, key, nil
}

// ResumeSession marks the session warm and counts the resume.
func (b *ProcessBackend) ResumeSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	session, _, err := b.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	session.handle.Warm = true
	session.handle.Suspended = false
	session.handle.Resumes++
	return session.handle, nil
}

// DescribeSession returns the current handle without changing lifecycle state.
func (b *ProcessBackend) DescribeSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	session, _, err := b.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	return session.handle, nil
}

// SuspendSession marks the session idle without dropping it.
func (b *ProcessBackend) SuspendSession(ctx context.Context, namespace, name string) (SessionHandle, error) {
	if err := ctx.Err(); err != nil {
		return SessionHandle{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	session, _, err := b.lookup(namespace, name)
	if err != nil {
		return SessionHandle{}, err
	}
	session.handle.Suspended = true
	return session.handle, nil
}

// StreamTurn runs one turn as a harness subprocess and streams its stdout as
// token events. It returns the full native stdout so the caller persists the
// durable turn record; streaming is delivery, not storage. Fake-only harness
// kinds, a missing CLI on PATH, a non-zero exit, or a timeout all return an
// error so the turn path keeps today's hold behavior.
func (b *ProcessBackend) StreamTurn(ctx context.Context, handle SessionHandle, turnID, prompt string, sink Sink) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(turnID) == "" {
		return "", fmt.Errorf("standing turn ID is required")
	}
	if len(prompt) > maxPromptBytes {
		return "", fmt.Errorf("standing prompt exceeds %d bytes", maxPromptBytes)
	}
	b.mu.Lock()
	session, _, err := b.lookup(handle.Namespace, handle.SessionName)
	if err != nil {
		b.mu.Unlock()
		return "", err
	}
	session.turnMu.Lock()
	defer session.turnMu.Unlock()
	runner := b.runner
	if runner == nil {
		runner = &ExecRunner{}
	}
	b.mu.Unlock()

	seq := 0
	emit := func(chunk string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sink == nil {
			seq++
			return nil
		}
		event := TokenEvent{
			ThreadID: handle.ThreadID,
			TurnID:   strings.TrimSpace(turnID),
			Seq:      seq,
			Token:    chunk,
		}
		seq++
		return sink.OnToken(ctx, event)
	}
	reply, err := runner.Run(ctx, handle.HarnessKind, prompt, emit)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(reply) == "" {
		return "", fmt.Errorf("standing process backend: harness %q returned empty output", handle.HarnessKind)
	}
	if sink != nil {
		if err := sink.OnToken(ctx, TokenEvent{
			ThreadID: handle.ThreadID,
			TurnID:   strings.TrimSpace(turnID),
			Seq:      seq,
			Done:     true,
		}); err != nil {
			return "", err
		}
	}
	b.mu.Lock()
	session.turns++
	b.mu.Unlock()
	return reply, nil
}

// Created reports how many distinct sessions exist.
func (b *ProcessBackend) Created() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sessions)
}

// Turns reports how many streamed turns the backend has served.
func (b *ProcessBackend) Turns() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, session := range b.sessions {
		total += session.turns
	}
	return total
}
