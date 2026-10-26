package onechat

import (
	"fmt"
	"math"
	"strings"
)

func ValidateDecision(d Decision, setups []Setup, sessions []Session, roots []string) []string {
	problems := []string{}
	if len(d.Actions) < 1 || len(d.Actions) > 3 {
		problems = append(problems, "a decision needs one to three actions")
	}
	if math.IsNaN(d.Confidence) || math.IsInf(d.Confidence, 0) || d.Confidence < 0 || d.Confidence > 1 {
		problems = append(problems, "confidence must be between zero and one")
	}
	if strings.TrimSpace(d.ReasoningSummary) == "" {
		problems = append(problems, "reasoning_summary is required")
	}
	sm, cm := map[string]Setup{}, map[string]Session{}
	for _, s := range setups {
		sm[s.ID] = s
	}
	for _, s := range sessions {
		cm[s.ID] = s
	}
	hasText, hasSend := false, false
	sent := map[string]bool{}
	for i, a := range d.Actions {
		bad := func(message string) { problems = append(problems, fmt.Sprintf("action %d: %s", i+1, message)) }
		if a.SessionID != "" {
			if _, ok := cm[a.SessionID]; !ok {
				bad("session does not exist")
			}
		}
		if a.SetupID != "" {
			if _, ok := sm[a.SetupID]; !ok {
				bad("setup does not exist")
			}
		}
		switch a.Type {
		case "answer", "ask_user":
			hasText = true
			if strings.TrimSpace(a.Text) == "" {
				bad("text is required")
			}
		case "read_status", "send":
			if a.SessionID == "" {
				bad("session_id is required")
			}
			if s, ok := cm[a.SessionID]; ok {
				if a.SetupID != "" && a.SetupID != s.SetupID {
					bad("setup does not match the session")
				}
				if a.Type == "send" && s.State == "running" {
					bad("cannot send to a running session")
				}
				if a.Type == "send" {
					if _, exists := sm[s.SetupID]; !exists {
						bad("session has no configured setup")
					}
				}
			}
			if a.Type == "send" {
				hasSend = true
				if strings.TrimSpace(a.Message) == "" {
					bad("message is required")
				}
				if sent[a.SessionID] {
					bad("duplicate send to the same session")
				}
				sent[a.SessionID] = true
			}
		case "create":
			hasSend = true
			if a.SessionID != "" {
				bad("create cannot name an existing session")
			}
			if a.SetupID == "" {
				bad("setup_id is required")
			}
			if strings.TrimSpace(a.Message) == "" || strings.TrimSpace(a.Title) == "" {
				bad("create needs title and message")
			}
			if s, ok := sm[a.SetupID]; ok {
				if s.Location == "local" {
					if _, err := allowedWorkdir(a.Workdir, roots); err != nil {
						bad(err.Error())
					}
				} else if a.Workdir != "" {
					bad("workdir is only for local create")
				}
			}
		default:
			bad("unknown action type")
		}
		if a.Type != "create" && a.Workdir != "" {
			bad("workdir is only for local create")
		}
	}
	if hasText && hasSend {
		problems = append(problems, "answer/ask_user cannot be mixed with send/create")
	}
	return problems
}
