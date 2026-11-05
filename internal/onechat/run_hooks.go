package onechat

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// RunHooks belongs to one harness invocation. Context keeps concurrent sessions
// from changing a shared backend's callbacks or hook command.
type RunHooks struct {
	Binary        string
	StateDir      string
	SessionID     string
	RunID         string
	Activity      func(kind, summary string)
	Started       func(pid int)
	NativeSession func(id string)
}

type runHooksKey struct{}

func WithRunHooks(ctx context.Context, hooks RunHooks) context.Context {
	return context.WithValue(ctx, runHooksKey{}, hooks)
}

type SessionHook struct {
	HookEventName   string `json:"hookEventName"`
	NativeSessionID string `json:"nativeSessionId,omitempty"`
	TranscriptPath  string `json:"transcriptPath,omitempty"`
	ToolName        string `json:"toolName,omitempty"`
	Summary         string `json:"summary,omitempty"`
	StopHookActive  bool   `json:"stopHookActive,omitempty"`
	RunID           string `json:"runId,omitempty"`
}

// ParseClaudeHook copies routing metadata and a short activity description. Tool
// responses, edit contents and write contents never enter the daemon protocol.
func ParseClaudeHook(raw []byte) (SessionHook, error) {
	var input struct {
		Event          string `json:"hook_event_name"`
		AgentID        string `json:"agent_id"`
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		ToolName       string `json:"tool_name"`
		ToolInput      struct {
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
			Command  string `json:"command"`
		} `json:"tool_input"`
		Message        string `json:"message"`
		StopHookActive bool   `json:"stop_hook_active"`
	}
	if len(raw) > outputLimit {
		return SessionHook{}, fmt.Errorf("hook input exceeds %d bytes", outputLimit)
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return SessionHook{}, fmt.Errorf("invalid Claude hook JSON")
	}
	// Subagents inherit hook settings but do not own this worker's resume id or
	// mailbox. Their reports must not consume the parent session's queued work.
	if input.AgentID != "" {
		return SessionHook{}, fmt.Errorf("subagent hook is not a session worker event")
	}
	report := SessionHook{HookEventName: input.Event, StopHookActive: input.StopHookActive}
	if validLocalResumeID(input.SessionID) {
		report.NativeSessionID = input.SessionID
	}
	report.TranscriptPath = truncate(input.TranscriptPath, 4096)
	report.ToolName = activityLine(input.ToolName, 80)
	switch input.Event {
	case "SessionStart", "Stop":
	case "Notification":
		report.Summary = activityLine(input.Message, 400)
	case "PreToolUse", "PostToolUse":
		report.Summary = report.ToolName
		if input.ToolName == "Bash" {
			report.Summary = "Bash: " + commandSummary(input.ToolInput.Command)
		} else {
			path := input.ToolInput.FilePath
			if path == "" {
				path = input.ToolInput.Path
			}
			if path != "" {
				report.Summary += " " + activityLine(path, 300)
			}
		}
	default:
		return SessionHook{}, fmt.Errorf("unsupported Claude hook event")
	}
	return report, nil
}

var activityPath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func commandSummary(command string) string {
	// Shell arguments can carry file contents and credentials, even on one line.
	// Admit only familiar build/check operations; everything else names the
	// executable without its payload. Final replies retain execution evidence.
	if end := strings.IndexAny(command, "\r\n"); end >= 0 {
		command = command[:end]
	}
	words := strings.Fields(command)
	if len(words) == 0 {
		return "command"
	}
	binary := filepath.Base(words[0])
	if !activityPath.MatchString(binary) {
		return "shell command"
	}
	if len(words) < 2 {
		return binary
	}
	operation := words[1]
	safe := map[string]string{
		"go":    " test build vet fmt list version ",
		"cargo": " test check build fmt clippy ",
		"git":   " status diff log branch show fetch pull push add commit checkout switch ",
		"npm":   " test build run ",
		"pnpm":  " test build lint typecheck run ",
		"yarn":  " test build lint typecheck run ",
	}
	if binary == "node" && operation == "--check" && len(words) > 2 && activityPath.MatchString(words[2]) {
		return activityLine("node --check "+words[2], 300)
	}
	if (binary == "python" || binary == "python3") && operation == "-m" && len(words) > 2 && (words[2] == "unittest" || words[2] == "pytest") {
		return binary + " -m " + words[2]
	}
	if allowed := safe[binary]; allowed != "" && strings.Contains(allowed, " "+operation+" ") {
		summary := binary + " " + operation
		if operation == "run" && len(words) > 2 {
			switch words[2] {
			case "test", "build", "lint", "typecheck":
				summary += " " + words[2]
			}
		}
		return summary
	}
	return binary
}

func activityLine(text string, limit int) string {
	return truncate(strings.Join(strings.Fields(text), " "), limit)
}

func claudeHookSettings(hooks RunHooks) (string, error) {
	if !filepath.IsAbs(hooks.Binary) || !filepath.IsAbs(hooks.StateDir) || hooks.SessionID == "" {
		return "", fmt.Errorf("Claude hooks require the absolute onechat binary, state directory and session id")
	}
	args := []string{hooks.Binary, "hook", "claude", "--session", hooks.SessionID, "--state-dir", hooks.StateDir}
	if hooks.RunID != "" {
		args = append(args, "--run-id", hooks.RunID)
	}
	for i, arg := range args {
		args[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	command := strings.Join(args, " ")
	type hookCommand struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type hookEntry struct {
		Hooks []hookCommand `json:"hooks"`
	}
	settings := struct {
		Hooks map[string][]hookEntry `json:"hooks"`
	}{Hooks: map[string][]hookEntry{}}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Notification", "Stop"} {
		settings.Hooks[event] = []hookEntry{{Hooks: []hookCommand{{Type: "command", Command: command, Timeout: 5}}}}
	}
	raw, err := json.Marshal(settings)
	return string(raw), err
}
