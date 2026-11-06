package desktop

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Server) handleLocalLoop(w http.ResponseWriter, r *http.Request) {
	// Same loopback/origin checks as mutation routes, including DNS-rebinding.
	checked := r.Clone(r.Context())
	checked.Header = r.Header.Clone()
	checked.Header.Set("Content-Type", "application/json")
	if !localChatRequest(checked) {
		writeError(w, 403, "invalid_origin", "The manager requires a same-origin request.")
		return
	}
	l, err := s.getLoop()
	if err != nil {
		writeLoopError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, l.snapshot())
}
func (s *Server) handleLocalLoopWork(w http.ResponseWriter, r *http.Request) {
	var body loopWorkRequest
	if !decodeLocalManagerRequest(w, r, &body, 128<<10) {
		return
	}
	if !validLoopRequest(body.RequestID) || strings.TrimSpace(body.Content) == "" || len(body.Content) > 12000 {
		writeError(w, 400, "invalid_work", "Work requires a UUID request ID and a message of at most 12,000 bytes.")
		return
	}
	if err := validateLoopConversation(body.Conversation); err != nil {
		writeLoopError(w, err)
		return
	}
	if len(loopPrompt(body.Conversation, body.Content)) > maxPromptBytes {
		writeError(w, 400, "history_too_large", "Saved history plus this message exceeds the local prompt limit.")
		return
	}
	l, err := s.getLoop()
	if err != nil {
		writeLoopError(w, err)
		return
	}
	hash := loopHash(struct {
		Kind string
		Body loopWorkRequest
	}{"work", body})
	l.mu.Lock()
	replayed, err := l.replayLocked(body.RequestID, hash)
	l.mu.Unlock()
	if err != nil {
		writeLoopError(w, err)
		return
	}
	if replayed {
		writeJSON(w, 200, l.snapshot())
		return
	}
	// Freeze the actual canonical folder before durable acceptance, but never
	// start a subprocess during this preflight.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	_, target, err := s.resolveDelegate(ctx, body.Conversation.Harness)
	if err != nil {
		writeError(w, 409, "harness_unavailable", "The saved harness is unavailable. No work was accepted.")
		return
	}
	if target.Mode != body.Conversation.Target || target.Distro != body.Conversation.WSLDistro {
		writeError(w, 409, "runtime_changed", "The conversation's saved runtime no longer matches Desktop. No work was accepted.")
		return
	}
	d := s.opts.Discoverer
	d.Target = target.Mode
	d.Distro = target.Distro
	workdir, err := s.chatWorkdir(ctx, d, target, body.Conversation.Workdir)
	if err != nil {
		writeError(w, 400, "invalid_workdir", "The saved working folder is unavailable. No work was accepted.")
		return
	}
	submittedWorkdir := body.Conversation.Workdir
	body.Conversation.Workdir = workdir
	err = l.change(func() error {
		if replayed, err := l.replayLocked(body.RequestID, hash); err != nil || replayed {
			return err
		}
		key := chatWorkspaceKey(d, target, workdir)
		s.mu.Lock()
		owner := s.chatWorkspaceOwners[key]
		s.mu.Unlock()
		if owner == "conversation:" || owner == "conversation:"+body.Conversation.ID {
			return loopConflict("conversation_running", "This conversation has an active direct turn. Wait for its saved reply before submitting managed work.")
		}
		if l.unresolvedLocked(body.Conversation.ID) {
			return loopConflict("conversation_unsettled", "Resolve this conversation's existing manager work before submitting another turn.")
		}
		if len(l.disk.Tasks) >= maxLoopTasks {
			return loopConflict("loop_capacity", "The local manager has reached its work capacity. Existing records are preserved.")
		}
		if err := l.recordLocked(body.RequestID, hash, "", ""); err != nil {
			return err
		}
		now := time.Now().UTC()
		task := loopTask{ID: uuid.NewString(), RequestID: body.RequestID, Conversation: body.Conversation, Content: body.Content, Origin: "manager", SubmittedWorkdir: submittedWorkdir, State: "queued", Blocking: true, CreatedAt: now, UpdatedAt: now}
		l.disk.Tasks = append(l.disk.Tasks, task)
		l.eventLocked(task.ID, "detect", "Accepted this conversation's work and saved its runtime binding. Waiting for the workspace to become available.")
		return nil
	})
	if err != nil {
		writeLoopError(w, err)
		return
	}
	l.notify()
	writeJSON(w, http.StatusAccepted, l.snapshot())
}
func (s *Server) handleLocalLoopObserve(w http.ResponseWriter, r *http.Request) {
	var body loopObserveRequest
	if !decodeLocalManagerRequest(w, r, &body, 128<<10) {
		return
	}
	if !validLoopRequest(body.RequestID) || (!body.Pending && strings.TrimSpace(body.LastError) == "") || len(body.LastError) > 2000 {
		writeError(w, 400, "invalid_observation", "Provide a pending or failed Anvil conversation with a UUID observation ID.")
		return
	}
	if err := validateLoopConversation(body.Conversation); err != nil {
		writeLoopError(w, err)
		return
	}
	l, err := s.getLoop()
	if err != nil {
		writeLoopError(w, err)
		return
	}
	hash := loopHash(struct {
		Kind string
		Body loopObserveRequest
	}{"observe", body})
	fingerprint := loopHash(struct {
		Conversation loopConversation
		Pending      bool
		Error        string
	}{body.Conversation, body.Pending, body.LastError})
	submittedWorkdir := body.Conversation.Workdir
	workspaceUnresolved := false
	target := invokeTarget{Mode: body.Conversation.Target, Distro: body.Conversation.WSLDistro}
	discoverer := s.opts.Discoverer
	discoverer.Target = target.Mode
	discoverer.Distro = target.Distro
	observeCtx, cancelObserve := context.WithTimeout(r.Context(), 15*time.Second)
	if folder, resolveErr := s.chatWorkdir(observeCtx, discoverer, target, submittedWorkdir); resolveErr == nil {
		body.Conversation.Workdir = folder
	} else {
		workspaceUnresolved = true
	}
	cancelObserve()
	err = l.change(func() error {
		if replayed, err := l.replayLocked(body.RequestID, hash); err != nil || replayed {
			return err
		}
		if err := l.recordLocked(body.RequestID, hash, "", ""); err != nil {
			return err
		}
		for _, t := range l.disk.Tasks {
			if t.Fingerprint == fingerprint || t.Conversation.ID == body.Conversation.ID && (t.State != "completed" && t.State != "cancelled") {
				return nil
			}
		}
		if len(l.disk.Tasks) >= maxLoopTasks {
			return loopConflict("loop_capacity", "The local manager has reached its work capacity. Existing records are preserved.")
		}
		c := body.Conversation
		content := ""
		if len(c.Messages) > 0 && c.Messages[len(c.Messages)-1].Role == "user" {
			content = c.Messages[len(c.Messages)-1].Content
			c.Messages = c.Messages[:len(c.Messages)-1]
		}
		now := time.Now().UTC()
		task := loopTask{ID: uuid.NewString(), RequestID: body.RequestID, Conversation: c, Content: content, Origin: "observed", SubmittedWorkdir: submittedWorkdir, WorkspaceUnresolved: workspaceUnresolved, State: "needs_decision", Blocking: body.Pending, CreatedAt: now, UpdatedAt: now, Fingerprint: fingerprint}
		reason := "prior_failure"
		message := "This saved conversation reports a failed turn. Choose whether to retry it or close the issue."
		if body.Pending {
			reason = "completion_unknown"
			message = "This saved conversation has an unconfirmed turn. Check its harness and folder before allowing another attempt."
		}
		task.Error = body.LastError
		l.disk.Tasks = append(l.disk.Tasks, task)
		l.eventLocked(task.ID, "detect", message)
		l.askLocked(&l.disk.Tasks[len(l.disk.Tasks)-1], reason, message, body.Pending)
		return nil
	})
	if err != nil {
		writeLoopError(w, err)
		return
	}
	writeJSON(w, 200, l.snapshot())
}
func (s *Server) handleLocalLoopDecision(w http.ResponseWriter, r *http.Request) {
	var body loopChoiceRequest
	if !decodeLocalManagerRequest(w, r, &body, 4096) {
		return
	}
	snapshot, err := s.loopDecisionChoice(r.Context(), r.PathValue("id"), body.RequestID, body.Choice, body.Version)
	if err != nil {
		writeLoopError(w, err)
		return
	}
	writeJSON(w, 200, snapshot)
}
func (s *Server) loopDecisionChoice(ctx context.Context, id, requestID, choice string, version int) (loopSnapshot, error) {
	if !validLoopRequest(requestID) || version < 1 {
		return loopSnapshot{}, &loopError{400, "invalid_decision", "The decision needs a UUID request ID and its current version."}
	}
	if err := ctx.Err(); err != nil {
		return loopSnapshot{}, err
	}
	l, err := s.getLoop()
	if err != nil {
		return loopSnapshot{}, err
	}
	hash := loopHash(struct {
		ID, Choice string
		Version    int
	}{id, choice, version})
	var clearKey string
	err = l.change(func() error {
		if replayed, err := l.replayLocked(requestID, hash); err != nil || replayed {
			return err
		}
		var decision *loopDecision
		for i := range l.disk.Decisions {
			if l.disk.Decisions[i].ID == id {
				decision = &l.disk.Decisions[i]
				break
			}
		}
		if decision == nil {
			return &loopError{404, "decision_missing", "This manager decision was not found."}
		}
		if decision.State != "pending" || decision.Version != version {
			return loopConflict("decision_changed", "This decision changed. Read its latest choices before answering.")
		}
		allowed := false
		for _, c := range decision.Choices {
			if c.ID == choice {
				allowed = true
			}
		}
		if !allowed {
			return &loopError{400, "invalid_choice", "Choose one of this decision's available actions."}
		}
		var task *loopTask
		for i := range l.disk.Tasks {
			if l.disk.Tasks[i].ID == decision.TaskID {
				task = &l.disk.Tasks[i]
				break
			}
		}
		if task == nil {
			return &loopError{404, "work_missing", "This manager work was not found."}
		}
		if choice == "checked_stopped" {
			key := chatWorkspaceKey(s.opts.Discoverer, invokeTarget{Mode: task.Conversation.Target, Distro: task.Conversation.WSLDistro}, task.Conversation.Workdir)
			s.mu.Lock()
			active := s.chatWorkspaces[key]
			if task.WorkspaceUnresolved {
				active = len(s.chatWorkspaces) > 0
			}
			s.mu.Unlock()
			if active {
				return loopConflict("work_still_running", "Desktop still owns an active process in this workspace. Wait for it to finish before acknowledging it.")
			}
			clearKey = key
			task.Blocking = false
			task.State = "needs_decision"
			decision.Reason = "stopped_confirmed"
			decision.Message = "You confirmed the previous work has stopped. Choose whether to make a new attempt."
			decision.Choices = fixedChoices(false, task.Content != "")
			l.eventLocked(task.ID, "verify", "User confirmed the previous work has stopped; no execution was replayed.")
		} else if choice == "retry" {
			if len(loopPrompt(task.Conversation, task.Content)) > maxPromptBytes {
				return &loopError{400, "history_too_large", "The saved turn is too large to retry through the local harness."}
			}
			task.State = "queued"
			task.Blocking = true
			task.Error = ""
			task.Reply = ""
			task.ExitCode = nil
			task.Verification = ""
			decision.State = "resolved"
			l.eventLocked(task.ID, "act", "User authorized a new attempt. Waiting for the saved workspace to become available.")
		} else if choice == "cancel" {
			task.State = "cancelled"
			task.Blocking = false
			decision.State = "resolved"
			l.eventLocked(task.ID, "verify", "Closed by the user without starting another attempt.")
		} else {
			task.State = "paused"
			l.eventLocked(task.ID, "act", "User left this work paused. Its decision remains available.")
		}
		task.UpdatedAt = time.Now().UTC()
		decision.Version++
		decision.Choice = choice
		return l.recordLocked(requestID, hash, id, choice)
	})
	if err != nil {
		return loopSnapshot{}, err
	}
	if clearKey != "" {
		s.mu.Lock()
		delete(s.chatWorkspaceUnknown, clearKey)
		s.mu.Unlock()
	}
	l.notify()
	return l.snapshot(), nil
}
