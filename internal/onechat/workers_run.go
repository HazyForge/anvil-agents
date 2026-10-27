package onechat

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (m *Manager) sessionLoop(id string, worker *sessionWorker) {
	runtime := m.workerRuntime
	defer runtime.wg.Done()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-worker.wake:
		}
		for {
			if !m.cloudSessionReady(id) {
				break
			}
			prior, setup, run, ctx, cancel, ok := m.takeSessionRun(id, worker)
			if !ok {
				break
			}
			backend := m.Local
			if prior.Location == "cloud" {
				backend = m.Cloud
			}
			var result ActionResult
			var err error
			if backend == nil {
				err = fmt.Errorf("session backend is not configured")
			} else {
				if prior.Location == "local" {
					ctx = WithRunHooks(ctx, RunHooks{Binary: runtime.options.Binary, StateDir: m.Config.StateDir, SessionID: id, RunID: run.ID,
						Activity:      func(kind, summary string) { m.recordActivity(id, run.ID, kind, summary) },
						Started:       func(pid int) { m.recordProcess(id, run.ID, pid) },
						NativeSession: func(nativeID string) { m.recordNativeSession(id, run.ID, nativeID) },
					})
					if _, checkErr := allowedWorkdir(prior.Workdir, m.Config.Local.WorkdirRoots); checkErr != nil {
						err = checkErr
					}
				}
				if err == nil {
					result, err = backend.Send(ctx, setup, prior, run.Messages[0].Text)
				}
			}
			cancelled := ctx.Err()
			cancel()
			if !m.finishSessionRun(id, run.ID, result, err, cancelled) {
				break
			}
			if runtime.ctx.Err() != nil {
				return
			}
		}
	}
}

func (m *Manager) takeSessionRun(id string, worker *sessionWorker) (Session, Setup, SessionRun, context.Context, context.CancelFunc, bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	fail := func() (Session, Setup, SessionRun, context.Context, context.CancelFunc, bool) {
		return Session{}, Setup{}, SessionRun{}, nil, nil, false
	}
	if m.workerRuntime.ctx.Err() != nil {
		return fail()
	}
	s, ok := m.session(id)
	if !ok {
		return fail()
	}
	record := m.recordLocked(id)
	if record.Running != nil || len(record.Mailbox) == 0 || record.RemoteBusy || record.PendingOutcomeID != "" {
		return fail()
	}
	setup, ok := m.setup(s.SetupID)
	if !ok {
		return fail()
	}
	old := s
	old.Transcript = append([]Message{}, s.Transcript...)
	run := SessionRun{ID: uuid.NewString(), StartedAt: time.Now().UTC(), Messages: []QueuedMessage{record.Mailbox[0]}}
	record.Mailbox = record.Mailbox[1:]
	record.Running = &run
	s.State, s.Since, s.UpdatedAt, s.LastActivity = "running", run.StartedAt, run.StartedAt, run.StartedAt
	s.RunningSince = &run.StartedAt
	s.Activity = "Starting queued work."
	s.Queued = len(record.Mailbox)
	s.Transcript = append(s.Transcript, Message{Role: "user", Text: run.Messages[0].Text, At: run.StartedAt})
	m.put(s)
	if err := m.saveWorkerStateLocked(); err != nil {
		record = m.recordLocked(id)
		record.Mailbox = append([]QueuedMessage{run.Messages[0]}, record.Mailbox...)
		record.Running = nil
		old.State, old.Activity = "failed", "Could not persist the session run: "+m.redact(err.Error())
		old.Queued = len(record.Mailbox)
		m.put(old)
		m.refreshInventorySnapshot(time.Now().UTC())
		m.workerEvent("session.state", sessionState(old))
		return fail()
	}
	ctx, cancel := context.WithCancel(m.workerRuntime.ctx)
	worker.cancel = cancel
	m.workerEvent("session.queued", map[string]any{"sessionId": id, "count": s.Queued})
	m.workerEvent("session.state", sessionState(s))
	return old, setup, run, ctx, cancel, true
}

func (m *Manager) activeRunLocked(id, runID string) (Session, *WorkerRecord, bool) {
	s, ok := m.session(id)
	record := m.state.Workers[id]
	if !ok || record == nil || record.Running == nil || record.Running.ID != runID {
		return s, record, false
	}
	return s, record, true
}

func (m *Manager) recordProcess(id, runID string, pid int) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	_, record, ok := m.activeRunLocked(id, runID)
	if !ok {
		return
	}
	record.Running.PID, record.Running.ProcessIdentity = pid, sessionProcessIdentity(pid)
	if err := m.saveWorkerStateLocked(); err != nil {
		if worker := m.workerRuntime.workers[id]; worker != nil && worker.cancel != nil {
			worker.cancel()
		}
	}
}

func (m *Manager) recordNativeSession(id, runID, nativeID string) {
	if !validLocalResumeID(nativeID) {
		return
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	s, _, ok := m.activeRunLocked(id, runID)
	if !ok {
		return
	}
	s.NativeSessionID = nativeID
	m.put(s)
	_ = m.saveWorkerStateLocked()
}

func (m *Manager) recordActivity(id, runID, kind, summary string) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	s, _, ok := m.activeRunLocked(id, runID)
	if !ok || s.State == "interrupted" {
		return
	}
	now := time.Now().UTC()
	if s.State != "running" {
		s.State, s.Since = "running", now
	}
	s.Activity, s.LastActivity = m.redact(truncate(summary, 400)), now
	m.put(s)
	if err := m.saveWorkerStateLocked(); err != nil {
		return
	}
	m.workerEvent("session.activity", map[string]any{"sessionId": id, "kind": kind, "summary": s.Activity, "at": now})
	m.workerEvent("session.state", sessionState(s))
}

func (m *Manager) finishSessionRun(id, runID string, result ActionResult, runErr, cancelErr error) bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	s, record, ok := m.activeRunLocked(id, runID)
	if !ok {
		return false
	}
	run := *record.Running
	message := run.Messages[0]
	result.Action, result.SessionID, result.SessionTitle, result.Harness = message.Action, id, s.Title, s.Harness
	if result.SentText == "" {
		result.SentText = message.Text
	}
	if result.Receipt.Cloud != nil && message.Creation.Cloud != nil {
		result.Receipt.Cloud.Created = message.Creation.Cloud.Created
	}
	if runErr != nil {
		result.Error = m.redact(runErr.Error())
	}
	if cancelErr != nil {
		result.State = "interrupted"
		result.Error = m.redact(cancelErr.Error())
		if s.Location == "cloud" {
			result.Error += "; remote work may still be running; cancellation and completion are unconfirmed"
		}
	} else if runErr != nil || result.Error != "" {
		result.State = "failed"
	} else {
		result.State = "idle"
	}
	if result.DurationMS == 0 {
		result.DurationMS = time.Since(run.StartedAt).Milliseconds()
	}
	now := time.Now().UTC()
	s.State, s.Since, s.UpdatedAt, s.LastActivity = result.State, now, now, now
	if result.Error != "" {
		s.Activity = result.Error
	} else {
		s.Activity = "Run finished."
	}
	if result.Reply != "" {
		s.Transcript = append(s.Transcript, Message{Role: "assistant", Text: result.Reply, At: now})
		s.Summary = truncate(result.Reply, 600)
	}
	if result.Receipt.Local != nil {
		if result.Receipt.Local.NativeSessionID != "" {
			s.NativeSessionID = result.Receipt.Local.NativeSessionID
		}
		if result.Receipt.Local.Sandbox != "" {
			s.Sandbox = result.Receipt.Local.Sandbox
		}
	}
	record.Running = nil
	s.RunningSince = nil
	if s.Location == "cloud" {
		record.RemoteBusy = result.State != "idle" && result.HarnessCalled
	}
	s.Queued = len(record.Mailbox)
	m.put(s)
	if worker := m.workerRuntime.workers[id]; worker != nil {
		worker.cancel = nil
	}
	m.addPendingFollowupLocked(s, &run, result, now)
	if err := m.saveWorkerStateLocked(); err != nil {
		s.State, s.Activity = "failed", "Could not persist the session outcome: "+m.redact(err.Error())
		m.put(s)
		m.refreshInventorySnapshot(now)
		m.recordLocked(id).PendingOutcomeID = m.state.PendingFollowups[len(m.state.PendingFollowups)-1].ID
		m.workerEvent("session.state", sessionState(s))
		m.wakeFollowupsLocked()
		return false
	}
	m.workerEvent("session.state", sessionState(s))
	m.wakeFollowupsLocked()
	return true
}

// Read a cloud thread before consuming its mailbox. Cancelling our poller does
// not cancel remote execution, so queued work waits until that run has ended.
func (m *Manager) cloudSessionReady(id string) bool {
	m.stateMu.Lock()
	session, ok := m.session(id)
	record := m.state.Workers[id]
	if !ok || record == nil || len(record.Mailbox) == 0 || record.Running != nil || record.PendingOutcomeID != "" {
		m.stateMu.Unlock()
		return false
	}
	if session.Location != "cloud" {
		m.stateMu.Unlock()
		return true
	}
	busy := record.RemoteBusy
	m.stateMu.Unlock()
	reader, ok := m.Cloud.(interface {
		Read(context.Context, Session) (Session, error)
	})
	if !ok {
		return !busy
	}
	ctx, cancel := context.WithTimeout(m.workerRuntime.ctx, 5*time.Second)
	remote, err := reader.Read(ctx, session)
	cancel()
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	current, ok := m.session(id)
	if !ok {
		return false
	}
	record = m.recordLocked(id)
	if err != nil {
		current.Activity = "Could not refresh cloud state; queued work is retained. " + m.redact(err.Error())
		m.put(current)
		if m.saveWorkerStateLocked() == nil {
			m.workerEvent("session.state", sessionState(current))
		}
		return false
	}
	record.RemoteBusy = remote.State == "running" || remote.State == "waiting"
	if record.RemoteBusy && current.State != "interrupted" && current.State != "failed" {
		current.State = remote.State
		current.Since = remote.UpdatedAt
		current.Activity = "Cloud work is still running; queued work waits for it to end."
		m.put(current)
	}
	if err := m.saveWorkerStateLocked(); err != nil {
		return false
	}
	m.workerEvent("session.state", sessionState(current))
	return !record.RemoteBusy
}
