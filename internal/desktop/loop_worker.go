package desktop

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (l *localLoop) run() {
	defer close(l.done)
	defer func() { unlockLoopFile(l.lock); _ = l.lock.Close() }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if l.ctx.Err() != nil {
			return
		}
		l.step()
		select {
		case <-l.ctx.Done():
			return
		case <-l.wake:
		case <-ticker.C:
		}
	}
}
func (l *localLoop) step() {
	snapshot := l.snapshot()
	if snapshot.StorageError != "" {
		// A heartbeat write probes recovery, but never bypasses an uncertain turn.
		_ = l.change(func() error { return nil })
		return
	}
	for _, task := range snapshot.Tasks {
		if l.ctx.Err() != nil {
			return
		}
		if task.State == "running" || task.State == "verifying" {
			// The worker is serial, so reaching another step with this state
			// means its prior completion could not be persisted. Never replay.
			l.fail(task.ID, "completion_unknown", "Execution ended while manager storage was unavailable. Check the harness and folder before another attempt.", true, task.ExitCode)
			return
		}
		if task.State != "queued" {
			continue
		}
		if l.execute(task) {
			return
		}
	}
}
func (l *localLoop) execute(task loopTask) bool {
	s := l.server
	ctx, cancel := context.WithTimeout(l.ctx, maxDelegateTO)
	defer cancel()
	tool, target, err := s.resolveDelegate(ctx, task.Conversation.Harness)
	if err != nil {
		l.fail(task.ID, "harness_unavailable", "The saved harness is unavailable. Restore it before retrying.", false, nil)
		return true
	}
	if target.Mode != task.Conversation.Target || target.Distro != task.Conversation.WSLDistro {
		l.fail(task.ID, "runtime_changed", "Desktop's runtime changed. Restore this conversation's saved runtime before retrying.", false, nil)
		return true
	}
	d := s.opts.Discoverer
	d.Target = target.Mode
	d.Distro = target.Distro
	workdir, err := s.chatWorkdir(ctx, d, target, task.Conversation.Workdir)
	if err != nil || (!task.WorkspaceUnresolved && workdir != task.Conversation.Workdir) {
		l.fail(task.ID, "workdir_changed", "The saved working folder is unavailable or changed. Review it before retrying.", false, nil)
		return true
	}
	if !s.acquireChatWorkspace(d, target, workdir, task.ID, task.Conversation.ID) {
		return false
	}
	key := chatWorkspaceKey(d, target, workdir)
	// Persistent unknown completion becomes the guard; the in-memory process
	// lease is released only once its adapter returns.
	defer s.releaseChatWorkspace(key, true)
	admitted := false
	err = l.change(func() error {
		for i := range l.disk.Tasks {
			t := &l.disk.Tasks[i]
			if t.ID == task.ID && t.State == "queued" {
				t.Conversation.Workdir = workdir
				t.WorkspaceUnresolved = false
				t.State = "running"
				t.Blocking = true
				t.Attempt++
				t.UpdatedAt = time.Now().UTC()
				admitted = true
				l.eventLocked(t.ID, "act", "Starting the saved harness with this conversation's frozen transcript.")
				return nil
			}
		}
		return loopConflict("work_changed", "This work changed before execution.")
	})
	if err != nil || !admitted {
		return true
	}
	var tail strings.Builder
	lines := &jsonLineWriter{emit: func(line string) error {
		if tail.Len()+len(line) > 2<<20 {
			old := tail.String()
			tail.Reset()
			keep := 1 << 20
			if len(old) < keep {
				keep = len(old)
			}
			tail.WriteString(old[len(old)-keep:])
		}
		tail.WriteString(line)
		tail.WriteByte('\n')
		return nil
	}}
	result, runErr := runDelegateWithOptions(ctx, d, tool.forLocalChat(), target, loopPrompt(task.Conversation, task.Content), maxDelegateTO, delegateOptions{Workdir: workdir, Stdout: lines})
	if runErr != nil {
		unknown := errors.Is(runErr, errWSLCleanupUnconfirmed)
		reason, message := "execution_failed", "The harness could not finish this turn. Review its work before retrying."
		if unknown {
			reason = "completion_unknown"
			message = "Process cleanup could not be confirmed. Check the harness and folder before allowing another attempt."
		}
		l.fail(task.ID, reason, message, unknown, nil)
		return true
	}
	if !result.TimedOut && ctx.Err() == nil {
		_ = lines.Flush()
	}
	if result.TimedOut || ctx.Err() != nil || result.ExitCode != 0 {
		message := "The harness exited unsuccessfully. Review its work before retrying."
		if result.TimedOut {
			message = "The harness reached its time limit. Review its work before retrying."
		} else if ctx.Err() != nil {
			message = "Desktop stopped this attempt. Review its work before retrying."
		}
		l.fail(task.ID, "execution_failed", message, false, &result.ExitCode)
		return true
	}
	if err = l.change(func() error {
		for i := range l.disk.Tasks {
			t := &l.disk.Tasks[i]
			if t.ID == task.ID {
				t.State = "verifying"
				t.UpdatedAt = time.Now().UTC()
				t.ExitCode = &result.ExitCode
				l.eventLocked(t.ID, "verify", "The native process exited successfully. Checking for a complete public assistant reply.")
				return nil
			}
		}
		return errors.New("work missing")
	}); err != nil {
		return true
	}
	output := result.Stdout
	if result.StdoutTruncated {
		output = tail.String()
	}
	reply := loopPublicReply(output, task.Conversation.Harness)
	if lines.dropped || reply == "" {
		l.fail(task.ID, "reply_unverified", "The process exited, but a complete public assistant reply could not be verified. Review the harness and folder before deciding what to do.", false, &result.ExitCode)
		return true
	}
	_ = l.change(func() error {
		for i := range l.disk.Tasks {
			t := &l.disk.Tasks[i]
			if t.ID == task.ID {
				t.State = "completed"
				t.Blocking = false
				t.Reply = reply
				t.Error = ""
				t.ExitCode = &result.ExitCode
				t.UpdatedAt = time.Now().UTC()
				t.Verification = "Verified native process exit and public assistant reply. The requested work's real-world outcome is not independently verified."
				l.eventLocked(t.ID, "verify", t.Verification)
				return nil
			}
		}
		return errors.New("work missing")
	})
	return true
}
func (l *localLoop) fail(id, reason, message string, unknown bool, exit *int) {
	_ = l.change(func() error {
		for i := range l.disk.Tasks {
			t := &l.disk.Tasks[i]
			if t.ID == id {
				t.State = "needs_decision"
				t.Blocking = unknown
				t.Error = message
				t.ExitCode = exit
				t.UpdatedAt = time.Now().UTC()
				l.eventLocked(t.ID, "detect", message)
				l.askLocked(t, reason, message, unknown)
				return nil
			}
		}
		return errors.New("work missing")
	})
}
