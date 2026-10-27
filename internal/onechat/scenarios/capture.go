package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
	"sigs.k8s.io/yaml"
)

type CaptureOptions struct {
	TurnID    string
	Expect    []string
	Note      string
	State     string
	Out       string
	AllowRepo bool
}

func Capture(config onechat.Config, opts CaptureOptions) (string, error) {
	id, err := uuid.Parse(opts.TurnID)
	if err != nil || len(opts.TurnID) != 36 || !strings.EqualFold(id.String(), opts.TurnID) {
		return "", fmt.Errorf("turn must be a canonical UUID")
	}
	if opts.Out == "" {
		return "", fmt.Errorf("capture requires --out DIR")
	}
	raw, err := os.ReadFile(filepath.Join(config.StateDir, "traces", id.String()+".json"))
	if err != nil {
		return "", fmt.Errorf("read turn trace: %w", err)
	}
	var trace onechat.TurnTrace
	if err := json.Unmarshal(raw, &trace); err != nil {
		return "", fmt.Errorf("read turn trace: %w", err)
	}
	expect, err := captureExpectation(opts.Expect)
	if err != nil {
		return "", err
	}
	state := opts.State
	if state == "" {
		state = "idle"
	}
	world := captureWorld(config, trace)
	suite := Suite{
		Name: "capture-" + id.String(), State: state, World: world,
		Cases: []Case{{Name: "captured-turn", Say: trace.Message, Expect: &expect, Note: opts.Note}},
	}
	if err := ValidateSuite(suite); err != nil {
		return "", fmt.Errorf("capture: %w", err)
	}
	data, err := yaml.Marshal(suite)
	if err != nil {
		return "", err
	}
	out, err := captureDirectory(opts.Out, opts.AllowRepo)
	if err != nil {
		return "", err
	}
	if err := privateDirectory(out); err != nil {
		return "", err
	}
	path := filepath.Join(out, suite.Name+".yaml")
	if err := writePrivateFile(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func captureExpectation(specs []string) (Expectation, error) {
	var expect Expectation
	if len(specs) == 0 {
		return expect, fmt.Errorf("capture requires at least one --expect SPEC")
	}
	for _, spec := range specs {
		kind, value, colon := strings.Cut(spec, ":")
		switch {
		case !colon && (kind == "answer" || kind == "ask_user"):
			expect.Does = append(expect.Does, Matcher{Action: kind})
		case colon && value != "" && (kind == "send" || kind == "read_status"):
			expect.Does = append(expect.Does, Matcher{Action: kind, Session: value})
		case colon && value != "" && kind == "create":
			expect.Does = append(expect.Does, Matcher{Action: kind, Setup: value})
		case colon && value != "" && kind == "not":
			expect.Not = append(expect.Not, value)
		default:
			return expect, fmt.Errorf("invalid expectation %q; use send:LABEL, read_status:LABEL, create:SETUP, answer, ask_user or not:TYPE", spec)
		}
	}
	return expect, nil
}

func captureWorld(config onechat.Config, trace onechat.TurnTrace) World {
	// A captured suite needs routing metadata, never credentials or commands.
	cloud := onechat.CloudConfig{BaseURL: config.Cloud.BaseURL, Namespaces: config.Cloud.Namespaces}
	config.Cloud = cloud
	world := World{Setups: config.Setups, Backend: cloud, ConfigData: config}
	world.History = captureMessages(trace.History)
	labels := map[string]bool{}
	for _, session := range trace.Inventory {
		base := sessionLabel(session.Title)
		label := base
		for suffix := 2; labels[label]; suffix++ {
			label = fmt.Sprintf("%s-%d", base, suffix)
		}
		labels[label] = true
		s := session
		world.Sessions = append(world.Sessions, SessionSeed{
			Label: label, Location: &s.Location, Setup: &s.SetupID, Harness: &s.Harness,
			Title: &s.Title, Summary: &s.Summary, State: &s.State, Workdir: &s.Workdir,
			CreatedAt: &s.CreatedAt, UpdatedAt: &s.UpdatedAt, Transcript: captureMessages(s.Transcript),
			BaseURL: &s.BaseURL, Namespace: &s.Namespace, ThreadID: &s.ThreadID, Profile: &s.Profile,
			NativeSessionID: &s.NativeSessionID, Sandbox: &s.Sandbox,
		})
	}
	return world
}

func captureMessages(messages []onechat.Message) []MessageSeed {
	out := make([]MessageSeed, 0, len(messages))
	for _, message := range messages {
		at := message.At
		out = append(out, MessageSeed{Role: message.Role, Text: message.Text, At: &at})
	}
	return out
}

func sessionLabel(title string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func captureDirectory(path string, allowRepo bool) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Resolve existing ancestors before walking for .git, including when a
	// symlink outside a repository points into its work tree.
	ancestor := abs
	var missing []string
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	if !allowRepo {
		for dir := resolved; ; dir = filepath.Dir(dir) {
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				return "", fmt.Errorf("captured suites contain private data; choose --out outside the git work tree or pass --allow-repo")
			} else if !os.IsNotExist(err) {
				return "", err
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return resolved, nil
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func writePrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".onechat-scenario-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
