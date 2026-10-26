// Package localrouting plans manager actions against Anvil-owned local
// conversation descriptors. It never discovers native histories or executes a
// harness. Callers retain and revalidate the original conversation binding.
package localrouting

import (
	"time"

	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

const (
	MaxConversations      = 200
	MaxContext            = 6
	StatusReady           = "ready"
	StatusRunning         = "running"
	StatusUnconfirmed     = "unconfirmed"
	ActionContinue        = "continue"
	ActionReadStatus      = "read_status"
	ActionNewConversation = "new_conversation"
	ActionGeneral         = "general"
	ActionWait            = "wait"
	ActionClarify         = "clarify"
)

// Conversation is a browser-owned Anvil wrapper, not a native harness session.
// Workdirs, credentials and full transcripts intentionally have no fields here.
type Conversation struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Summary   string     `json:"summary"`
	Aliases   []string   `json:"aliases,omitempty"`
	Harness   string     `json:"harness"`
	Target    string     `json:"target"`
	WSLDistro string     `json:"wslDistro,omitempty"`
	Status    string     `json:"status"`
	TouchedAt *time.Time `json:"touchedAt,omitempty"`
}

type Message struct {
	Role           string     `json:"role"`
	Content        string     `json:"content"`
	ConversationID string     `json:"conversationId,omitempty"`
	At             *time.Time `json:"at,omitempty"`
}

type Request struct {
	Content        string         `json:"content"`
	Conversations  []Conversation `json:"conversations"`
	Context        []Message      `json:"context,omitempty"`
	TargetID       string         `json:"targetId,omitempty"`
	DefaultHarness string         `json:"defaultHarness,omitempty"`
}

// Runtime comes from the loopback host's current discovery, never from the
// routing request. Now is injectable because the replay runner owns its clock.
type Runtime struct {
	Target             string    `json:"target"`
	WSLDistro          string    `json:"wslDistro,omitempty"`
	AvailableHarnesses []string  `json:"availableHarnesses"`
	Busy               bool      `json:"busy,omitempty"`
	Now                time.Time `json:"now"`
}

type Action struct {
	Kind     string `json:"kind"`
	TargetID string `json:"targetId,omitempty"`
	Reason   string `json:"reason"`
}

type Result struct {
	Decision   subjectrouting.Decision    `json:"decision"`
	Action     Action                     `json:"action"`
	Candidates []subjectrouting.Candidate `json:"candidates"`
	LatencyMS  int64                      `json:"latencyMs"`
}
