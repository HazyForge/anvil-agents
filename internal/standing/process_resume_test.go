package standing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Slice-5b persistent native session resume tests. Stub SessionRunners prove
// the record/reuse plumbing with no model call and no subprocess; stub shell
// binaries prove the real ExecRunner resume argv (codex resume subcommand,
// opencode --session, openClaw --session-key) with no credentials.

// sessionStubRunner is a resume-aware test double. RunWithResume records the
// resume id it was offered and replays per-call replies, ids, and errors;
// Run records plain-path use for the fail-closed assertions.
type sessionStubRunner struct {
	resumeSeen []string
	kindsSeen  []string
	replies    []string
	ids        []string
	errs       []error
	plainCalls int
	plainReply string
	plainErr   error
}

func (s *sessionStubRunner) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
	s.plainCalls++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.plainErr != nil {
		return "", s.plainErr
	}
	reply := s.plainReply
	if reply == "" {
		reply = "plain cold reply"
	}
	if emit != nil {
		if err := emit(reply); err != nil {
			return "", err
		}
	}
	return reply, nil
}

func (s *sessionStubRunner) RunWithResume(ctx context.Context, handle SessionHandle, turnID, prompt, resumeID string, emit func(chunk string) error) (string, string, error) {
	idx := len(s.resumeSeen)
	s.resumeSeen = append(s.resumeSeen, resumeID)
	s.kindsSeen = append(s.kindsSeen, handle.HarnessKind)
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if idx < len(s.errs) && s.errs[idx] != nil {
		return "", "", s.errs[idx]
	}
	reply := "warm reply"
	if idx < len(s.replies) {
		reply = s.replies[idx]
	}
	id := ""
	if idx < len(s.ids) {
		id = s.ids[idx]
	}
	if emit != nil {
		if err := emit(reply); err != nil {
			return "", "", err
		}
	}
	return reply, id, nil
}

func ensureResumeSession(t *testing.T, backend *ProcessBackend, threadID, kind string) SessionHandle {
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

func TestProcessBackendRecordsAndResumesNativeSession(t *testing.T) {
	t.Parallel()

	stub := &sessionStubRunner{ids: []string{"thread-uuid-1", "thread-uuid-1"}}
	backend := NewProcessBackend(stub)
	handle := ensureResumeSession(t, backend, "thread-1", "codex")
	name := handle.SessionName

	if _, ok := backend.NativeSessionID("agents", name); ok {
		t.Fatal("native id must be absent before the first turn")
	}
	if _, err := backend.StreamTurn(context.Background(), handle, "turn-1", "hello", nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(stub.resumeSeen) != 1 || stub.resumeSeen[0] != "" {
		t.Fatalf("first turn resume ids = %q, want one cold turn with an empty resume id", stub.resumeSeen)
	}
	got, ok := backend.NativeSessionID("agents", name)
	if !ok || got != "thread-uuid-1" {
		t.Fatalf("recorded native id = %q/%v, want thread-uuid-1", got, ok)
	}

	if _, err := backend.StreamTurn(context.Background(), handle, "turn-2", "again", nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if len(stub.resumeSeen) != 2 || stub.resumeSeen[1] != "thread-uuid-1" {
		t.Fatalf("resume ids = %q, want the second turn to resume thread-uuid-1", stub.resumeSeen)
	}
	if backend.Created() != 1 || backend.Turns() != 2 {
		t.Fatalf("sessions = %d turns = %d, want one standing session across two resumed turns", backend.Created(), backend.Turns())
	}
}

func TestProcessBackendResumeRotatesSessionID(t *testing.T) {
	t.Parallel()

	stub := &sessionStubRunner{ids: []string{"uuid-1", "uuid-2"}}
	backend := NewProcessBackend(stub)
	handle := ensureResumeSession(t, backend, "thread-1", "openCode")
	ctx := context.Background()

	if _, err := backend.StreamTurn(ctx, handle, "turn-1", "hello", nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-2", "again", nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if got, _ := backend.NativeSessionID("agents", handle.SessionName); got != "uuid-2" {
		t.Fatalf("recorded native id = %q, want the rotated uuid-2", got)
	}
	if stub.resumeSeen[1] != "uuid-1" {
		t.Fatalf("second turn resumed %q, want uuid-1", stub.resumeSeen[1])
	}
}

func TestProcessBackendUnsupportedHarnessSkipsResume(t *testing.T) {
	t.Parallel()

	// primeAgent runs --no-session and grokBuild/agy have no documented
	// resume surface: the turn must succeed cold through the plain Runner
	// path, record nothing, and never offer a resume id.
	for _, kind := range []string{"primeAgent", "grokBuild", "agy"} {
		stub := &sessionStubRunner{ids: []string{"must-be-ignored"}, plainReply: "cold native reply"}
		backend := NewProcessBackend(stub)
		handle := ensureResumeSession(t, backend, "thread-"+kind, kind)
		reply, err := backend.StreamTurn(context.Background(), handle, "turn-1", "hello", nil)
		if err != nil {
			t.Fatalf("kind %q turn: %v", kind, err)
		}
		if reply != "cold native reply" {
			t.Fatalf("kind %q reply = %q, want the cold turn to succeed verbatim", kind, reply)
		}
		if len(stub.resumeSeen) != 0 || stub.plainCalls != 1 {
			t.Fatalf("kind %q used resume path (resume=%v plain=%d), want plain cold Run only", kind, stub.resumeSeen, stub.plainCalls)
		}
		if _, ok := backend.NativeSessionID("agents", handle.SessionName); ok {
			t.Fatalf("kind %q recorded a native id, want fail-closed with no resume state", kind)
		}
		if SupportsNativeResume(kind) {
			t.Fatalf("kind %q reports resume support, want fail-closed", kind)
		}
	}
	for _, kind := range []string{"codex", "openCode", "openClaw"} {
		if !SupportsNativeResume(kind) {
			t.Fatalf("kind %q must report resume support", kind)
		}
	}
	for _, kind := range []string{"hermesAgent", "piAgent", "custom", ""} {
		if SupportsNativeResume(kind) {
			t.Fatalf("kind %q must stay resume-unsupported", kind)
		}
	}
}

func TestProcessBackendResumeFailureHoldsAndClears(t *testing.T) {
	t.Parallel()

	stub := &sessionStubRunner{
		ids:  []string{"uuid-1", "uuid-1", "uuid-2"},
		errs: []error{nil, fmt.Errorf("session expired"), nil},
	}
	backend := NewProcessBackend(stub)
	handle := ensureResumeSession(t, backend, "thread-1", "codex")
	ctx := context.Background()

	if _, err := backend.StreamTurn(ctx, handle, "turn-1", "hello", nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	// A rejected resume fails the turn back to hold (error), and clears the
	// stale id so the next turn re-discovers cold instead of wedging.
	if _, err := backend.StreamTurn(ctx, handle, "turn-2", "again", nil); err == nil {
		t.Fatal("expected the rejected resume to fail the turn back to hold")
	}
	if _, ok := backend.NativeSessionID("agents", handle.SessionName); ok {
		t.Fatal("stale native id must be cleared after a rejected resume")
	}
	if backend.Turns() != 1 {
		t.Fatalf("turns = %d, want the failed resume to serve no turn", backend.Turns())
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-3", "retry", nil); err != nil {
		t.Fatalf("third turn: %v", err)
	}
	if len(stub.resumeSeen) != 3 || stub.resumeSeen[0] != "" || stub.resumeSeen[1] != "uuid-1" || stub.resumeSeen[2] != "" {
		t.Fatalf("resume ids = %q, want cold, resume uuid-1, cold again", stub.resumeSeen)
	}
	if got, _ := backend.NativeSessionID("agents", handle.SessionName); got != "uuid-2" {
		t.Fatalf("re-discovered native id = %q, want uuid-2", got)
	}
}

func TestProcessBackendPlainRunnerStaysCold(t *testing.T) {
	t.Parallel()

	// A Runner without the SessionRunner extension keeps slice-3b behavior
	// exactly: warm process-local sessions, but no native resume state.
	backend := NewProcessBackend(stubRunner("reply", []string{"reply"}))
	handle := ensureResumeSession(t, backend, "thread-1", "codex")
	ctx := context.Background()
	for _, turn := range []string{"turn-1", "turn-2"} {
		if _, err := backend.StreamTurn(ctx, handle, turn, "hello", nil); err != nil {
			t.Fatalf("%s: %v", turn, err)
		}
	}
	if _, ok := backend.NativeSessionID("agents", handle.SessionName); ok {
		t.Fatal("plain Runner must never record a native id")
	}
	if backend.Turns() != 2 {
		t.Fatalf("turns = %d, want 2 cold turns", backend.Turns())
	}
}

func TestNativeResumeArgs(t *testing.T) {
	t.Parallel()

	codex := processRecipes["codex"]
	if got, ok := codex.resumeArgs("0199a213-81c0-7800-8aa1-bbab2a035a53", ""); !ok || strings.Join(got, " ") != "exec resume 0199a213-81c0-7800-8aa1-bbab2a035a53 --skip-git-repo-check --json" {
		t.Fatalf("codex resume argv = %q/%v, want the documented exec resume subcommand", got, ok)
	}
	for _, bad := range []string{"", "  ", "not a uuid!", "ses_abc", strings.Repeat("a", 129)} {
		if _, ok := codex.resumeArgs(bad, ""); ok {
			t.Fatalf("codex resumeArgs(%q) must fail closed", bad)
		}
	}

	openCode := processRecipes["openCode"]
	if got, ok := openCode.resumeArgs("ses_abc123", ""); !ok || strings.Join(got, " ") != "run --format json --session ses_abc123" {
		t.Fatalf("openCode resume argv = %q/%v, want run --session on top of the cold recipe", got, ok)
	}
	for _, bad := range []string{"", "nope", " abc", "ses with space"} {
		if _, ok := openCode.resumeArgs(bad, ""); ok {
			t.Fatalf("openCode resumeArgs(%q) must fail closed", bad)
		}
	}

	openClaw := processRecipes["openClaw"]
	if got, ok := openClaw.resumeArgs("", "standing:agents/standing-abc"); !ok || strings.Join(got, " ") != "agent --session-key standing:agents/standing-abc" {
		t.Fatalf("openClaw resume argv = %q/%v, want agent --session-key on top of the cold recipe", got, ok)
	}
	if _, ok := openClaw.resumeArgs("", ""); ok {
		t.Fatal("openClaw resumeArgs without a key must fail closed")
	}

	for _, kind := range []string{"grokBuild", "primeAgent", "agy"} {
		if _, ok := processRecipes[kind].resumeArgs("ses_abc", "key"); ok {
			t.Fatalf("kind %q must have no resume argv", kind)
		}
	}

	// Cold argv pins: codex/openCode run structured output (Job-plane parity,
	// and the only way the resume path can discover an id).
	if got := strings.Join(processRecipes["codex"].args, " "); got != "exec --skip-git-repo-check --json" {
		t.Fatalf("codex cold argv = %q, want exec --skip-git-repo-check --json", got)
	}
	if got := strings.Join(processRecipes["openCode"].args, " "); got != "run --format json" {
		t.Fatalf("openCode cold argv = %q, want run --format json", got)
	}
}

func TestExtractCodexThreadID(t *testing.T) {
	t.Parallel()

	output := `{"type":"thread.started","thread_id":"0199a213-81c0-7800-8aa1-bbab2a035a53"}` + "\n" +
		`{"type":"turn.started"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"hi"}}` + "\n"
	if got := extractCodexThreadID(output); got != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Fatalf("thread id = %q, want the thread.started uuid", got)
	}
	for _, tc := range []string{
		"",
		"formatted text without json\nmore text\n",
		`{"type":"turn.started"}` + "\n",
		`{"type":"thread.started","thread_id":"not a uuid!"}` + "\n",
		`{"type":"thread.started"}` + "\n",
	} {
		if got := extractCodexThreadID(tc); got != "" {
			t.Fatalf("extractCodexThreadID(%q) = %q, want fail-closed empty", tc, got)
		}
	}
}

func TestExtractOpencodeSessionID(t *testing.T) {
	t.Parallel()

	output := `{"type":"text","timestamp":1,"sessionID":"ses_abc123","part":{"type":"text","text":"hi"}}` + "\n"
	if got := extractOpencodeSessionID(output); got != "ses_abc123" {
		t.Fatalf("session id = %q, want ses_abc123", got)
	}
	for _, tc := range []string{
		"",
		"formatted text without json\n",
		`{"type":"text","sessionID":"nope"}` + "\n",
		`{"type":"text"}` + "\n",
	} {
		if got := extractOpencodeSessionID(tc); got != "" {
			t.Fatalf("extractOpencodeSessionID(%q) = %q, want fail-closed empty", tc, got)
		}
	}
}

func TestNativeSessionKey(t *testing.T) {
	t.Parallel()

	if got := NativeSessionKey("agents", "standing-abc"); got != "standing:agents/standing-abc" {
		t.Fatalf("session key = %q, want a stable namespaced key", got)
	}
}

// writeResumeStubBinary installs an executable stub harness CLI that logs its
// argv to logPath and replays canned stdout lines, so the real ExecRunner
// resume wiring is exercised with no model call and no credentials.
func writeResumeStubBinary(t *testing.T, name, script, logPath string, envExtra []string) *ExecRunner {
	t.Helper()
	dir := t.TempDir()
	path := writeExecutableStub(t, dir, name, script)
	env := append([]string{"ARGV_LOG=" + logPath}, envExtra...)
	return &ExecRunner{
		LookPath: func(file string) (string, error) {
			if file == name {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		Env: env,
	}
}

func readArgvLog(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func TestExecRunnerCodexResumeAcrossTurns(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$ARGV_LOG\"\n" +
		"printf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"0199a213-81c0-7800-8aa1-bbab2a035a53\"}' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"warm hello\"}}'\n"
	backend := NewProcessBackend(writeResumeStubBinary(t, "codex", script, logPath, nil))
	handle := ensureResumeSession(t, backend, "thread-1", "codex")
	ctx := context.Background()

	if _, err := backend.StreamTurn(ctx, handle, "turn-1", "hello", nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if got, _ := backend.NativeSessionID("agents", handle.SessionName); got != "0199a213-81c0-7800-8aa1-bbab2a035a53" {
		t.Fatalf("recorded thread id = %q, want the stub thread.started uuid", got)
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-2", "again", nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	invocations := readArgvLog(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %q, want two subprocess turns", invocations)
	}
	if strings.Contains(invocations[0], "resume") {
		t.Fatalf("first invocation = %q, want a cold turn without resume", invocations[0])
	}
	if !strings.Contains(invocations[1], "resume 0199a213-81c0-7800-8aa1-bbab2a035a53") {
		t.Fatalf("second invocation = %q, want codex exec resume with the recorded thread id", invocations[1])
	}
}

func TestExecRunnerOpencodeResumeAcrossTurns(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$ARGV_LOG\"\n" +
		"printf '%s\\n' '{\"type\":\"text\",\"timestamp\":1,\"sessionID\":\"ses_live1\",\"part\":{\"type\":\"text\",\"text\":\"hi\"}}'\n"
	backend := NewProcessBackend(writeResumeStubBinary(t, "opencode", script, logPath, nil))
	handle := ensureResumeSession(t, backend, "thread-1", "openCode")
	ctx := context.Background()

	if _, err := backend.StreamTurn(ctx, handle, "turn-1", "hello", nil); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if got, _ := backend.NativeSessionID("agents", handle.SessionName); got != "ses_live1" {
		t.Fatalf("recorded session id = %q, want ses_live1", got)
	}
	if _, err := backend.StreamTurn(ctx, handle, "turn-2", "again", nil); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	invocations := readArgvLog(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %q, want two subprocess turns", invocations)
	}
	if !strings.Contains(invocations[1], "--session ses_live1") {
		t.Fatalf("second invocation = %q, want opencode run --session with the recorded id", invocations[1])
	}
}

func TestExecRunnerOpenClawSessionKeyStableAcrossTurns(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "argv.log")
	// openClaw uses prompt-file mode: $5 is the temp prompt path
	// (agent --session-key <key> --message-file <path>).
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$ARGV_LOG\"\n" +
		"cat \"$5\"\n"
	backend := NewProcessBackend(writeResumeStubBinary(t, "openclaw", script, logPath, nil))
	handle := ensureResumeSession(t, backend, "thread-1", "openClaw")
	ctx := context.Background()

	for _, turn := range []string{"turn-1", "turn-2"} {
		reply, err := backend.StreamTurn(ctx, handle, turn, "prompt "+turn, nil)
		if err != nil {
			t.Fatalf("%s: %v", turn, err)
		}
		if !strings.Contains(reply, "prompt "+turn) {
			t.Fatalf("%s reply = %q, want the prompt-file content echoed by the stub", turn, reply)
		}
	}
	wantKey := NativeSessionKey("agents", handle.SessionName)
	if got, _ := backend.NativeSessionID("agents", handle.SessionName); got != wantKey {
		t.Fatalf("recorded key = %q, want the stable derived %q", got, wantKey)
	}
	invocations := readArgvLog(t, logPath)
	if len(invocations) != 2 {
		t.Fatalf("invocations = %q, want two subprocess turns", invocations)
	}
	for i, invocation := range invocations {
		if !strings.Contains(invocation, "--session-key "+wantKey) {
			t.Fatalf("invocation %d = %q, want the stable --session-key on every turn", i, invocation)
		}
	}
}

func TestExecRunnerUnsupportedKindRecordsNothing(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "argv.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$ARGV_LOG\"\n" +
		"printf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"stopReason\":\"stop\",\"content\":[{\"type\":\"text\",\"text\":\"cold\"}]}}'\n"
	backend := NewProcessBackend(writeResumeStubBinary(t, "prime-agent", script, logPath, nil))
	handle := ensureResumeSession(t, backend, "thread-1", "primeAgent")
	ctx := context.Background()

	for _, turn := range []string{"turn-1", "turn-2"} {
		if _, err := backend.StreamTurn(ctx, handle, turn, "hello", nil); err != nil {
			t.Fatalf("%s: %v", turn, err)
		}
	}
	if _, ok := backend.NativeSessionID("agents", handle.SessionName); ok {
		t.Fatal("primeAgent (--no-session) must never record a native id")
	}
	for _, invocation := range readArgvLog(t, logPath) {
		if strings.Contains(invocation, "--session ") || strings.Contains(invocation, "--session-key") || strings.Contains(invocation, "resume") {
			t.Fatalf("invocation = %q, want no resume argv for an unsupported kind", invocation)
		}
	}
}
