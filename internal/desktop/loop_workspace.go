package desktop

import (
	"runtime"
	"strings"
)

func chatWorkspaceKey(d Discoverer, target invokeTarget, workdir string) string {
	d.Target = target.Mode
	d.Distro = target.Distro
	if d.useWSL() {
		return "wsl:" + strings.ToLower(target.Distro) + ":" + workdir
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(workdir)
	}
	return workdir
}
func (s *Server) acquireChatWorkspace(d Discoverer, target invokeTarget, workdir, ownTask, conversationID string) bool {
	key := chatWorkspaceKey(d, target, workdir)
	s.loopMu.Lock()
	l := s.loop
	s.loopMu.Unlock()
	if l != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if ownTask == "" && conversationID != "" && l.unresolvedLocked(conversationID) {
			return false
		}
		for _, t := range l.disk.Tasks {
			if t.ID == ownTask || !t.Blocking || (t.State == "queued" && ownTask != "") {
				continue
			}
			c := t.Conversation
			if (c.Workdir == "" || t.WorkspaceUnresolved) && c.Target == target.Mode && c.WSLDistro == target.Distro {
				return false
			}
			if chatWorkspaceKey(d, invokeTarget{Mode: c.Target, Distro: c.WSLDistro}, c.Workdir) == key {
				return false
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatWorkspaces == nil {
		s.chatWorkspaces = map[string]bool{}
	}
	if s.chatWorkspaces[key] || s.chatWorkspaceUnknown[key] {
		return false
	}
	s.chatWorkspaces[key] = true
	if s.chatWorkspaceOwners == nil {
		s.chatWorkspaceOwners = map[string]string{}
	}
	if ownTask != "" {
		s.chatWorkspaceOwners[key] = "task:" + ownTask
	} else {
		s.chatWorkspaceOwners[key] = "conversation:" + conversationID
	}
	return true
}
func (s *Server) releaseChatWorkspace(key string, confirmed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.chatWorkspaces, key)
	delete(s.chatWorkspaceOwners, key)
	if !confirmed {
		if s.chatWorkspaceUnknown == nil {
			s.chatWorkspaceUnknown = map[string]bool{}
		}
		s.chatWorkspaceUnknown[key] = true
	}
}

func (s *Server) loopConversationUnsettled(id string) bool {
	if id == "" {
		return false
	}
	s.loopMu.Lock()
	l := s.loop
	s.loopMu.Unlock()
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unresolvedLocked(id)
}
