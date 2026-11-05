package onechat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *Manager) addPendingFollowupLocked(session Session, run *SessionRun, result ActionResult, at time.Time) {
	if run == nil || len(run.Messages) == 0 {
		return
	}
	id := uuid.NewString()
	trace := TurnTrace{Version: "onechat.turn/v1", Kind: "followup", ID: id, ParentTurnID: run.Messages[0].TurnID, SessionID: session.ID, Messages: append([]QueuedMessage{}, run.Messages...), At: at, Message: runContext(run.Messages), Setups: m.Config.Setups, Inventory: snapshot([]Session{session}), History: []Message{}, Actions: []ActionResult{result}, Decision: Decision{ReasoningSummary: "Report the actual session outcome for every message handled in this run.", Confidence: 1, Actions: []Action{result.Action}}, ValidationErrors: []string{}, Totals: Totals{HarnessCalls: map[string]int{}}}
	trace.RawDecision = trace.Decision
	trace.TracePath = filepath.Join(m.Config.StateDir, "traces", id+".json")
	trace.Error = result.Error
	addUsage(&trace.Totals, result.Usage)
	if result.HarnessCalled {
		trace.Totals.HarnessCalls[result.Harness]++
	}
	m.state.PendingFollowups = append(m.state.PendingFollowups, trace)
}

func (m *Manager) wakeFollowupsLocked() {
	if m.workerRuntime != nil {
		signal(m.workerRuntime.followups)
	}
}

func (m *Manager) followupLoop() {
	runtime := m.workerRuntime
	defer runtime.wg.Done()
	retry := time.NewTicker(time.Second)
	defer retry.Stop()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-runtime.followups:
		case <-retry.C:
		}
		for {
			trace, ready := m.nextFollowup()
			if !ready {
				break
			}
			if !m.waitDispatches(runtime.ctx, trace.Messages) {
				return
			}
			if trace.Answer == "" {
				input := ComposeInput{Message: trace.Message, Decision: trace.Decision, Outcomes: trace.Actions}
				if err := m.sanitize(&input); err != nil {
					break
				}
				trace.Totals.ManagerCalls++
				answer, usage, err := m.Decider.Compose(runtime.ctx, input)
				if runtime.ctx.Err() != nil {
					return
				}
				trace.ComposeUsage = usage
				addUsage(&trace.Totals, usage)
				if err != nil {
					answer = fmt.Sprintf("%s: %s", trace.Inventory[0].Title, trace.Actions[0].State)
					if trace.Actions[0].Error != "" {
						answer += ". " + trace.Actions[0].Error
					} else {
						answer += ". " + trace.Actions[0].Reply
					}
				}
				if len(trace.Messages) > 1 {
					var labels []string
					for _, message := range trace.Messages {
						labels = append(labels, "- "+truncate(message.Text, 300))
					}
					answer += "\n\nMessages handled in this run:\n" + strings.Join(labels, "\n")
				}
				trace.Answer = answer
			}
			if err := m.publishFollowup(trace); err != nil {
				// Leave the persisted completion pending instead of claiming delivery. A
				// later wake or daemon restart retries without rerunning the harness.
				break
			}
		}
	}
}

func (m *Manager) nextFollowup() (TurnTrace, bool) {
	m.stateMu.Lock()
	if len(m.state.PendingFollowups) == 0 {
		m.stateMu.Unlock()
		return TurnTrace{}, false
	}
	trace := m.state.PendingFollowups[0]
	if err := m.sanitize(&trace); err != nil {
		m.stateMu.Unlock()
		return TurnTrace{}, false
	}
	m.stateMu.Unlock()
	// A state-write retry can reuse the already composed trace without charging
	// for another model call or changing the stable follow-up ID.
	if raw, err := os.ReadFile(trace.TracePath); err == nil {
		var saved TurnTrace
		if json.Unmarshal(raw, &saved) == nil && saved.ID == trace.ID && saved.Kind == "followup" && saved.Answer != "" {
			trace = saved
		}
	}
	return trace, true
}

func (m *Manager) waitDispatches(ctx context.Context, messages []QueuedMessage) bool {
	for _, message := range messages {
		m.stateMu.Lock()
		done := m.workerRuntime.dispatching[message.TurnID]
		m.stateMu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return false
			}
		}
	}
	return true
}

func (m *Manager) publishFollowup(trace TurnTrace) error {
	trace.At = time.Now().UTC()
	if err := m.sanitize(&trace); err != nil {
		return err
	}
	if err := writeJSON(trace.TracePath, trace); err != nil {
		return err
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	followup := Followup{ID: trace.ID, At: trace.At, SessionID: trace.SessionID, TurnID: trace.ParentTurnID, Text: trace.Answer}
	oldFollowups := append([]Followup{}, m.state.Followups...)
	oldHistory := append([]Message{}, m.state.History...)
	oldPending := append([]TurnTrace{}, m.state.PendingFollowups...)
	s, sessionExists := m.session(trace.SessionID)
	oldSession := s
	record := m.recordLocked(trace.SessionID)
	oldBlocked := record.PendingOutcomeID
	if record.PendingOutcomeID == trace.ID {
		record.PendingOutcomeID = ""
		s.State, s.Since, s.Activity = trace.Actions[0].State, trace.At, "Run outcome saved."
		if trace.Actions[0].Error != "" {
			s.Activity = trace.Actions[0].Error
		}
		if sessionExists {
			m.put(s)
		}
	}
	exists := false
	for _, existing := range m.state.Followups {
		if existing.ID == trace.ID {
			exists = true
			break
		}
	}
	if !exists {
		m.state.Followups = append(m.state.Followups, followup)
		m.state.History = append(m.state.History, Message{ID: trace.ID, Role: "assistant", Kind: "followup", SessionID: trace.SessionID, TurnID: trace.ParentTurnID, Text: trace.Answer, At: followup.At})
	}
	for i, pending := range m.state.PendingFollowups {
		if pending.ID == trace.ID {
			m.state.PendingFollowups = append(m.state.PendingFollowups[:i], m.state.PendingFollowups[i+1:]...)
			break
		}
	}
	if err := m.saveWorkerStateLocked(); err != nil {
		m.state.Followups, m.state.History, m.state.PendingFollowups = oldFollowups, oldHistory, oldPending
		m.recordLocked(trace.SessionID).PendingOutcomeID = oldBlocked
		if sessionExists {
			m.put(oldSession)
		}
		return err
	}
	m.workerEvent("chat.message", map[string]any{"id": followup.ID, "role": "assistant", "kind": "followup", "sessionId": followup.SessionID, "turnId": followup.TurnID, "text": followup.Text, "at": followup.At})
	if worker := m.workerRuntime.workers[trace.SessionID]; worker != nil {
		signal(worker.wake)
	}
	return nil
}
