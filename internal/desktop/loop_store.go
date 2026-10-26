package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const maxLoopTasks = 200
const maxLoopRequests = 2000
const maxLoopEvents = 2000
const maxLoopFile = 32 << 20

type loopError struct {
	status        int
	code, message string
}

func (e *loopError) Error() string            { return e.message }
func loopConflict(code, message string) error { return &loopError{http.StatusConflict, code, message} }
func writeLoopError(w http.ResponseWriter, err error) {
	var e *loopError
	if errors.As(err, &e) {
		writeError(w, e.status, e.code, e.message)
		return
	}
	writeError(w, http.StatusServiceUnavailable, "loop_unavailable", "The persistent manager is unavailable. Its existing work has not been replayed.")
}

type localLoop struct {
	server       *Server
	mu           sync.Mutex
	disk         loopDisk
	path         string
	lock         *os.File
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	storageError string
}

func (s *Server) getLoop() (*localLoop, error) {
	s.loopMu.Lock()
	defer s.loopMu.Unlock()
	if s.loopClosed {
		return nil, errors.New("desktop closed")
	}
	if s.loop != nil {
		return s.loop, nil
	}
	prefs, err := prefsPath(s.opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Dir(prefs))
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "manager-loop.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockLoopFile(lock); err != nil {
		_ = lock.Close()
		return nil, loopConflict("loop_in_use", "Another Desktop host owns this manager store. Use that host or a separate config directory.")
	}
	fail := func(err error) (*localLoop, error) { unlockLoopFile(lock); _ = lock.Close(); return nil, err }
	l := &localLoop{server: s, path: filepath.Join(dir, "manager-loop.json"), lock: lock, wake: make(chan struct{}, 1), done: make(chan struct{})}
	l.disk = loopDisk{Version: 1, loopSnapshot: loopSnapshot{Tasks: []loopTask{}, Decisions: []loopDecision{}, Events: []loopEvent{}}, Requests: []loopReplay{}}
	f, err := os.Open(l.path)
	if err == nil {
		raw, readErr := io.ReadAll(io.LimitReader(f, maxLoopFile+1))
		_ = f.Close()
		if readErr != nil || len(raw) > maxLoopFile {
			return fail(errors.New("manager store exceeds limit or cannot be read"))
		}
		if err = json.Unmarshal(raw, &l.disk); err != nil {
			return fail(fmt.Errorf("invalid manager store: %w", err))
		}
		if err := validateLoopDisk(l.disk); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	recovered := false
	for i := range l.disk.Tasks {
		t := &l.disk.Tasks[i]
		if t.State == "running" || t.State == "verifying" {
			t.State = "needs_decision"
			t.Blocking = true
			t.Error = "Desktop restarted before completion was durably verified. Check the harness and folder before resuming."
			t.UpdatedAt = time.Now().UTC()
			l.eventLocked(t.ID, "detect", t.Error)
			l.askLocked(t, "completion_unknown", t.Error, true)
			recovered = true
		}
	}
	if recovered {
		l.disk.Revision++
		if err = l.persistLocked(); err != nil {
			return fail(err)
		}
	}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	s.loop = l
	go l.run()
	return l, nil
}

// Close stops host-owned work and releases the private store lock. Browser
// disconnection never calls this; only the Desktop host lifecycle does.
func (s *Server) Close() {
	s.loopMu.Lock()
	s.loopClosed = true
	l := s.loop
	s.loopMu.Unlock()
	if l == nil {
		return
	}
	l.cancel()
	<-l.done
}

func (l *localLoop) snapshot() loopSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out loopSnapshot
	raw, _ := json.Marshal(l.disk.loopSnapshot)
	_ = json.Unmarshal(raw, &out)
	out.StorageError = l.storageError
	return out
}
func (l *localLoop) notify() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
func (l *localLoop) persistLocked() error {
	raw, err := json.Marshal(l.disk)
	if err != nil {
		return err
	}
	if len(raw) > maxLoopFile {
		return errors.New("manager store capacity reached")
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".manager-loop-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, l.path); err != nil {
		return err
	}
	if err := syncLoopDirectory(filepath.Dir(l.path)); err != nil {
		return &loopCommittedError{err}
	}
	return nil
}

type loopCommittedError struct{ error }

func (l *localLoop) restore(raw []byte) {
	var restored loopDisk
	_ = json.Unmarshal(raw, &restored)
	l.disk = restored
}

// Mutations are copy-on-write. A failed durable write never admits new work.
func (l *localLoop) change(fn func() error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	before, _ := json.Marshal(l.disk)
	if err := fn(); err != nil {
		l.restore(before)
		return err
	}
	l.disk.Revision++
	if err := l.persistLocked(); err != nil {
		var committed *loopCommittedError
		if !errors.As(err, &committed) {
			l.restore(before)
		}
		l.storageError = "Manager state could not be saved. New execution is blocked until storage recovers."
		return err
	}
	l.storageError = ""
	return nil
}
func (l *localLoop) eventLocked(task, stage, message string) {
	l.disk.NextEvent++
	l.disk.Events = append(l.disk.Events, loopEvent{ID: l.disk.NextEvent, TaskID: task, Stage: stage, Message: message, At: time.Now().UTC()})
	if len(l.disk.Events) > maxLoopEvents {
		l.disk.Events = l.disk.Events[len(l.disk.Events)-maxLoopEvents:]
	}
}
func fixedChoices(unknown bool, retry bool) []loopChoice {
	if unknown {
		return []loopChoice{{"checked_stopped", "I checked that this work has stopped"}, {"pause", "Leave paused"}}
	}
	out := []loopChoice{}
	if retry {
		out = append(out, loopChoice{"retry", "Retry this work"})
	}
	return append(out, loopChoice{"pause", "Leave paused"}, loopChoice{"cancel", "Close without retrying"})
}
func (l *localLoop) askLocked(t *loopTask, reason, message string, unknown bool) {
	for i := range l.disk.Decisions {
		d := &l.disk.Decisions[i]
		if d.TaskID == t.ID && d.State == "pending" {
			return
		}
	}
	d := loopDecision{ID: uuid.NewString(), TaskID: t.ID, Version: 1, State: "pending", Reason: reason, Message: message, Choices: fixedChoices(unknown, t.Content != "")}
	l.disk.Decisions = append(l.disk.Decisions, d)
	l.eventLocked(t.ID, "ask", message)
}
func loopHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validLoopRequest(id string) bool { _, err := uuid.Parse(id); return err == nil && len(id) == 36 }
func (l *localLoop) replayLocked(id, hash string) (bool, error) {
	for _, r := range l.disk.Requests {
		if r.RequestID == id {
			if r.Hash != hash {
				return false, loopConflict("request_conflict", "This request ID already belongs to a different manager action.")
			}
			return true, nil
		}
	}
	return false, nil
}
func (l *localLoop) recordLocked(id, hash, decisionID, choice string) error {
	if len(l.disk.Requests) >= maxLoopRequests {
		return loopConflict("loop_capacity", "The local manager has reached its request capacity. Existing records are preserved.")
	}
	l.disk.Requests = append(l.disk.Requests, loopReplay{RequestID: id, Hash: hash, DecisionID: decisionID, Choice: choice})
	return nil
}
func validateLoopConversation(c loopConversation) error {
	if strings.TrimSpace(c.ID) == "" || len(c.ID) > 128 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 240 || len(c.Workdir) > 4096 || len(c.Messages) > 400 {
		return &loopError{400, "invalid_conversation", "The conversation identity, folder, or history is invalid or too large."}
	}
	if c.Target != HarnessTargetNative && c.Target != HarnessTargetWSL {
		return &loopError{400, "invalid_runtime", "Confirm the conversation's native or WSL runtime first."}
	}
	if (c.Target == HarnessTargetNative && c.WSLDistro != "") || (c.WSLDistro != "" && !safeDistroName(c.WSLDistro)) {
		return &loopError{400, "invalid_runtime", "The saved runtime binding is invalid."}
	}
	tool, ok := catalogTool(c.Harness)
	if !ok || !tool.Delegatable() {
		return &loopError{400, "invalid_harness", "The conversation's harness is not supported."}
	}
	total := 0
	for _, m := range c.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return &loopError{400, "invalid_history", "Only saved public user and assistant messages are accepted."}
		}
		total += len(m.Content)
	}
	if total > maxPromptBytes {
		return &loopError{400, "history_too_large", "The conversation history exceeds the local prompt limit."}
	}
	return nil
}
func loopPrompt(c loopConversation, content string) string {
	if len(c.Messages) == 0 {
		return content
	}
	messages := append(append([]loopMessage(nil), c.Messages...), loopMessage{Role: "user", Content: content})
	raw, _ := json.Marshal(messages)
	return "Continue this conversation. Prior messages below are context; respond to the latest user message. Use your tools for requested work.\n" + string(raw)
}
func (l *localLoop) unresolvedLocked(id string) bool {
	for _, t := range l.disk.Tasks {
		if t.Conversation.ID == id && t.State != "completed" && t.State != "cancelled" {
			return true
		}
	}
	return false
}
func (l *localLoop) decisionReplay(decisionID, requestID string) (string, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.disk.Requests {
		if r.RequestID == requestID {
			if r.DecisionID != decisionID {
				return "", false, loopConflict("request_conflict", "This request ID belongs to a different manager action.")
			}
			return r.Choice, true, nil
		}
	}
	return "", false, nil
}
