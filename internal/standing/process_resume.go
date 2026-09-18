package standing

import (
	"context"
	"encoding/json"
	"strings"
)

// Slice-5b persistent native session resume.
//
// ProcessBackend keeps one subprocess per turn (no daemon harness), but where
// a harness CLI documents a durable/native session surface, the backend now
// passes a resume token across turns for the same standing session (thread)
// so warm turns keep model context beyond the process-local EnsureTurnSession
// identity. The turn-based model does not move: one append-only AgentRun per
// accepted message, frozen outbox intent in, Succeeded completion out.
//
// Verified resume surfaces (flags checked against the desktop PATH catalog
// recipes in internal/desktop, the Job runner images in docker/agent-run-*,
// and the CLIs' documented `--help`/reference):
//
//   - codex: `codex exec resume <SESSION_ID>` reopens a non-interactive exec
//     session by its uuid; the follow-up prompt still travels on stdin and
//     `--json` keeps the native envelope the codex extractor parses. The
//     thread id is discovered from the `thread.started` JSONL event
//     (`thread_id`), which the codex exec reference documents as resumable.
//     Cold argv gains `--json` to match the Job runner image
//     (docker/agent-run-codex), which already runs `exec --json`.
//   - openCode: `opencode run --session <ses_ID>` continues a session by id
//     (`--session`/`-s`, "Session ID to continue"; ids start with `ses`, and
//     an unknown id exits non-zero). Cold argv gains `--format json` to match
//     the Job runner image (docker/agent-run-opencode); every json event
//     carries the top-level `sessionID`, which is how the id is discovered.
//     The Job plane explicitly forbids `--session`/`--continue` as operator
//     additional args (hack/test-opencode-runner.sh), confirming the flags
//     exist and stay controller-owned there.
//   - openClaw: `openclaw agent --session-key <key>` pins the turn to a
//     client-assigned session key (docker/agent-run-openclaw passes a fresh
//     `--session-key` per AgentRun). The standing backend derives one stable
//     key per standing session (NativeSessionKey), so every turn for the
//     thread resumes the same native session with no output parsing.
//
// Everything else fails closed: grokBuild has no documented resume flag,
// primeAgent explicitly runs `--no-session`, agy documents single-turn
// execution with no persistent conversation, and hermesAgent/piAgent/custom
// stay Fake-only with no local recipe at all. A turn for those kinds runs
// exactly like slice 3b (cold subprocess, no resume argv, no recorded id)
// and still succeeds; only the native-context win is skipped.
//
// Failure posture: a resume attempt that the CLI rejects (expired id, unknown
// flag on an older binary) returns an error so the turn keeps today's hold
// behavior — there is deliberately no silent cold retry inside the failed
// turn. The backend clears the stale recorded id on that path so the NEXT
// turn goes cold and re-discovers, instead of wedging every future warm turn
// on a dead session.

// SessionRunner is an optional Runner extension for CLIs with a documented
// durable/native session surface. The backend prefers it over plain Runner
// exactly when the harness kind supports native resume
// (SupportsNativeResume); plain Runners keep the slice-3b cold behavior
// byte-identical.
//
// resumeID is the id recorded from the session's previous turn (empty on the
// first turn). The runner passes it with the kind's documented resume
// argv — or ignores it when empty/invalid and runs a cold turn instead — and
// reports the id the CLI actually served under as newSessionID (empty when
// the kind carries no discoverable id). A failing turn returns err and the
// turn keeps hold behavior; newSessionID is ignored on error.
type SessionRunner interface {
	Runner
	RunWithResume(ctx context.Context, handle SessionHandle, turnID, prompt, resumeID string, emit func(chunk string) error) (reply, newSessionID string, err error)
}

// nativeResumeMode selects how a recipe resumes a native session. The zero
// value disables resume: the kind runs cold and records nothing.
type nativeResumeMode int

const (
	resumeUnsupported nativeResumeMode = iota
	// resumeCodexSubcommand resumes via `codex exec resume <uuid>` and
	// discovers the thread id from the `thread.started` JSONL event.
	resumeCodexSubcommand
	// resumeSessionFlag resumes via `opencode run --session <ses_ID>` and
	// discovers the id from the top-level `sessionID` of `--format json`
	// events.
	resumeSessionFlag
	// resumeSessionKey resumes via `openclaw agent --session-key <key>`
	// with a backend-derived stable key; no output parsing is needed.
	resumeSessionKey
)

// SupportsNativeResume reports whether the harness kind has a verified
// native resume surface. Every other kind (Fake-only kinds, grokBuild,
// primeAgent, agy) fails closed to the slice-3b cold turn.
func SupportsNativeResume(harnessKind string) bool {
	recipe, ok := processRecipes[harnessKind]
	return ok && recipe.resume != resumeUnsupported
}

// NativeSessionKey derives the stable client-assigned session key for kinds
// that resume by key (openClaw). It is namespaced like every other session
// identity and carries no credentials.
func NativeSessionKey(namespace, name string) string {
	return "standing:" + SessionKey(namespace, name)
}

// validSessionKeyValue bounds the client-assigned session key to a single
// safe argv token.
func validSessionKeyValue(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 256 {
		return false
	}
	return !strings.ContainsAny(key, " \t\n\r")
}

// validCodexSessionID bounds a codex resume id to the documented SESSION_ID
// shape (uuid: hex and dashes, single argv token).
func validCodexSessionID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r >= '0' && r <= '9' {
			continue
		}
		if r >= 'a' && r <= 'f' {
			continue
		}
		if r >= 'A' && r <= 'F' {
			continue
		}
		if r == '-' {
			continue
		}
		return false
	}
	return true
}

// validOpencodeSessionID bounds an opencode resume id to the documented
// `--session` shape: an existing session id, which opencode validates with
// `starts_with "ses"`.
func validOpencodeSessionID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return false
	}
	if !strings.HasPrefix(id, "ses") {
		return false
	}
	return !strings.ContainsAny(id, " \t\n\r\"';&|$`\\")
}

// resumeArgs returns the argv replacing the cold recipe args when resuming a
// native session. It reports false (fail closed: run the cold turn) when the
// kind has no resume surface or the recorded id/key is missing or malformed.
// Returned argv elements are catalog constants plus the validated id/key —
// never derived from prompt text — and are exec'd without a shell.
func (r processRecipe) resumeArgs(resumeID, sessionKey string) ([]string, bool) {
	switch r.resume {
	case resumeCodexSubcommand:
		id := strings.TrimSpace(resumeID)
		if !validCodexSessionID(id) {
			return nil, false
		}
		if len(r.args) == 0 || r.args[0] != "exec" {
			return nil, false
		}
		// `codex exec resume <SESSION_ID>`: the follow-up prompt still
		// travels on stdin; the remaining exec flags carry over unchanged.
		out := []string{"exec", "resume", id}
		return append(out, r.args[1:]...), true
	case resumeSessionFlag:
		id := strings.TrimSpace(resumeID)
		if !validOpencodeSessionID(id) {
			return nil, false
		}
		return append(append([]string(nil), r.args...), "--session", id), true
	case resumeSessionKey:
		key := strings.TrimSpace(sessionKey)
		if !validSessionKeyValue(key) {
			return nil, false
		}
		return append(append([]string(nil), r.args...), "--session-key", key), true
	default:
		return nil, false
	}
}

// extractCodexThreadID scans codex `--json` output for the documented first
// event `{"type":"thread.started","thread_id":"<uuid>"}`. Anything else —
// formatted output, truncated streams, unknown shapes — yields "" so the
// backend simply has no id to resume with yet.
func extractCodexThreadID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type != "thread.started" {
			continue
		}
		if validCodexSessionID(event.ThreadID) {
			return strings.TrimSpace(event.ThreadID)
		}
	}
	return ""
}

// extractOpencodeSessionID scans `opencode run --format json` output for the
// top-level `sessionID` every json event carries. Ids that fail opencode's
// own `starts_with "ses"` shape are ignored so a garbage line can never
// become a resume token.
func extractOpencodeSessionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if validOpencodeSessionID(event.SessionID) {
			return strings.TrimSpace(event.SessionID)
		}
	}
	return ""
}

// discoverNativeSessionID reads the served native session id back from a
// successful turn's output (or derives the stable key for key-based kinds).
// It returns "" when the kind carries no discoverable id, so the backend
// records nothing and the next turn goes cold again.
func discoverNativeSessionID(recipe processRecipe, handle SessionHandle, output string) string {
	switch recipe.resume {
	case resumeCodexSubcommand:
		return extractCodexThreadID(output)
	case resumeSessionFlag:
		return extractOpencodeSessionID(output)
	case resumeSessionKey:
		key := NativeSessionKey(handle.Namespace, handle.SessionName)
		if !validSessionKeyValue(key) {
			return ""
		}
		return key
	default:
		return ""
	}
}
