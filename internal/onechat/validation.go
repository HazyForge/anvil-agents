package onechat

import (
	"fmt"
	"math"
	"strings"
)

// NormalizeDecision clears fields an action cannot use before validation, so
// a harmless slip (a workdir or setup on a send) does not discard the whole
// turn. A send, read or stop always runs in its session's own folder and setup;
// only a local create takes a workdir. The raw decision stays in the trace.
func NormalizeDecision(d *Decision, setups []Setup) {
	location := map[string]string{}
	for _, s := range setups {
		location[s.ID] = s.Location
	}
	for i := range d.Actions {
		a := &d.Actions[i]
		switch a.Type {
		case "create":
			if location[a.SetupID] != "local" {
				a.Workdir = ""
			}
		case "send", "read_status", "stop":
			a.Workdir, a.SetupID = "", ""
		default:
			a.Workdir = ""
		}
	}
}

func ValidateDecision(d Decision, setups []Setup, sessions []Session, roots []string) []string {
	problems := []string{}
	if len(d.Actions) < 1 || len(d.Actions) > 4 {
		problems = append(problems, "a decision needs one to four actions")
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
			if strings.TrimSpace(a.Text) == "" {
				bad("text is required")
			}
		case "read_status", "send", "stop":
			if a.SessionID == "" {
				bad("session_id is required")
			}
			if s, ok := cm[a.SessionID]; ok {
				if a.SetupID != "" && a.SetupID != s.SetupID {
					bad("setup does not match the session")
				}
				if a.Type == "send" {
					if _, exists := sm[s.SetupID]; !exists {
						bad("session has no configured setup")
					}
				}
			}
			if a.Type == "send" {
				if strings.TrimSpace(a.Message) == "" {
					bad("message is required")
				}
				if sent[a.SessionID] {
					bad("duplicate send to the same session")
				}
				sent[a.SessionID] = true
			}
		case "create":
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
	// A message can cover several subjects: work for one session, an answer
	// about another, a question about a third. One question per turn keeps
	// the person's reply unambiguous.
	asks := 0
	for _, a := range d.Actions {
		if a.Type == "ask_user" {
			asks++
		}
	}
	if asks > 1 {
		problems = append(problems, "ask at most one question per turn")
	}
	return problems
}
