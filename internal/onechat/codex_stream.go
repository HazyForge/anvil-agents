package onechat

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// Codex emits tool output as well as replies. Keep only the evidence used by
// Send while publishing activity as each line arrives, so a long session's
// command output does not displace its final reply or grow memory indefinitely.
type codexStream struct {
	hooks    RunHooks
	pending  []byte
	dropping bool
	overflow bool
	thread   string
	message  string
	usage    string
	failure  string
}

func (s *codexStream) Write(raw []byte) (int, error) {
	count := len(raw)
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, '\n')
		part := raw
		if end >= 0 {
			part = raw[:end]
		}
		if !s.dropping {
			if len(s.pending)+len(part) > outputLimit {
				s.pending, s.dropping, s.overflow = nil, true, true
			} else {
				s.pending = append(s.pending, part...)
			}
		}
		if end < 0 {
			break
		}
		if !s.dropping {
			s.line(s.pending)
		}
		s.pending, s.dropping = s.pending[:0], false
		raw = raw[end+1:]
	}
	return count, nil
}

func (s *codexStream) finish() {
	if !s.dropping && len(s.pending) > 0 {
		s.line(s.pending)
	}
	s.pending = nil
}

func (s *codexStream) line(raw []byte) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Text    string `json:"text"`
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	switch event.Type {
	case "thread.started":
		s.thread = string(raw)
		if _, err := uuid.Parse(event.ThreadID); err == nil && s.hooks.NativeSession != nil {
			s.hooks.NativeSession(event.ThreadID)
		}
	case "turn.completed":
		s.usage = string(raw)
	case "turn.failed", "error":
		s.failure = string(raw)
	case "item.started", "item.updated", "item.completed":
		switch event.Item.Type {
		case "command_execution":
			if s.hooks.Activity != nil {
				s.hooks.Activity("command", "Bash: "+commandSummary(event.Item.Command))
			}
		case "file_change":
			paths := []string{}
			for _, change := range event.Item.Changes {
				if len(paths) == 5 {
					paths = append(paths, "…")
					break
				}
				paths = append(paths, activityLine(change.Path, 100))
			}
			if s.hooks.Activity != nil {
				s.hooks.Activity("file_change", "Changed "+strings.Join(paths, ", "))
			}
		case "agent_message":
			if event.Type == "item.completed" {
				s.message = string(raw)
				if s.hooks.Activity != nil {
					s.hooks.Activity("agent_message", "Agent message received")
				}
			}
		}
	}
}

func (s *codexStream) String() string {
	return strings.Join([]string{s.thread, s.message, s.usage, s.failure}, "\n")
}

func (s *codexStream) Overflow() bool {
	return s.overflow || len(s.thread)+len(s.message)+len(s.usage)+len(s.failure) > outputLimit
}
