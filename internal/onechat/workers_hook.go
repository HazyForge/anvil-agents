package onechat

import (
	"fmt"
	"time"
)

type HookResult struct {
	Output     any    `json:"output"`
	DeliveryID string `json:"deliveryId,omitempty"`
}

func (m *Manager) HandleHook(id string, report SessionHook) (any, error) {
	if m.workerRuntime == nil {
		return nil, fmt.Errorf("session workers are not running")
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	s, record, ok := m.activeRunLocked(id, report.RunID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if s.Harness != "claudeCode" || s.Location != "local" {
		return nil, fmt.Errorf("Claude hooks require a local Claude session")
	}
	output := map[string]any{}
	result := HookResult{Output: output}
	if s.State == "interrupted" {
		return result, nil
	}
	now := time.Now().UTC()
	old := s
	switch report.HookEventName {
	case "SessionStart":
		if validLocalResumeID(report.NativeSessionID) {
			s.NativeSessionID = report.NativeSessionID
		}
		s.TranscriptPath = truncate(report.TranscriptPath, 4096)
		s.State, s.Since, s.LastActivity = "running", now, now
		s.Activity = "Claude session started."
	case "PreToolUse", "PostToolUse":
		if s.State != "running" {
			s.State, s.Since = "running", now
		}
		s.Activity, s.LastActivity = m.redact(truncate(report.Summary, 400)), now
		if report.HookEventName == "PostToolUse" && len(record.Mailbox) > 0 {
			message := record.Mailbox[0]
			result.DeliveryID = message.ID
			output["hookSpecificOutput"] = map[string]any{"hookEventName": "PostToolUse", "additionalContext": "The person sent this while you were working: " + message.Text}
		}
	case "Notification":
		if s.State != "waiting" {
			s.State, s.Since = "waiting", now
		}
		s.Activity, s.LastActivity = m.redact(truncate(report.Summary, 400)), now
	case "Stop":
		if !report.StopHookActive && len(record.Mailbox) > 0 {
			message := record.Mailbox[0]
			result.DeliveryID = message.ID
			s.State, s.Since, s.LastActivity = "running", now, now
			s.Activity = "Continuing with the person's queued follow-up."
			output["decision"], output["reason"] = "block", "The person sent a follow-up while you were working: "+message.Text
		}
	default:
		return nil, fmt.Errorf("unknown Claude hook event")
	}
	m.put(s)
	if err := m.saveWorkerStateLocked(); err != nil {
		m.put(old)
		return nil, err
	}
	if report.HookEventName == "PreToolUse" || report.HookEventName == "PostToolUse" || report.HookEventName == "Notification" {
		m.workerEvent("session.activity", map[string]any{"sessionId": id, "kind": report.HookEventName, "summary": s.Activity, "at": now})
	}
	m.workerEvent("session.state", sessionState(s))
	return result, nil
}

// A hook first writes the context to Claude, then acknowledges delivery. A
// disconnected or timed-out hook therefore leaves its message in the mailbox.
func (m *Manager) AcknowledgeHook(id, runID, deliveryID string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	s, record, ok := m.activeRunLocked(id, runID)
	if !ok {
		return ErrSessionNotFound
	}
	for _, message := range record.Running.Messages {
		if message.ID == deliveryID {
			return nil
		}
	}
	if deliveryID == "" || len(record.Mailbox) == 0 || record.Mailbox[0].ID != deliveryID {
		return fmt.Errorf("hook delivery no longer names the oldest queued message")
	}
	old := s
	oldMailbox := append([]QueuedMessage{}, record.Mailbox...)
	oldMessages := append([]QueuedMessage{}, record.Running.Messages...)
	message := record.Mailbox[0]
	record.Mailbox = record.Mailbox[1:]
	record.Running.Messages = append(record.Running.Messages, message)
	s.Transcript = append(append([]Message{}, s.Transcript...), Message{Role: "user", Text: message.Text, At: time.Now().UTC()})
	s.Queued = len(record.Mailbox)
	m.put(s)
	if err := m.saveWorkerStateLocked(); err != nil {
		record.Mailbox, record.Running.Messages = oldMailbox, oldMessages
		m.put(old)
		return err
	}
	m.workerEvent("session.queued", map[string]any{"sessionId": id, "count": s.Queued})
	m.workerEvent("session.state", sessionState(s))
	return nil
}
