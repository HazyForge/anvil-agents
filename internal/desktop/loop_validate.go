package desktop

import (
	"errors"
	"strings"
)

// A syntactically valid but inconsistent file must never become execution
// intent. The store has no repair-by-default path that could replay work.
func validateLoopDisk(d loopDisk) error {
	invalid := func() error { return errors.New("manager store has unsupported or inconsistent records") }
	if d.Version != 1 || d.Revision < 0 || len(d.Tasks) > maxLoopTasks || len(d.Requests) > maxLoopRequests || len(d.Decisions) > maxLoopRequests+maxLoopTasks || len(d.Events) > maxLoopEvents {
		return invalid()
	}
	tasks := map[string]loopTask{}
	for _, t := range d.Tasks {
		if !validLoopRequest(t.ID) || !validLoopRequest(t.RequestID) || tasks[t.ID].ID != "" || validateLoopConversation(t.Conversation) != nil || len(t.Content) > maxPromptBytes || len(t.Reply) > 128<<10 || t.Attempt < 0 || t.CreatedAt.IsZero() || t.UpdatedAt.IsZero() {
			return invalid()
		}
		if t.Origin != "manager" && t.Origin != "observed" {
			return invalid()
		}
		switch t.State {
		case "queued", "running", "verifying":
			if !t.Blocking || strings.TrimSpace(t.Content) == "" || (t.Conversation.Workdir == "" && !t.WorkspaceUnresolved) || len(loopPrompt(t.Conversation, t.Content)) > maxPromptBytes {
				return invalid()
			}
		case "completed":
			if t.Blocking || t.Reply == "" || t.ExitCode == nil || *t.ExitCode != 0 {
				return invalid()
			}
		case "cancelled":
			if t.Blocking {
				return invalid()
			}
		case "paused", "needs_decision":
		default:
			return invalid()
		}
		tasks[t.ID] = t
	}
	decisions := map[string]bool{}
	pending := map[string]bool{}
	for _, decision := range d.Decisions {
		if !validLoopRequest(decision.ID) || decisions[decision.ID] || tasks[decision.TaskID].ID == "" || decision.Version < 1 || len(decision.Choices) == 0 || len(decision.Choices) > 3 {
			return invalid()
		}
		if decision.State != "pending" && decision.State != "resolved" {
			return invalid()
		}
		if decision.State == "pending" {
			if pending[decision.TaskID] {
				return invalid()
			}
			pending[decision.TaskID] = true
			task := tasks[decision.TaskID]
			if task.State != "needs_decision" && task.State != "paused" {
				return invalid()
			}
			expected := fixedChoices(task.Blocking, task.Content != "")
			if len(expected) != len(decision.Choices) {
				return invalid()
			}
			for i := range expected {
				if expected[i].ID != decision.Choices[i].ID {
					return invalid()
				}
			}
		}
		seen := map[string]bool{}
		for _, choice := range decision.Choices {
			if choice.ID != "retry" && choice.ID != "pause" && choice.ID != "cancel" && choice.ID != "checked_stopped" {
				return invalid()
			}
			if seen[choice.ID] {
				return invalid()
			}
			seen[choice.ID] = true
		}
		decisions[decision.ID] = true
	}
	for _, task := range d.Tasks {
		if (task.State == "paused" || task.State == "needs_decision") && !pending[task.ID] {
			return invalid()
		}
	}
	requests := map[string]bool{}
	for _, r := range d.Requests {
		if !validLoopRequest(r.RequestID) || requests[r.RequestID] || len(r.Hash) != 64 || r.DecisionID != "" && !decisions[r.DecisionID] {
			return invalid()
		}
		requests[r.RequestID] = true
	}
	for _, task := range d.Tasks {
		if !requests[task.RequestID] {
			return invalid()
		}
	}
	last := int64(0)
	for _, e := range d.Events {
		if e.ID <= last || e.ID > d.NextEvent || tasks[e.TaskID].ID == "" || e.At.IsZero() {
			return invalid()
		}
		if e.Stage != "detect" && e.Stage != "ask" && e.Stage != "act" && e.Stage != "verify" {
			return invalid()
		}
		last = e.ID
	}
	return nil
}
