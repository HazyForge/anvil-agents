package onechat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c := Config{StateDir: t.TempDir(), Setups: []Setup{{ID: "local", Location: "local", Harness: "claudeCode"}}}
	c.Local.WorkdirRoots = []string{t.TempDir()}
	return c
}

func TestOneChatDecisionValidation(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	setups := []Setup{{ID: "local", Location: "local"}, {ID: "cloud", Location: "cloud"}}
	sessions := []Session{{ID: "idle", SetupID: "local", State: "idle"}, {ID: "busy", SetupID: "local", State: "running"}, {ID: "unmapped", State: "idle"}}
	send := Action{Type: "send", SessionID: "idle", Message: "continue"}
	create := Action{Type: "create", SetupID: "local", Workdir: root, Title: "work", Message: "start"}
	base := func(actions ...Action) Decision {
		return Decision{ReasoningSummary: "Continue the relevant work.", Confidence: 0.8, Actions: actions}
	}
	cases := []struct {
		name     string
		decision Decision
		want     string
	}{
		{"send", base(send), ""},
		{"unknown intent", Decision{Intent: "delegate", Confidence: 0.8, ReasoningSummary: "Continue.", Actions: []Action{send}}, "unknown intent"},
		{"known intent", Decision{Intent: "amend_running", Confidence: 0.8, ReasoningSummary: "Continue.", Actions: []Action{send}}, ""},
		{"local create", base(create), ""},
		{"status running", base(Action{Type: "read_status", SessionID: "busy"}), ""},
		{"answer with status", base(Action{Type: "answer", Text: "hello"}, Action{Type: "read_status", SessionID: "idle"}), ""},
		{"no actions", base(), "one to three"},
		{"too many", base(send, send, send, send), "one to three"},
		{"unknown session", base(Action{Type: "send", SessionID: "missing", Message: "x"}), "session does not exist"},
		{"unknown setup", base(Action{Type: "create", SetupID: "missing", Title: "x", Message: "x"}), "setup does not exist"},
		{"running", base(Action{Type: "send", SessionID: "busy", Message: "x"}), "running"},
		{"unmapped", base(Action{Type: "send", SessionID: "unmapped", Message: "x"}), "no configured setup"},
		{"mismatched setup", base(Action{Type: "send", SessionID: "idle", SetupID: "cloud", Message: "x"}), "does not match"},
		{"answer and send", base(Action{Type: "answer", Text: "ok"}, send), "cannot be mixed"},
		{"ask and create", base(Action{Type: "ask_user", Text: "which?"}, create), "cannot be mixed"},
		{"outside root", base(Action{Type: "create", SetupID: "local", Title: "x", Message: "x", Workdir: outside}), "workdirRoots"},
		{"symlink escape", base(Action{Type: "create", SetupID: "local", Title: "x", Message: "x", Workdir: link}), "workdirRoots"},
		{"missing directory", base(Action{Type: "create", SetupID: "local", Title: "x", Message: "x", Workdir: filepath.Join(root, "missing")}), "no such file"},
		{"missing workdir", base(Action{Type: "create", SetupID: "local", Title: "x", Message: "x"}), "workdir is required"},
		{"cloud workdir", base(Action{Type: "create", SetupID: "cloud", Title: "x", Message: "x", Workdir: root}), "only for local"},
		{"noncreate workdir", base(Action{Type: "read_status", SessionID: "idle", Workdir: root}), "only for local"},
		{"duplicate", base(send, send), "duplicate send"},
		{"missing message", base(Action{Type: "send", SessionID: "idle"}), "message is required"},
		{"missing text", base(Action{Type: "answer"}), "text is required"},
		{"missing session", base(Action{Type: "read_status"}), "session_id is required"},
		{"missing setup", base(Action{Type: "create", Title: "x", Message: "x"}), "setup_id is required"},
		{"missing title", base(Action{Type: "create", SetupID: "local", Message: "x", Workdir: root}), "title and message"},
		{"create existing", base(Action{Type: "create", SessionID: "idle", SetupID: "local", Title: "x", Message: "x", Workdir: root}), "existing session"},
		{"unknown action", base(Action{Type: "destroy"}), "unknown action"},
		{"confidence", Decision{Confidence: 1.1, ReasoningSummary: "x", Actions: []Action{send}}, "confidence"},
		{"reason", Decision{Confidence: 0.5, Actions: []Action{send}}, "reasoning_summary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(ValidateDecision(tc.decision, setups, sessions, []string{root}), ";")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

type fakeDecider struct {
	decide  func(DecideInput) (Decision, error)
	compose func(ComposeInput) (string, error)
	seen    []DecideInput
}

func (f *fakeDecider) Decide(_ context.Context, in DecideInput) (Decision, Usage, error) {
	f.seen = append(f.seen, in)
	d, e := f.decide(in)
	tokens := int64(11)
	cost := 0.02
	return d, Usage{Model: "fake-manager", InputTokens: &tokens, CostUSD: &cost, DurationMS: 3}, e
}
func (f *fakeDecider) Compose(_ context.Context, in ComposeInput) (string, Usage, error) {
	text, err := f.compose(in)
	tokens := int64(7)
	return text, Usage{Model: "fake-manager", OutputTokens: &tokens, DurationMS: 2}, err
}

type fakeBackend struct {
	sessions []Session
	sends    int
	creates  int
	reused   bool
}

func (f *fakeBackend) List(context.Context) ([]Session, error) { return f.sessions, nil }
func (f *fakeBackend) Create(_ context.Context, s Setup, title, dir string) (Session, Receipt, error) {
	f.creates++
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Location: s.Location, SetupID: s.ID, Harness: s.Harness, Title: title, State: "idle", CreatedAt: now, UpdatedAt: now, Workdir: dir, BaseURL: "https://api.invalid", Namespace: s.Namespace, Profile: s.Profile, Transcript: []Message{}}
	r := Receipt{}
	if s.Location == "cloud" {
		session.ThreadID = uuid.NewString()
		created := !f.reused
		r.Cloud = &CloudReceipt{ThreadID: session.ThreadID, Created: &created}
		f.sessions = append(f.sessions, session)
	} else {
		r.Local = &LocalReceipt{Workdir: dir}
	}
	return session, r, nil
}
func (f *fakeBackend) Send(_ context.Context, s Setup, session Session, text string) (ActionResult, error) {
	f.sends++
	count := int64(3)
	cost := 0.01
	zero := 0
	r := ActionResult{SessionID: session.ID, SentText: text, Reply: "actual harness reply: " + text, State: "idle", HarnessCalled: true, Harness: s.Harness, Usage: Usage{InputTokens: &count, CostUSD: &cost, DurationMS: 1}, DurationMS: 1}
	if s.Location == "cloud" {
		r.Receipt.Cloud = &CloudReceipt{BaseURL: session.BaseURL, Namespace: session.Namespace, ThreadID: session.ThreadID, TurnID: uuid.NewString(), RunName: "run-one", MessageIDs: []string{"user-one", "assistant-one"}}
	} else {
		r.Receipt.Local = &LocalReceipt{Binary: "fake-claude", Workdir: session.Workdir, NativeSessionID: "native-one", ExitCode: &zero}
	}
	for i := range f.sessions {
		if f.sessions[i].ID == session.ID {
			f.sessions[i].Transcript = append(f.sessions[i].Transcript, Message{Role: "user", Text: text}, Message{Role: "assistant", Text: r.Reply})
		}
	}
	return r, nil
}

func TestOneChatInvalidDecisionFallback(t *testing.T) {
	c := testConfig(t)
	local := &fakeBackend{}
	d := &fakeDecider{decide: func(DecideInput) (Decision, error) {
		return Decision{ReasoningSummary: "bad route", Confidence: 0.9, Actions: []Action{{Type: "send", SessionID: "invented", Message: "do work"}}}, nil
	}, compose: func(in ComposeInput) (string, error) {
		if in.Decision.Actions[0].Type != "ask_user" || in.Outcomes[0].Action.Type != "ask_user" {
			t.Fatal("invalid decision reached composition")
		}
		return "untrusted rewrite", nil
	}}
	m, err := NewManager(c, d, local, nil)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := m.Turn(context.Background(), "continue")
	if err != nil || local.sends != 0 || local.creates != 0 || len(trace.ValidationErrors) == 0 || trace.Decision.Actions[0].Type != "ask_user" || !strings.Contains(trace.Answer, "session does not exist") {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	b, err := os.ReadFile(trace.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "invented") || !strings.Contains(string(b), "rawDecision") {
		t.Fatal("lost raw decision")
	}
	for _, path := range []string{trace.TracePath, filepath.Join(c.StateDir, "onechat.json")} {
		stat, err := os.Stat(path)
		if err != nil || stat.Mode().Perm() != 0o600 {
			t.Fatalf("private state %v %v", stat, err)
		}
	}
	reopened, err := NewManager(c, d, local, nil)
	if err != nil || len(reopened.state.History) != 2 {
		t.Fatalf("history not durable: %v", err)
	}
}

func TestOneChatInventoryTruncation(t *testing.T) {
	c := testConfig(t)
	m, err := NewManager(c, &fakeDecider{}, &fakeBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var messages []Message
	for i := 0; i < 9; i++ {
		messages = append(messages, Message{Role: "assistant", Text: strings.Repeat("界", 650)})
	}
	m.state.Sessions = []Session{{ID: "s", Transcript: messages}}
	got, err := m.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Transcript) != 6 || len([]rune(got[0].Transcript[0].Text)) != 600 || len([]rune(m.state.Sessions[0].Transcript[0].Text)) != 650 {
		t.Fatal("snapshot truncation changed owned transcript")
	}
	got[0].Transcript[0].Text = "changed"
	if m.state.Sessions[0].Transcript[3].Text == "changed" {
		t.Fatal("snapshot aliases owned transcript")
	}
}

func TestOneChatScenarioTrace(t *testing.T) {
	c := testConfig(t)
	c.Cloud = CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}, TokenCommand: []string{"fake-token"}}
	c.Setups = append(c.Setups, Setup{ID: "cloud", Location: "cloud", Harness: "codex", Namespace: "agents", Profile: "cloud-codex"})
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "invoice.txt"), []byte("fixture original"), 0o600); err != nil {
		t.Fatal(err)
	}
	var scenario Scenario
	if err := yamlScenario(fixture, &scenario); err != nil {
		t.Fatal(err)
	}
	local, cloud := &fakeBackend{}, &fakeBackend{sessions: []Session{{ID: "unrelated", Location: "cloud", Title: "must never enter inventory"}}}
	d := &fakeDecider{decide: func(in DecideInput) (Decision, error) {
		if len(in.Inventory) != 2 || in.Instructions != ManagerInstructions || len(in.History) > 10 {
			t.Fatalf("bad manager input: %+v", in)
		}
		for _, s := range in.Inventory {
			if s.ID == "unrelated" {
				t.Fatal("scenario leak")
			}
			if s.Location == "local" {
				return Decision{ReasoningSummary: "Continue Invoice CLI.", Confidence: 0.9, Actions: []Action{{Type: "send", SessionID: s.ID, Message: "add json"}}}, nil
			}
		}
		return Decision{}, errors.New("missing local")
	}, compose: func(in ComposeInput) (string, error) {
		if !strings.Contains(in.Outcomes[0].Reply, "actual harness reply") {
			t.Fatal("compose did not get real outcome")
		}
		return in.Outcomes[0].Reply, nil
	}}
	factory := func(_ context.Context, c Config) (*Manager, error) { return NewManager(c, d, local, cloud) }
	out := t.TempDir()
	report, err := RunScenario(context.Background(), c, scenario, out, factory)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Setup) != 2 || len(report.Turns) != 1 || !report.Turns[0].Check.Pass || report.Totals.HarnessCalls["claudeCode"] != 2 || report.Totals.HarnessCalls["codex"] != 1 || report.Totals.ManagerCalls != 2 {
		t.Fatalf("incomplete scenario: %+v", report)
	}
	if report.StartedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) || report.Setup[0].ScenarioLabel != "inv" || report.Setup[0].SessionTitle != "Invoice CLI" || report.Setup[1].ScenarioLabel != "trip" || report.Setup[1].SessionTitle != "Trip" {
		t.Fatal("scenario review metadata missing")
	}
	trace := report.Turns[0]
	if trace.ID == "" || trace.At.IsZero() || trace.RawDecision == nil || trace.DeciderUsage.InputTokens == nil || trace.ComposeUsage.OutputTokens == nil || trace.Actions[0].Receipt.Local.NativeSessionID != "native-one" || report.Setup[1].Receipt.Cloud.RunName != "run-one" || len(report.Setup[1].Receipt.Cloud.MessageIDs) != 2 {
		t.Fatalf("missing trace evidence: %+v", trace)
	}
	b, err := os.ReadFile(report.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ScenarioTrace
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Turns[0].Inventory, d.seen[0].Inventory) {
		t.Fatal("trace inventory differs from decider input")
	}
	copyPath := filepath.Join(report.Workspaces["invoice"], "invoice.txt")
	if err := os.WriteFile(copyPath, []byte("changed copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(fixture, "invoice.txt"))
	if err != nil || string(original) != "fixture original" {
		t.Fatal("fixture was mutated")
	}
	state, err := loadState(report.StateDir)
	if err != nil || len(state.Sessions) != 1 || len(state.CloudBindings) != 1 || len(state.Sessions[0].Transcript) != 4 || state.Sessions[0].NativeSessionID != "native-one" {
		t.Fatalf("state incomplete %+v %v", state, err)
	}
	// A second scenario with no cloud setup must not see the first one's cloud session.
	scenario.Setup = scenario.Setup[:1]
	d.decide = func(in DecideInput) (Decision, error) {
		if len(in.Inventory) != 1 {
			t.Fatal("cross-scenario leak")
		}
		return Decision{ReasoningSummary: "Invoice.", Confidence: 1, Actions: []Action{{Type: "send", SessionID: in.Inventory[0].ID, Message: "next"}}}, nil
	}
	again, err := RunScenario(context.Background(), c, scenario, out, factory)
	if err != nil || again.StateDir == report.StateDir {
		t.Fatalf("not fresh %v", err)
	}
}

func yamlScenario(fixture string, s *Scenario) error {
	// JSON is valid YAML and avoids escaping platform paths in this fixture.
	raw := map[string]any{"name": "continue-local-work", "title": "Continue local work", "description": "fixture-backed execution", "workspaces": map[string]any{"invoice": map[string]string{"fixture": fixture}}, "setup": []any{map[string]any{"session": map[string]string{"label": "inv", "setup": "local", "title": "Invoice CLI", "workspace": "invoice"}, "send": []string{"add total"}}, map[string]any{"session": map[string]string{"label": "trip", "setup": "cloud", "title": "Trip"}, "send": []string{"research"}}}, "turns": []any{map[string]any{"say": "add json", "expect": map[string]string{"action": "send", "session": "inv"}}}}
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, s)
}

func TestOneChatScenarioRejectsExistingCloudThread(t *testing.T) {
	c := testConfig(t)
	c.Cloud = CloudConfig{BaseURL: "https://api.invalid", Namespaces: []string{"agents"}, TokenCommand: []string{"fake"}}
	c.Setups = append(c.Setups, Setup{ID: "cloud", Location: "cloud", Harness: "codex", Namespace: "agents", Profile: "p"})
	var s Scenario
	if err := yamlScenario(t.TempDir(), &s); err != nil {
		t.Fatal(err)
	}
	s.Setup = s.Setup[1:]
	backend := &fakeBackend{reused: true}
	report, err := RunScenario(context.Background(), c, s, t.TempDir(), func(_ context.Context, c Config) (*Manager, error) {
		return NewManager(c, &fakeDecider{}, &fakeBackend{}, backend)
	})
	if err == nil || !strings.Contains(err.Error(), "did not create a new thread") || backend.sends != 0 || len(report.Setup) != 1 || report.Setup[0].Receipt.Cloud.Created == nil || *report.Setup[0].Receipt.Cloud.Created {
		t.Fatalf("reuse was not rejected: %+v %v", report, err)
	}
}

func TestOneChatScenarioExpectationTargets(t *testing.T) {
	trace := TurnTrace{Actions: []ActionResult{
		{Action: Action{Type: "read_status", SessionID: "session-one"}, SessionID: "session-one"},
		{Action: Action{Type: "create", SetupID: "cloud-opencode"}, SessionID: "new-session"},
		{Action: Action{Type: "send", SessionID: "session-two"}, SessionID: "session-two"},
	}}
	labels := map[string]string{"inv": "session-one", "trip": "session-two"}
	cases := []struct {
		name   string
		expect Expectation
		pass   bool
		note   string
	}{
		{"create setup", Expectation{Action: "create", Setup: "cloud-opencode"}, true, "matched action 2"},
		{"wrong setup", Expectation{Action: "create", Setup: "local-claude"}, false, ""},
		{"missing setup", Expectation{Action: "create"}, false, "requires setup"},
		{"send label", Expectation{Action: "send", Session: "trip"}, true, "matched action 3"},
		{"wrong session", Expectation{Action: "send", Session: "inv"}, false, ""},
		{"missing label", Expectation{Action: "send"}, false, "requires session label"},
		{"unknown label", Expectation{Action: "send", Session: "missing"}, false, "unknown expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := checkExpectation(trace, tc.expect, labels)
			if check.Pass != tc.pass || !strings.Contains(strings.Join(check.Notes, ";"), tc.note) {
				t.Fatalf("unexpected check %+v", check)
			}
		})
	}
	trace.Actions[1].Error = "create failed"
	if checkExpectation(trace, Expectation{Action: "create", Setup: "cloud-opencode"}, labels).Pass {
		t.Fatal("failed create passed expectation")
	}
}
