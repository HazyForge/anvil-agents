package onechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *Manager) dispatchTurn(ctx context.Context, text string, observe func(Event)) (trace TurnTrace, turnErr error) {
	if !m.mu.TryLock() {
		return trace, fmt.Errorf("a onechat turn is already running")
	}
	defer m.mu.Unlock()
	id, _ := ctx.Value(turnIDKey{}).(uuid.UUID)
	if id == uuid.Nil {
		id = uuid.New()
	}
	trace = TurnTrace{Version: "onechat.turn/v1", ID: id.String(), At: time.Now().UTC(), Message: m.redact(text), Setups: m.Config.Setups, Actions: []ActionResult{}, ValidationErrors: []string{}, Totals: Totals{HarnessCalls: map[string]int{}}}
	trace.TracePath = filepath.Join(m.Config.StateDir, "traces", trace.ID+".json")
	m.stateMu.Lock()
	done := make(chan struct{})
	m.workerRuntime.dispatching[trace.ID] = done
	m.stateMu.Unlock()
	defer func() {
		m.stateMu.Lock()
		delete(m.workerRuntime.dispatching, trace.ID)
		close(done)
		m.stateMu.Unlock()
	}()
	defer func() {
		if turnErr != nil {
			trace.Error = m.redact(turnErr.Error())
		}
		addUsage(&trace.Totals, trace.DeciderUsage)
		addUsage(&trace.Totals, trace.ComposeUsage)
		m.emit(observe, "turn.answered", map[string]any{"answer": trace.Answer})
		if err := m.sanitize(&trace); err != nil {
			turnErr = errors.Join(turnErr, err)
			return
		}
		m.stateMu.Lock()
		m.state.History = append(m.state.History, Message{Role: "user", Text: trace.Message, At: trace.At}, Message{Role: "assistant", Text: trace.Answer, At: time.Now().UTC()})
		if err := m.saveWorkerStateLocked(); err != nil {
			turnErr = errors.Join(turnErr, err)
			trace.Error = m.redact(turnErr.Error())
		}
		m.stateMu.Unlock()
		if err := writeJSON(trace.TracePath, trace); err != nil {
			turnErr = errors.Join(turnErr, err)
		}
		if turnErr != nil {
			turnErr = errors.New(m.redact(turnErr.Error()))
		}
	}()
	sessions, _ := m.LastInventory()
	trace.Inventory = snapshot(sessions)
	trace.History = m.History(10)
	m.emit(observe, "turn.inventory", map[string]any{"sessions": inventoryEventSessions(trace.Inventory)})
	if err := ctx.Err(); err != nil {
		return trace, err
	}
	if err := m.decide(ctx, &trace, observe); err != nil {
		return trace, err
	}
	for i, a := range trace.Decision.Actions {
		m.stateMu.Lock()
		s, _ := m.session(a.SessionID)
		m.stateMu.Unlock()
		if a.Type == "create" {
			setup, _ := m.setup(a.SetupID)
			s.Title, s.Harness, s.Location = a.Title, setup.Harness, setup.Location
		}
		m.emit(observe, "action.started", map[string]any{"index": i, "action": a, "sessionId": a.SessionID, "sessionTitle": s.Title, "harness": s.Harness, "location": s.Location})
		result := m.dispatchAction(ctx, a, trace.ID)
		trace.Actions = append(trace.Actions, result)
		m.emit(observe, "action.finished", map[string]any{"index": i, "result": result})
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
			trace.Answer = "I could not compose an answer. Actual dispatch outcomes:"
			for _, outcome := range trace.Actions {
				trace.Answer += "\n" + outcome.Action.Type + ": "
				if outcome.Error != "" {
					trace.Answer += outcome.Error
				} else {
					trace.Answer += outcome.Reply
				}
			}
		}
		return trace, err
	}
	return trace, nil
}

func (m *Manager) dispatchAction(ctx context.Context, a Action, turnID string) (result ActionResult) {
	started := time.Now()
	result = ActionResult{Action: a, SessionID: a.SessionID, SentText: a.Message, State: "idle"}
	defer func() { result.DurationMS = time.Since(started).Milliseconds(); _ = m.sanitize(&result) }()
	if a.Type == "answer" || a.Type == "ask_user" {
		result.Reply = a.Text
		return
	}
	if err := ctx.Err(); err != nil {
		result.State, result.Error = "interrupted", err.Error()
		return
	}
	if a.Type == "stop" {
		m.stateMu.Lock()
		s, _ := m.session(a.SessionID)
		m.stateMu.Unlock()
		result.SessionTitle, result.Harness = s.Title, s.Harness
		if err := m.StopSession(a.SessionID); err != nil {
			result.State, result.Error = "failed", err.Error()
		} else {
			result.State, result.Reply = "interrupted", "Stop requested for "+s.Title+"; queued work is retained."
			if s.Location == "cloud" {
				result.Reply += " Remote cancellation is unconfirmed."
			}
		}
		return
	}
	m.stateMu.Lock()
	s, ok := m.session(a.SessionID)
	m.stateMu.Unlock()
	var creation Receipt
	if a.Type == "create" {
		var err error
		s, creation, err = m.createWorkerSession(ctx, a)
		result.Receipt = creation
		if err != nil {
			result.State, result.Error = "failed", m.redact(err.Error())
			return
		}
		ok = true
	}
	if !ok {
		result.State, result.Error = "failed", ErrSessionNotFound.Error()
		return
	}
	result.SessionID, result.SessionTitle, result.Harness = s.ID, s.Title, s.Harness
	if a.Type == "read_status" {
		result.State = s.State
		raw, _ := json.Marshal(snapshot([]Session{s})[0])
		result.Reply = string(raw)
		if s.Location == "cloud" {
			result.Receipt.Cloud = &CloudReceipt{BaseURL: s.BaseURL, Namespace: s.Namespace, ThreadID: s.ThreadID, MessageIDs: []string{}}
		} else {
			result.Receipt.Local = &LocalReceipt{Workdir: s.Workdir, NativeSessionID: s.NativeSessionID}
		}
		return
	}
	queued, err := m.enqueueSession(s.ID, a.Message, turnID, a, creation, false)
	if err != nil {
		result.State, result.Error = "failed", m.redact(err.Error())
		return
	}
	return queued
}

func (m *Manager) createWorkerSession(ctx context.Context, a Action) (Session, Receipt, error) {
	setup, ok := m.setup(a.SetupID)
	if !ok {
		return Session{}, Receipt{}, fmt.Errorf("setup no longer exists")
	}
	backend := m.Local
	if setup.Location == "cloud" {
		backend = m.Cloud
	}
	if backend == nil {
		return Session{}, Receipt{}, fmt.Errorf("backend is not configured")
	}
	workdir := a.Workdir
	if setup.Location == "local" {
		var err error
		workdir, err = allowedWorkdir(workdir, m.Config.Local.WorkdirRoots)
		if err != nil {
			return Session{}, Receipt{}, err
		}
	}
	session, receipt, err := backend.Create(ctx, setup, a.Title, workdir)
	if err != nil {
		return session, receipt, err
	}
	if session.Location == "cloud" && (receipt.Cloud == nil || receipt.Cloud.Created == nil || !*receipt.Cloud.Created) {
		return session, receipt, fmt.Errorf("cloud persona create did not create a new thread")
	}
	session.Since, session.State = time.Now().UTC(), "idle"
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.put(session)
	m.recordLocked(session.ID)
	if err := m.saveWorkerStateLocked(); err != nil {
		return session, receipt, err
	}
	m.ensureWorkerLocked(session.ID)
	return session, receipt, nil
}

func (m *Manager) recordQueuedTurnLocked(message QueuedMessage, result ActionResult) error {
	trace := TurnTrace{Version: "onechat.turn/v1", ID: message.TurnID, At: message.At, Message: message.Text, Setups: m.Config.Setups, Inventory: snapshot(m.allSessionsLocked()), History: append([]Message{}, m.state.History...), Decision: Decision{ReasoningSummary: "The person queued work directly to this session.", Confidence: 1, Actions: []Action{message.Action}}, Actions: []ActionResult{result}, Answer: result.Reply, ValidationErrors: []string{}, Totals: Totals{HarnessCalls: map[string]int{}}}
	trace.RawDecision = trace.Decision
	trace.TracePath = filepath.Join(m.Config.StateDir, "traces", trace.ID+".json")
	if len(trace.History) > 10 {
		trace.History = trace.History[len(trace.History)-10:]
	}
	if err := m.sanitize(&trace); err != nil {
		return err
	}
	if err := writeJSON(trace.TracePath, trace); err != nil {
		return err
	}
	m.state.History = append(m.state.History, Message{Role: "user", Text: message.Text, At: message.At}, Message{Role: "assistant", Text: result.Reply, At: time.Now().UTC()})
	return nil
}

func runContext(messages []QueuedMessage) string {
	var out strings.Builder
	for i, message := range messages {
		if i > 0 {
			out.WriteString("\n\nThe person also sent this during the same run:\n")
		}
		out.WriteString(message.Text)
	}
	return out.String()
}
