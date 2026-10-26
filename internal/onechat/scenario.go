package onechat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

type Scenario struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Workspaces  map[string]struct {
		Fixture string `json:"fixture"`
	} `json:"workspaces"`
	Setup []struct {
		Session struct {
			Label     string `json:"label"`
			Setup     string `json:"setup"`
			Title     string `json:"title"`
			Workspace string `json:"workspace"`
		} `json:"session"`
		Send []string `json:"send"`
	} `json:"setup"`
	Turns []struct {
		Say    string       `json:"say"`
		Expect *Expectation `json:"expect,omitempty"`
	} `json:"turns"`
}

type Expectation struct {
	Action  string `json:"action"`
	Session string `json:"session"`
	Setup   string `json:"setup"`
}
type ScenarioTrace struct {
	StartedAt   time.Time         `json:"startedAt"`
	FinishedAt  time.Time         `json:"finishedAt"`
	Version     string            `json:"version"`
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	StateDir    string            `json:"stateDir"`
	Workspaces  map[string]string `json:"workspaces"`
	Sessions    map[string]string `json:"sessions"`
	Setup       []ActionResult    `json:"setup"`
	Turns       []TurnTrace       `json:"turns"`
	Totals      Totals            `json:"totals"`
	Error       string            `json:"error"`
	TracePath   string            `json:"tracePath"`
}

func LoadScenario(path string) (Scenario, error) {
	var s Scenario
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = yaml.UnmarshalStrict(b, &s)
	return s, err
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// RunScenario starts from a new state directory, including when a name is rerun.
// The factory is also the application boundary for offline backend substitutes.
func RunScenario(ctx context.Context, config Config, scenario Scenario, out string, factory func(context.Context, Config) (*Manager, error)) (report ScenarioTrace, err error) {
	startedAt := time.Now().UTC()
	// Resolve the person's session file before replacing stateDir. Scenarios
	// isolate work and history, while all reuse the same signed-in credentials.
	if err = config.Validate(); err != nil {
		return report, err
	}
	if !safeName.MatchString(scenario.Name) {
		return report, fmt.Errorf("scenario name must contain only letters, digits, underscores or hyphens")
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return report, err
	}
	if err = os.MkdirAll(out, 0o700); err != nil {
		return report, err
	}
	dir, err := os.MkdirTemp(out, scenario.Name+"-state-")
	if err != nil {
		return report, err
	}
	config.StateDir = dir
	report = ScenarioTrace{Version: "onechat.scenario/v1", Name: scenario.Name, Title: scenario.Title, Description: scenario.Description, StateDir: dir, Workspaces: map[string]string{}, Sessions: map[string]string{}, Setup: []ActionResult{}, Turns: []TurnTrace{}, TracePath: filepath.Join(out, scenario.Name+".json"), Totals: Totals{HarnessCalls: map[string]int{}}}
	report.StartedAt = startedAt
	var manager *Manager
	defer func() {
		report.FinishedAt = time.Now().UTC()
		if err != nil {
			report.Error = err.Error()
		}
		for _, r := range report.Setup {
			addUsage(&report.Totals, r.Usage)
			if r.HarnessCalled {
				report.Totals.HarnessCalls[r.Harness]++
			}
		}
		for _, turn := range report.Turns {
			addUsage(&report.Totals, turn.DeciderUsage)
			addUsage(&report.Totals, turn.ComposeUsage)
			report.Totals.ManagerCalls += turn.Totals.ManagerCalls
			for _, r := range turn.Actions {
				addUsage(&report.Totals, r.Usage)
				if r.HarnessCalled {
					report.Totals.HarnessCalls[r.Harness]++
				}
			}
		}
		if manager != nil {
			if err != nil {
				err = errors.New(manager.redact(err.Error()))
			}
			if e := manager.sanitize(&report); e != nil {
				err = e
				return
			}
		}
		if e := writeJSON(report.TracePath, report); e != nil {
			err = e
		}
	}()
	for name, workspace := range scenario.Workspaces {
		if !safeName.MatchString(name) {
			return report, fmt.Errorf("invalid workspace name")
		}
		dest := filepath.Join(dir, "work", name)
		if err = copyFixture(workspace.Fixture, dest); err != nil {
			return report, err
		}
		report.Workspaces[name] = dest
		config.Local.WorkdirRoots = append(config.Local.WorkdirRoots, dest)
	}
	if factory == nil {
		factory = Open
	}
	manager, err = factory(ctx, config)
	if err != nil {
		return report, err
	}
	manager.cloudScope = map[string]bool{}
	for _, step := range scenario.Setup {
		if step.Session.Label == "" || report.Sessions[step.Session.Label] != "" || len(step.Send) == 0 {
			return report, fmt.Errorf("setup requires a unique label and at least one send")
		}
		setup, ok := manager.setup(step.Session.Setup)
		if !ok {
			return report, fmt.Errorf("scenario setup does not exist: %s", step.Session.Setup)
		}
		workdir := report.Workspaces[step.Session.Workspace]
		if setup.Location == "local" && workdir == "" {
			return report, fmt.Errorf("local scenario setup needs a copied workspace")
		}
		for i, text := range step.Send {
			if strings.TrimSpace(text) == "" {
				return report, fmt.Errorf("scenario setup messages must be nonempty")
			}
			action := Action{Type: "send", SessionID: report.Sessions[step.Session.Label], Message: text}
			if i == 0 {
				action = Action{Type: "create", SetupID: setup.ID, Title: step.Session.Title, Workdir: workdir, Message: text}
			}
			result := manager.execute(ctx, action)
			result.ScenarioLabel, result.SessionTitle = step.Session.Label, step.Session.Title
			if session, ok := manager.session(result.SessionID); ok {
				result.SessionTitle = session.Title
			}
			report.Setup = append(report.Setup, result)
			if result.Error != "" {
				return report, fmt.Errorf("scenario setup: %s", result.Error)
			}
			report.Sessions[step.Session.Label] = result.SessionID
		}
	}
	failed := false
	for _, step := range scenario.Turns {
		trace, turnErr := manager.Turn(ctx, step.Say)
		if step.Expect != nil {
			trace.Check = checkExpectation(trace, *step.Expect, report.Sessions)
			if !trace.Check.Pass {
				failed = true
			}
			if e := writeJSON(trace.TracePath, trace); e != nil {
				return report, e
			}
		}
		report.Turns = append(report.Turns, trace)
		if turnErr != nil {
			return report, turnErr
		}
	}
	if failed {
		return report, fmt.Errorf("one or more scenario expectations failed")
	}
	return report, nil
}

func checkExpectation(trace TurnTrace, expect Expectation, labels map[string]string) *Check {
	c := &Check{Pass: false, Notes: []string{}}
	if expect.Action == "create" && expect.Setup == "" {
		c.Notes = append(c.Notes, "create expectation requires setup")
		return c
	}
	if expect.Action == "send" && expect.Session == "" {
		c.Notes = append(c.Notes, "send expectation requires session label")
		return c
	}
	if expect.Session != "" && labels[expect.Session] == "" {
		c.Notes = append(c.Notes, "unknown expected session label: "+expect.Session)
		return c
	}
	for i, result := range trace.Actions {
		if (expect.Action == "" || result.Action.Type == expect.Action) && (expect.Session == "" || result.SessionID == labels[expect.Session]) && (expect.Setup == "" || result.Action.SetupID == expect.Setup) && result.Error == "" {
			c.Pass = true
			c.Notes = append(c.Notes, fmt.Sprintf("matched action %d: type=%s session=%s setup=%s", i+1, result.Action.Type, result.SessionID, result.Action.SetupID))
			break
		}
	}
	if trace.Error != "" {
		c.Pass = false
	}
	if c.Pass {
		c.Notes = append(c.Notes, "observed action and session matched the expectation")
	} else {
		c.Notes = append(c.Notes, fmt.Sprintf("expected action=%s session=%s setup=%s; inspect actual actions", expect.Action, expect.Session, expect.Setup))
	}
	return c
}

func copyFixture(source, dest string) error {
	root, err := canonicalDir(source)
	if err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	if rel, e := filepath.Rel(root, dest); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace destination cannot be inside its fixture")
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixtures cannot contain symlinks")
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture contains a non-regular file")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info.Mode()&0o111 != 0 {
			mode = 0o700
		}
		out, err := os.OpenFile(filepath.Join(dest, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
