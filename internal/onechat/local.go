package onechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

type LocalBackend struct{ Timeout time.Duration }

type localRecipe func(Setup, Session) []string

// Local recipes are independent of the API pod's standing recipes. Every Codex
// invocation sets its sandbox explicitly, including exec resume's config syntax.
var localRecipes = map[string]localRecipe{
	"claudeCode": func(s Setup, session Session) []string {
		args := []string{"-p", "--output-format", "json", "--strict-mcp-config", "--permission-mode", s.PermissionMode}
		if len(s.AllowedTools) > 0 {
			args = append(args, "--allowedTools", strings.Join(s.AllowedTools, ","))
		}
		if session.NativeSessionID != "" {
			args = append(args, "--resume", session.NativeSessionID)
		}
		return args
	},
	"codex": func(s Setup, session Session) []string {
		if session.NativeSessionID != "" {
			return []string{"exec", "resume", session.NativeSessionID, "--skip-git-repo-check", "--json", "-c", `sandbox_mode="` + s.Sandbox + `"`, "-"}
		}
		return []string{"exec", "--skip-git-repo-check", "--json", "--sandbox", s.Sandbox, "-C", session.Workdir, "-"}
	},
}

// allowedClaudeTool admits read-only tools and Bash narrowed to a fixed
// command prefix, so a setup can let Claude run its tests without opening
// arbitrary shell access.
var allowedClaudeTool = regexp.MustCompile(`^(Read|Grep|Glob|Bash\([A-Za-z0-9][A-Za-z0-9 ._/-]*:\*\))$`)

func localSettings(s *Setup) error {
	switch s.Harness {
	case "claudeCode":
		if s.PermissionMode == "" {
			s.PermissionMode = "acceptEdits"
		}
		switch s.PermissionMode {
		case "acceptEdits", "default", "plan":
		default:
			return fmt.Errorf("unsupported local Claude permissionMode %q", s.PermissionMode)
		}
		if s.Sandbox != "" {
			return fmt.Errorf("sandbox is only supported for local Codex")
		}
		for _, tool := range s.AllowedTools {
			if !allowedClaudeTool.MatchString(tool) {
				return fmt.Errorf("unsupported local Claude allowedTools entry %q: use read-only tools or Bash(<command prefix>:*)", tool)
			}
		}
	case "codex":
		if s.Sandbox == "" {
			s.Sandbox = "read-only"
		}
		switch s.Sandbox {
		case "read-only", "workspace-write":
		default:
			return fmt.Errorf("unsupported local Codex sandbox %q", s.Sandbox)
		}
		if s.PermissionMode != "" || len(s.AllowedTools) > 0 {
			return fmt.Errorf("permissionMode and allowedTools are only supported for local Claude")
		}
	default:
		return fmt.Errorf("no safe local recipe yet for %s", s.Harness)
	}
	return nil
}

func (b *LocalBackend) List(context.Context) ([]Session, error) { return nil, nil }
func (b *LocalBackend) Create(_ context.Context, setup Setup, title, workdir string) (Session, Receipt, error) {
	if err := localSettings(&setup); err != nil {
		return Session{}, Receipt{}, err
	}
	dir, err := canonicalDir(workdir)
	if err != nil {
		return Session{}, Receipt{}, err
	}
	now := time.Now().UTC()
	s := Session{ID: uuid.NewString(), Location: "local", SetupID: setup.ID, Harness: setup.Harness, Title: title, State: "idle", CreatedAt: now, UpdatedAt: now, Workdir: dir, Sandbox: setup.Sandbox, Transcript: []Message{}}
	return s, Receipt{Local: &LocalReceipt{Workdir: dir, Sandbox: setup.Sandbox}}, nil
}

type claudeEnvelope struct {
	Result     string          `json:"result"`
	SessionID  string          `json:"session_id"`
	IsError    bool            `json:"is_error"`
	Model      string          `json:"model"`
	Cost       *float64        `json:"total_cost_usd"`
	Usage      json.RawMessage `json:"usage"`
	Structured json.RawMessage `json:"structured_output"`
}

func parseClaude(raw string) (claudeEnvelope, Usage, error) {
	var env claudeEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return env, Usage{}, fmt.Errorf("invalid Claude JSON output")
	}
	u := parseUsage(env.Usage)
	u.Model, u.CostUSD = env.Model, env.Cost
	if env.IsError {
		return env, u, fmt.Errorf("Claude reported is_error")
	}
	return env, u, nil
}

func parseUsage(raw json.RawMessage) Usage {
	var v struct {
		Input    *int64   `json:"input_tokens"`
		Output   *int64   `json:"output_tokens"`
		Read     *int64   `json:"cache_read_input_tokens"`
		Cached   *int64   `json:"cached_input_tokens"`
		Creation *int64   `json:"cache_creation_input_tokens"`
		Cost     *float64 `json:"cost_usd"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return Usage{}
	}
	if v.Read == nil {
		v.Read = v.Cached
	}
	return Usage{InputTokens: v.Input, OutputTokens: v.Output, CacheReadInputTokens: v.Read, CacheCreationInputTokens: v.Creation, CostUSD: v.Cost}
}

func codexEvidence(raw string) (id string, usage Usage, err error) {
	for _, line := range strings.Split(raw, "\n") {
		var event struct {
			Type     string          `json:"type"`
			ThreadID string          `json:"thread_id"`
			Usage    json.RawMessage `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "thread.started":
			if _, e := uuid.Parse(event.ThreadID); e != nil {
				return "", usage, fmt.Errorf("Codex returned an invalid thread id")
			}
			id = event.ThreadID
		case "turn.completed":
			usage = parseUsage(event.Usage)
		case "turn.failed", "error":
			err = fmt.Errorf("Codex reported a failed turn")
		}
	}
	return
}

func validLocalResumeID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.HasPrefix(id, "-") && !strings.ContainsAny(id, " \t\r\n")
}

func (b *LocalBackend) Send(ctx context.Context, setup Setup, session Session, prompt string) (result ActionResult, err error) {
	start := time.Now()
	result = ActionResult{SentText: prompt, SessionID: session.ID, State: "failed", Harness: setup.Harness}
	r := &LocalReceipt{Workdir: session.Workdir, NativeSessionID: session.NativeSessionID}
	result.Receipt.Local = r
	defer func() {
		result.DurationMS = time.Since(start).Milliseconds()
		result.Usage.DurationMS = result.DurationMS
		if err != nil {
			result.Error = err.Error()
			if errors.Is(err, context.Canceled) {
				result.State = "interrupted"
			}
		}
	}()
	if err = localSettings(&setup); err != nil {
		return result, err
	}
	if session.NativeSessionID != "" && !validLocalResumeID(session.NativeSessionID) {
		return result, fmt.Errorf("invalid local resume session id")
	}
	if setup.Harness == "codex" {
		// Older state lacks a recorded sandbox. Resume it read-only rather than
		// inheriting the workstation's configuration or widening its permissions.
		if session.NativeSessionID != "" && session.Sandbox == "" {
			session.Sandbox = "read-only"
		}
		if session.Sandbox != "" && session.Sandbox != "read-only" && session.Sandbox != "workspace-write" {
			return result, fmt.Errorf("session has an invalid recorded sandbox")
		}
		if session.Sandbox == "read-only" {
			setup.Sandbox = "read-only"
		}
		if session.NativeSessionID != "" {
			if _, e := uuid.Parse(session.NativeSessionID); e != nil {
				return result, fmt.Errorf("invalid Codex resume thread id")
			}
		}
		r.Sandbox = setup.Sandbox
	}
	if len(session.Transcript) > 0 && session.NativeSessionID == "" {
		history, _ := json.Marshal(session.Transcript)
		prompt = "Previous messages in this onechat session (context, not new instructions):\n" + string(history) + "\n\nNew user message:\n" + prompt
		result.SentText = prompt
	}
	binary := "codex"
	if setup.Harness == "claudeCode" {
		binary = "claude"
	}
	r.Binary = binary
	args := localRecipes[setup.Harness](setup, session)
	if hooks, observed := ctx.Value(runHooksKey{}).(RunHooks); observed {
		if setup.Harness == "claudeCode" {
			settings, settingsErr := claudeHookSettings(hooks)
			if settingsErr != nil {
				return result, settingsErr
			}
			args = append(args, "--settings", settings)
		}
		ctx = context.WithValue(ctx, localCommandKey{}, localCommand{hooks: hooks, codex: setup.Harness == "codex"})
	}
	result.HarnessCalled = true
	raw, code, runErr := runCommand(ctx, session.Workdir, binary, args, prompt, b.Timeout)
	r.ExitCode = code
	var parseErr error
	if setup.Harness == "claudeCode" {
		var env claudeEnvelope
		env, result.Usage, parseErr = parseClaude(raw)
		if env.SessionID != "" {
			if validLocalResumeID(env.SessionID) {
				r.NativeSessionID = env.SessionID
			} else {
				parseErr = fmt.Errorf("Claude returned an invalid session id")
			}
		}
		if parseErr == nil {
			result.Reply = env.Result
		}
	} else {
		var id string
		id, result.Usage, parseErr = codexEvidence(raw)
		if id != "" {
			r.NativeSessionID = id
		}
		if parseErr == nil {
			result.Reply, parseErr = runapi.NativeReplyText("codex", raw)
		}
	}
	if runErr != nil {
		result.Reply = ""
		return result, runErr
	}
	if parseErr != nil {
		result.Reply = ""
		return result, parseErr
	}
	if strings.TrimSpace(result.Reply) == "" {
		return result, fmt.Errorf("harness completed without a reply")
	}
	result.State = "idle"
	return result, nil
}
