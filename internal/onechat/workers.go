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

var ErrSessionNotFound = errors.New("session is not in the daemon inventory")

type WorkerOptions struct {
	Binary  string
	Observe func(Event)
}

type SessionState struct {
	SessionID string    `json:"sessionId"`
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	Activity  string    `json:"activity"`
	Queued    int       `json:"queued"`
}

type QueuedMessage struct {
	ID       string    `json:"id"`
	TurnID   string    `json:"turnId"`
	Text     string    `json:"text"`
	At       time.Time `json:"at"`
	Action   Action    `json:"action"`
	Creation Receipt   `json:"creation,omitempty"`
}

type SessionRun struct {
	ID              string          `json:"id"`
	StartedAt       time.Time       `json:"startedAt"`
	PID             int             `json:"pid,omitempty"`
	ProcessIdentity string          `json:"processIdentity,omitempty"`
	Messages        []QueuedMessage `json:"messages"`
}

type WorkerRecord struct {
	RemoteBusy       bool            `json:"remoteBusy,omitempty"`
	PendingOutcomeID string          `json:"pendingOutcomeId,omitempty"`
	Mailbox          []QueuedMessage `json:"mailbox"`
	Running          *SessionRun     `json:"running,omitempty"`
}

type Followup struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	SessionID string    `json:"sessionId"`
	TurnID    string    `json:"turnId"`
	Text      string    `json:"text"`
}

type sessionWorker struct {
	wake   chan struct{}
	cancel context.CancelFunc
}

type workerRuntime struct {
	ctx         context.Context
	options     WorkerOptions
	workers     map[string]*sessionWorker
	followups   chan struct{}
	dispatching map[string]chan struct{}
	eventMu     sync.Mutex
	events      []Event
	eventWake   chan struct{}
	wg          sync.WaitGroup
}

// StartWorkers is called once while the host owns the state directory. Turns
// subsequently dispatch work; standalone managers keep their synchronous path.
func (m *Manager) StartWorkers(ctx context.Context, options WorkerOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.workerRuntime != nil {
		return fmt.Errorf("session workers have already started")
	}
	runtime := &workerRuntime{ctx: ctx, options: options, workers: map[string]*sessionWorker{}, followups: make(chan struct{}, 1), dispatching: map[string]chan struct{}{}, eventWake: make(chan struct{}, 1)}
	now := time.Now().UTC()
	for _, s := range m.allSessionsLocked() {
		record := m.recordLocked(s.ID)
		if record.Running != nil {
			run := record.Running
			if run.PID > 0 && run.ProcessIdentity != "" && sessionProcessAlive(run.PID) && sessionProcessIdentity(run.PID) == run.ProcessIdentity {
				if err := interruptSessionProcess(run.PID); err != nil {
					return fmt.Errorf("stop interrupted session %s before restarting: %w", s.Title, err)
				}
			}
			result := ActionResult{SessionID: s.ID, SessionTitle: s.Title, Harness: s.Harness, State: "interrupted", Error: "The daemon stopped before this run's outcome was recorded; the run was cut off.", DurationMS: now.Sub(run.StartedAt).Milliseconds()}
			if len(run.Messages) > 0 {
				result.Action = run.Messages[0].Action
				result.SentText = run.Messages[0].Text
				result.Receipt = run.Messages[0].Creation
			}
			if s.Location == "local" {
				result.Receipt.Local = &LocalReceipt{Workdir: s.Workdir, NativeSessionID: s.NativeSessionID, Sandbox: s.Sandbox}
			}
			if s.Location == "cloud" {
				result.Error += " Remote work may still be running; its completion is unconfirmed."
			}
			m.addPendingFollowupLocked(s, run, result, now)
			record.Running = nil
			s.RunningSince = nil
			s.State, s.Since, s.UpdatedAt, s.Activity = "interrupted", now, now, result.Error
		} else if s.Location == "local" && (s.State == "running" || s.State == "waiting") {
			s.RunningSince = nil
			s.State, s.Since, s.UpdatedAt, s.Activity = "interrupted", now, now, "The previous run was cut off before this daemon attached."
		}
		if s.State == "" {
			s.State = "idle"
		}
		if s.Since.IsZero() {
			s.Since = s.UpdatedAt
		}
		s.Queued = len(record.Mailbox)
		m.put(s)
	}
	if err := m.saveWorkerStateLocked(); err != nil {
		return err
	}
	m.workerRuntime = runtime
	for _, s := range m.allSessionsLocked() {
		m.ensureWorkerLocked(s.ID)
	}
	runtime.wg.Add(3)
	go m.workerEventLoop()
	go m.refreshWorkerInventory()
	go m.followupLoop()
	m.wakeFollowupsLocked()
	return nil
}

// WaitWorkers waits for the session workers to stop once the host's context
// has ended, then releases them so a later host can start workers again with
// the same manager.
func (m *Manager) WaitWorkers() {
	m.stateMu.Lock()
	runtime := m.workerRuntime
	m.stateMu.Unlock()
	if runtime == nil {
		return
	}
	runtime.wg.Wait()
	m.mu.Lock()
	m.stateMu.Lock()
	if m.workerRuntime == runtime {
		m.workerRuntime = nil
	}
	m.stateMu.Unlock()
	m.mu.Unlock()
}

func (m *Manager) recordLocked(id string) *WorkerRecord {
	record := m.state.Workers[id]
	if record == nil {
		record = &WorkerRecord{Mailbox: []QueuedMessage{}}
		m.state.Workers[id] = record
	}
	return record
}

func (m *Manager) allSessionsLocked() []Session {
	sessions := cloneSessions(m.state.Sessions)
	for _, session := range m.cloudSessions {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return sessions
}

func (m *Manager) saveWorkerStateLocked() error {
	sort.SliceStable(m.state.History, func(i, j int) bool { return m.state.History[i].At.Before(m.state.History[j].At) })
	m.state.CloudSessions = nil
	for _, session := range m.cloudSessions {
		m.state.CloudSessions = append(m.state.CloudSessions, session)
	}
	sort.Slice(m.state.CloudSessions, func(i, j int) bool { return m.state.CloudSessions[i].ID < m.state.CloudSessions[j].ID })
	raw, err := json.Marshal(m.state)
	if err != nil {
		return err
	}
	var saved State
	if err := json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	if err := m.sanitize(&saved); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(m.Config.StateDir, "onechat.json"), saved); err != nil {
		return err
	}
	m.refreshInventorySnapshot(time.Now().UTC())
	m.refreshHistorySnapshot()
	return nil
}

func (m *Manager) ensureWorkerLocked(id string) {
	runtime := m.workerRuntime
	if runtime.workers[id] != nil {
		return
	}
	worker := &sessionWorker{wake: make(chan struct{}, 1)}
	runtime.workers[id] = worker
	runtime.wg.Add(1)
	go m.sessionLoop(id, worker)
	signal(worker.wake)
}

func signal(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

// Events are enqueued while state is serialized and observed outside its lock.
// The host observer must only enqueue client frames, never block on transport I/O.
func (m *Manager) workerEvent(name string, body any) {
	runtime := m.workerRuntime
	if runtime == nil {
		return
	}
	m.emit(func(event Event) {
		runtime.eventMu.Lock()
		runtime.events = append(runtime.events, event)
		runtime.eventMu.Unlock()
		signal(runtime.eventWake)
	}, name, body)
}

func (m *Manager) workerEventLoop() {
	runtime := m.workerRuntime
	defer runtime.wg.Done()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-runtime.eventWake:
		}
		for {
			runtime.eventMu.Lock()
			events := runtime.events
			runtime.events = nil
			runtime.eventMu.Unlock()
			if len(events) == 0 {
				break
			}
			if runtime.options.Observe != nil {
				for _, event := range events {
					runtime.options.Observe(event)
				}
			}
		}
	}
}

func sessionState(s Session) SessionState {
	return SessionState{SessionID: s.ID, State: s.State, Since: s.Since, Activity: s.Activity, Queued: s.Queued}
}

func (m *Manager) SessionStates() []SessionState {
	sessions, _ := m.LastInventory()
	states := make([]SessionState, 0, len(sessions))
	for _, session := range sessions {
		states = append(states, sessionState(session))
	}
	return states
}

func (m *Manager) Followups(limit int) []Followup {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	entries := m.state.Followups
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return append([]Followup{}, entries...)
}

func (m *Manager) QueueSession(ctx context.Context, id, text, turnID string) (ActionResult, error) {
	if len(text) > 64<<10 || strings.TrimSpace(text) == "" {
		return ActionResult{}, fmt.Errorf("session message must contain 1–65536 bytes")
	}
	if m.workerRuntime == nil {
		return ActionResult{}, fmt.Errorf("start onechat daemon before queueing session work")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	direct := turnID == ""
	if direct {
		turnID = uuid.NewString()
	}
	return m.enqueueSession(id, text, turnID, Action{Type: "send", SessionID: id, Message: text}, Receipt{}, direct)
}

func (m *Manager) enqueueSession(id, text, turnID string, action Action, creation Receipt, direct bool) (ActionResult, error) {
	m.stateMu.Lock()
	if err := m.workerRuntime.ctx.Err(); err != nil {
		m.stateMu.Unlock()
		return ActionResult{}, err
	}
	s, ok := m.session(id)
	if !ok {
		m.stateMu.Unlock()
		return ActionResult{}, ErrSessionNotFound
	}
	setup, ok := m.setup(s.SetupID)
	if !ok || setup.Location != s.Location || setup.Harness != s.Harness {
		m.stateMu.Unlock()
		return ActionResult{}, fmt.Errorf("restore the session's configured setup before sending work")
	}
	record := m.recordLocked(id)
	message := QueuedMessage{ID: uuid.NewString(), TurnID: turnID, Text: m.redact(text), At: time.Now().UTC(), Action: action, Creation: creation}
	record.Mailbox = append(record.Mailbox, message)
	s.Queued = len(record.Mailbox)
	m.put(s)
	result := ActionResult{Action: action, SessionID: id, SessionTitle: s.Title, Harness: s.Harness, SentText: message.Text, State: s.State, Receipt: creation, Reply: fmt.Sprintf("Queued work for %s (%d waiting); the session's outcome will arrive as a follow-up.", s.Title, s.Queued)}
	historyLen := len(m.state.History)
	var err error
	if direct {
		err = m.recordQueuedTurnLocked(message, result)
	}
	if err == nil {
		err = m.saveWorkerStateLocked()
	}
	if err != nil {
		record = m.recordLocked(id)
		record.Mailbox = record.Mailbox[:len(record.Mailbox)-1]
		s.Queued = len(record.Mailbox)
		m.put(s)
		m.state.History = m.state.History[:historyLen]
		m.stateMu.Unlock()
		return ActionResult{}, err
	}
	m.ensureWorkerLocked(id)
	state := sessionState(s)
	worker := m.workerRuntime.workers[id]
	m.workerEvent("session.queued", map[string]any{"sessionId": id, "count": state.Queued})
	m.workerEvent("session.state", state)
	m.stateMu.Unlock()
	signal(worker.wake)
	return result, nil
}

func (m *Manager) StopSession(id string) error {
	if m.workerRuntime == nil {
		return fmt.Errorf("start onechat daemon before stopping session work")
	}
	m.stateMu.Lock()
	s, ok := m.session(id)
	if !ok {
		m.stateMu.Unlock()
		return ErrSessionNotFound
	}
	worker := m.workerRuntime.workers[id]
	if worker != nil && worker.cancel != nil {
		worker.cancel()
	}
	now := time.Now().UTC()
	s.State, s.Since, s.UpdatedAt, s.LastActivity = "interrupted", now, now, now
	s.Activity = "The person stopped the current run. Queued work is retained."
	if s.Location == "cloud" {
		s.Activity += " Remote cancellation is unconfirmed."
	}
	m.put(s)
	err := m.saveWorkerStateLocked()
	state := sessionState(s)
	if err == nil {
		m.workerEvent("session.state", state)
	}
	m.stateMu.Unlock()
	if worker != nil {
		signal(worker.wake)
	}
	return err
}

func (m *Manager) refreshWorkerInventory() {
	runtime := m.workerRuntime
	defer runtime.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = m.workerInventory(runtime.ctx)
		select {
		case <-runtime.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Cloud discovery does not hold the turn or state mutex during network I/O.
// A failed refresh leaves the last good inventory available to local work.
func (m *Manager) workerInventory(ctx context.Context) ([]Session, error) {
	var cloud []Session
	if m.Cloud != nil {
		var err error
		cloud, err = m.Cloud.List(ctx)
		if err != nil {
			return nil, err
		}
		if reader, ok := m.Cloud.(interface {
			Read(context.Context, Session) (Session, error)
		}); ok {
			for i := range cloud {
				cloud[i], err = reader.Read(ctx, cloud[i])
				if err != nil {
					return nil, err
				}
			}
		}
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	changed := []Session{}
	for _, s := range cloud {
		s = m.bind(s)
		record := m.recordLocked(s.ID)
		record.RemoteBusy = s.State == "running" || s.State == "waiting"
		if old, ok := m.cloudSessions[s.ID]; ok {
			if record.Running != nil || old.State == "interrupted" || old.State == "failed" {
				s.State, s.Since, s.Activity, s.LastActivity, s.RunningSince = old.State, old.Since, old.Activity, old.LastActivity, old.RunningSince
			} else if old.State == s.State {
				s.Since, s.Activity, s.LastActivity = old.Since, old.Activity, old.LastActivity
			}
			if old.UpdatedAt.After(s.UpdatedAt) {
				s.Transcript, s.Summary, s.UpdatedAt = old.Transcript, old.Summary, old.UpdatedAt
			}
		} else if record.RemoteBusy {
			s.Activity = "Cloud work is already running outside this daemon."
		}
		s.Queued = len(record.Mailbox)
		if s.Since.IsZero() {
			s.Since = s.UpdatedAt
		}
		m.put(s)
		changed = append(changed, s)
		if m.workerRuntime != nil && m.workerRuntime.ctx.Err() == nil {
			m.ensureWorkerLocked(s.ID)
		}
	}
	if err := m.saveWorkerStateLocked(); err != nil {
		return nil, err
	}
	for _, s := range changed {
		m.workerEvent("session.state", sessionState(s))
	}
	if m.workerRuntime != nil && m.workerRuntime.ctx.Err() == nil {
		for id, worker := range m.workerRuntime.workers {
			if record := m.state.Workers[id]; record != nil && len(record.Mailbox) > 0 && !record.RemoteBusy {
				signal(worker.wake)
			}
		}
	}
	return snapshot(m.allSessionsLocked()), nil
}
