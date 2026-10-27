package onechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Manager struct {
	Config        Config
	Decider       Decider
	Local         Backend
	Cloud         Backend
	state         State
	cloudSessions map[string]Session
	// A non-nil scope admits only scenario-created threads, before reading history.
	cloudScope    map[string]bool
	mu            sync.Mutex
	snapshotMu    sync.RWMutex
	lastInventory []Session
	inventoryAt   time.Time
	history       []Message
}

func Open(ctx context.Context, config Config) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var cloud Backend
	if config.Cloud.BaseURL != "" {
		b, err := NewCloudBackend(ctx, config.Cloud, config.timeout(), nil, nil)
		if err != nil {
			return nil, err
		}
		cloud = b
	}
	return NewManager(config, &ClaudeDecider{Model: config.Manager.Model, Timeout: config.timeout()}, &LocalBackend{Timeout: config.timeout()}, cloud)
}

func NewManager(config Config, decider Decider, local, cloud Backend) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if decider == nil || local == nil {
		return nil, fmt.Errorf("manager requires a decider and local backend")
	}
	state, err := loadState(config.StateDir)
	if err != nil {
		return nil, err
	}
	m := &Manager{Config: config, Decider: decider, Local: local, Cloud: cloud, state: state, cloudSessions: map[string]Session{}}
	m.refreshInventorySnapshot(time.Time{})
	m.refreshHistorySnapshot()
	return m, nil
}

func (m *Manager) redact(text string) string {
	if b, ok := m.Cloud.(interface{ Redact(string) string }); ok {
		return b.Redact(text)
	}
	return text
}

// Sanitize every string, including JSON object keys, without corrupting escaping.
func (m *Manager) sanitize(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var tree any
	if err = decoder.Decode(&tree); err != nil {
		return err
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return m.redact(x)
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
			return x
		case map[string]any:
			out := map[string]any{}
			for k, v := range x {
				out[m.redact(k)] = walk(v)
			}
			return out
		default:
			return v
		}
	}
	raw, err = json.Marshal(walk(tree))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func (m *Manager) save() error {
	if err := m.sanitize(&m.state); err != nil {
		return err
	}
	return writeJSON(filepath.Join(m.Config.StateDir, "onechat.json"), m.state)
}

func (m *Manager) setup(id string) (Setup, bool) {
	for _, s := range m.Config.Setups {
		if s.ID == id {
			return s, true
		}
	}
	return Setup{}, false
}

func (m *Manager) bind(s Session) Session {
	if id := m.state.CloudBindings[s.ID]; id != "" {
		if setup, ok := m.setup(id); ok && setup.Location == "cloud" && setup.Namespace == s.Namespace && setup.Profile == s.Profile {
			s.SetupID, s.Harness = id, setup.Harness
			return s
		}
	}
	for _, setup := range m.Config.Setups {
		if setup.Location == "cloud" && setup.Namespace == s.Namespace && setup.Profile == s.Profile {
			s.SetupID, s.Harness = setup.ID, setup.Harness
			m.state.CloudBindings[s.ID] = setup.ID
			break
		}
	}
	return s
}

func (m *Manager) inventory(ctx context.Context) ([]Session, error) {
	sessions := append([]Session{}, m.state.Sessions...)
	if m.Cloud != nil {
		cloud, err := m.Cloud.List(ctx)
		if err != nil {
			return nil, err
		}
		m.cloudSessions = map[string]Session{}
		for _, s := range cloud {
			if m.cloudScope != nil && !m.cloudScope[s.ID] {
				continue
			}
			if reader, ok := m.Cloud.(interface {
				Read(context.Context, Session) (Session, error)
			}); ok {
				s, err = reader.Read(ctx, s)
				if err != nil {
					return nil, err
				}
			}
			s = m.bind(s)
			m.cloudSessions[s.ID] = s
			sessions = append(sessions, s)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	out := snapshot(sessions)
	if err := m.sanitize(&out); err != nil {
		return nil, err
	}
	m.refreshInventorySnapshot(time.Now().UTC())
	return out, nil
}

func (m *Manager) Inventory(ctx context.Context) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessions, err := m.inventory(ctx)
	if err != nil {
		return nil, err
	}
	return sessions, m.save()
}

func (m *Manager) session(id string) (Session, bool) {
	for _, s := range m.state.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	s, ok := m.cloudSessions[id]
	return s, ok
}

func (m *Manager) put(s Session) {
	if s.Location == "cloud" {
		m.cloudSessions[s.ID] = s
		m.state.CloudBindings[s.ID] = s.SetupID
		return
	}
	for i := range m.state.Sessions {
		if m.state.Sessions[i].ID == s.ID {
			m.state.Sessions[i] = s
			return
		}
	}
	m.state.Sessions = append(m.state.Sessions, s)
}

func (m *Manager) execute(ctx context.Context, a Action) (result ActionResult) {
	start := time.Now()
	result = ActionResult{Action: a, SessionID: a.SessionID, SentText: a.Message, State: "idle"}
	defer func() { result.DurationMS = time.Since(start).Milliseconds() }()
	defer func() { m.refreshInventorySnapshot(time.Now().UTC()) }()
	if a.Type == "answer" || a.Type == "ask_user" {
		result.Reply = a.Text
		return
	}
	s, ok := m.session(a.SessionID)
	var creation Receipt
	if a.Type == "create" {
		setup, exists := m.setup(a.SetupID)
		if !exists {
			result.State, result.Error = "failed", "setup no longer exists"
			return
		}
		backend := m.Local
		if setup.Location == "cloud" {
			backend = m.Cloud
		}
		if backend == nil {
			result.State, result.Error = "failed", "backend is not configured"
			return
		}
		workdir := a.Workdir
		if setup.Location == "local" {
			var err error
			workdir, err = allowedWorkdir(workdir, m.Config.Local.WorkdirRoots)
			if err != nil {
				result.State, result.Error = "failed", err.Error()
				return
			}
		}
		var err error
		s, creation, err = backend.Create(ctx, setup, a.Title, workdir)
		result.Receipt = creation
		if err != nil {
			result.State, result.Error = "failed", m.redact(err.Error())
			return
		}
		result.SessionID = s.ID
		if s.Location == "cloud" {
			if creation.Cloud == nil || creation.Cloud.Created == nil || !*creation.Cloud.Created {
				result.State, result.Error = "failed", "cloud persona create did not create a new thread"
				return
			}
			if m.cloudScope != nil {
				m.cloudScope[s.ID] = true
			}
		}
		// Retain the binding and creation receipt even if the following read fails.
		m.put(s)
		if err := m.save(); err != nil {
			result.State, result.Error = "failed", err.Error()
			return
		}
		// Read the new thread's server state before its first send.
		if reader, yes := backend.(interface {
			Read(context.Context, Session) (Session, error)
		}); yes {
			s, err = reader.Read(ctx, s)
			if err != nil {
				result.State, result.Error = "failed", m.redact(err.Error())
				return
			}
			s.SetupID, s.Harness = setup.ID, setup.Harness
		}
		m.put(s)
		if err := m.save(); err != nil {
			result.State, result.Error = "failed", err.Error()
			return
		}
		ok = true
	}
	if !ok {
		result.State, result.Error = "failed", "session no longer exists"
		return
	}
	result.SessionID, result.Harness = s.ID, s.Harness
	if a.Type == "read_status" {
		result.State = s.State
		payload, _ := json.Marshal(snapshot([]Session{s})[0])
		result.Reply = string(payload)
		if s.Location == "cloud" {
			result.Receipt.Cloud = &CloudReceipt{BaseURL: s.BaseURL, Namespace: s.Namespace, ThreadID: s.ThreadID, MessageIDs: []string{}}
		} else {
			result.Receipt.Local = &LocalReceipt{Workdir: s.Workdir, NativeSessionID: s.NativeSessionID}
		}
		return
	}
	if s.State == "running" {
		result.State, result.Error = "running", "cannot send to a running session"
		return
	}
	setup, ok := m.setup(s.SetupID)
	if !ok {
		result.State, result.Error = "failed", "session has no configured setup"
		return
	}
	if setup.Harness != s.Harness || setup.Location != s.Location {
		result.State, result.Error = "failed", "session setup changed harness or location; restore its original setup before sending"
		return
	}
	backend := m.Local
	if s.Location == "cloud" {
		backend = m.Cloud
	}
	if backend == nil {
		result.State, result.Error = "failed", "backend is not configured"
		return
	}
	if s.Location == "local" {
		if _, err := allowedWorkdir(s.Workdir, m.Config.Local.WorkdirRoots); err != nil {
			result.State, result.Error = "failed", err.Error()
			return
		}
	}
	prior := s
	s.State, s.UpdatedAt = "running", time.Now().UTC()
	if s.Location == "local" {
		s.Transcript = append(s.Transcript, Message{Role: "user", Text: a.Message, At: s.UpdatedAt})
	}
	m.put(s)
	if err := m.save(); err != nil {
		result.State, result.Error = "failed", err.Error()
		return
	}
	result, err := backend.Send(ctx, setup, prior, a.Message)
	result.Action, result.SessionID, result.Harness = a, s.ID, s.Harness
	if result.Receipt.Cloud != nil && creation.Cloud != nil {
		result.Receipt.Cloud.Created = creation.Cloud.Created
	}
	if err != nil {
		result.Error = m.redact(err.Error())
		if result.State == "" {
			result.State = "failed"
		}
	}
	s.State, s.UpdatedAt = result.State, time.Now().UTC()
	if result.Reply != "" {
		s.Transcript = append(s.Transcript, Message{Role: "assistant", Text: result.Reply, At: s.UpdatedAt})
		s.Summary = truncate(result.Reply, 600)
	}
	if result.Receipt.Local != nil && result.Receipt.Local.NativeSessionID != "" {
		s.NativeSessionID = result.Receipt.Local.NativeSessionID
	}
	if result.Receipt.Local != nil && result.Receipt.Local.Sandbox != "" {
		s.Sandbox = result.Receipt.Local.Sandbox
	}
	m.put(s)
	if err := m.save(); err != nil {
		result.Error = strings.TrimSpace(result.Error + "; persist result: " + err.Error())
	}
	return result
}

func (m *Manager) Turn(ctx context.Context, text string) (TurnTrace, error) {
	return m.TurnObserved(ctx, text, nil)
}

// TurnObserved calls observe synchronously from the turn goroutine. The observer
// must not block: clients should enqueue events and disconnect slow readers.
func (m *Manager) TurnObserved(ctx context.Context, text string, observe func(Event)) (trace TurnTrace, turnErr error) {
	if !m.mu.TryLock() {
		return trace, fmt.Errorf("a onechat turn is already running")
	}
	defer m.mu.Unlock()
	trace = m.newTurnTrace(ctx, text)
	trace.TracePath = filepath.Join(m.Config.StateDir, "traces", trace.ID+".json")
	defer func() {
		defer m.refreshHistorySnapshot()
		if turnErr != nil {
			trace.Error = m.redact(turnErr.Error())
		}
		addUsage(&trace.Totals, trace.DeciderUsage)
		addUsage(&trace.Totals, trace.ComposeUsage)
		for _, result := range trace.Actions {
			addUsage(&trace.Totals, result.Usage)
			if result.HarnessCalled {
				trace.Totals.HarnessCalls[result.Harness]++
			}
		}
		m.state.History = append(m.state.History, Message{Role: "user", Text: trace.Message, At: trace.At}, Message{Role: "assistant", Text: trace.Answer, At: time.Now().UTC()})
		m.emit(observe, "turn.answered", map[string]any{"answer": trace.Answer})
		if err := m.sanitize(&trace); err != nil {
			turnErr = errors.Join(turnErr, err)
			return
		}
		if err := m.save(); err != nil {
			turnErr = errors.Join(turnErr, err)
			trace.Error = m.redact(turnErr.Error())
		}
		if err := writeJSON(trace.TracePath, trace); err != nil {
			turnErr = errors.Join(turnErr, err)
		}
		if turnErr != nil {
			turnErr = errors.New(m.redact(turnErr.Error()))
		}
	}()
	if err := m.decide(ctx, &trace, observe); err != nil {
		return trace, err
	}

	for i, a := range trace.Decision.Actions {
		s, _ := m.session(a.SessionID)
		if a.Type == "create" {
			setup, _ := m.setup(a.SetupID)
			s.Title, s.Harness, s.Location = a.Title, setup.Harness, setup.Location
		}
		m.emit(observe, "action.started", map[string]any{"index": i, "action": a, "sessionId": a.SessionID, "sessionTitle": s.Title, "harness": s.Harness, "location": s.Location})
		trace.Actions = append(trace.Actions, m.execute(ctx, a))
		m.emit(observe, "action.finished", map[string]any{"index": i, "result": trace.Actions[len(trace.Actions)-1]})
	}
	compose := ComposeInput{Message: trace.Message, Decision: trace.Decision, Outcomes: trace.Actions}
	if err := m.sanitize(&compose); err != nil {
		return trace, err
	}
	trace.Totals.ManagerCalls++
	var err error
	trace.Answer, trace.ComposeUsage, err = m.Decider.Compose(ctx, compose)
	if len(trace.ValidationErrors) > 0 {
		trace.Answer = trace.Decision.Actions[0].Text
	}
	if err != nil {
		if len(trace.ValidationErrors) == 0 {
			trace.Answer = "I could not compose an answer. Actual outcomes:"
			for _, r := range trace.Actions {
				trace.Answer += "\n" + r.Action.Type + ": "
				if r.Error != "" {
					trace.Answer += r.Error
				} else {
					trace.Answer += r.Reply
				}
			}
		}
		return trace, err
	}
	return trace, nil
}

// DecideTurn uses the same inventory, history and validation as a full turn,
// without executing actions, composing an answer or writing state and traces.
func (m *Manager) DecideTurn(ctx context.Context, text string) (trace TurnTrace, turnErr error) {
	if !m.mu.TryLock() {
		return trace, fmt.Errorf("a onechat turn is already running")
	}
	defer m.mu.Unlock()
	trace = m.newTurnTrace(ctx, text)
	defer func() {
		addUsage(&trace.Totals, trace.DeciderUsage)
		if turnErr != nil {
			trace.Error = m.redact(turnErr.Error())
		}
		if err := m.sanitize(&trace); err != nil {
			turnErr = errors.Join(turnErr, err)
		}
		if turnErr != nil {
			turnErr = errors.New(m.redact(turnErr.Error()))
		}
	}()
	turnErr = m.decide(ctx, &trace, nil)
	return trace, turnErr
}

func (m *Manager) newTurnTrace(ctx context.Context, text string) TurnTrace {
	id, _ := ctx.Value(turnIDKey{}).(uuid.UUID)
	if id == uuid.Nil {
		id = uuid.New()
	}
	return TurnTrace{Version: "onechat.turn/v1", ID: id.String(), At: time.Now().UTC(), Message: m.redact(text), Inventory: []Session{}, Actions: []ActionResult{}, ValidationErrors: []string{}, Setups: m.Config.Setups, Totals: Totals{HarnessCalls: map[string]int{}}}
}

// The caller holds the turn lock. Both turn modes must validate the same model
// input and keep the same fallback when the proposed actions cannot run.
func (m *Manager) decide(ctx context.Context, trace *TurnTrace, observe func(Event)) error {
	var err error
	trace.Inventory, err = m.inventory(ctx)
	inventoryBody := map[string]any{"sessions": inventoryEventSessions(trace.Inventory)}
	if err != nil {
		inventoryBody["error"] = err.Error()
	}
	m.emit(observe, "turn.inventory", inventoryBody)
	if err != nil {
		trace.Answer = "I could not read the session inventory, so I have not sent any work."
		return err
	}
	trace.History = append([]Message{}, m.state.History...)
	if len(trace.History) > 10 {
		trace.History = trace.History[len(trace.History)-10:]
	}
	input := DecideInput{Instructions: ManagerInstructions, Setups: trace.Setups, Inventory: trace.Inventory, History: trace.History, Message: trace.Message}
	if err := m.sanitize(&input); err != nil {
		return err
	}
	trace.Setups, trace.Inventory, trace.History = input.Setups, input.Inventory, input.History
	trace.Totals.ManagerCalls++
	raw, usage, decideErr := m.Decider.Decide(ctx, input)
	trace.DeciderUsage = usage
	trace.RawDecision = raw
	if len(raw.Raw) > 0 {
		if json.Valid(raw.Raw) {
			trace.RawDecision = json.RawMessage(raw.Raw)
		} else {
			trace.RawDecision = string(raw.Raw)
		}
	}
	trace.ValidationErrors = ValidateDecision(raw, m.Config.Setups, trace.Inventory, m.Config.Local.WorkdirRoots)
	if decideErr != nil {
		trace.ValidationErrors = append(trace.ValidationErrors, "decider: "+m.redact(decideErr.Error()))
	}
	trace.Decision = raw
	if len(trace.ValidationErrors) > 0 {
		question := "I could not safely route this turn: " + strings.Join(trace.ValidationErrors, "; ") + ". Which session or setup and workdir should I use?"
		trace.Decision = Decision{ReasoningSummary: "The proposed decision failed validation; no session work was sent.", Confidence: 0, Actions: []Action{{Type: "ask_user", Text: question}}}
	}
	m.emit(observe, "turn.decided", map[string]any{"decision": trace.Decision, "validationErrors": trace.ValidationErrors})
	return nil
}
