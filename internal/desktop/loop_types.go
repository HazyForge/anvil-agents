package desktop

import "time"

type loopMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type loopConversation struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Harness   string        `json:"harness"`
	Workdir   string        `json:"workdir"`
	Target    string        `json:"target"`
	WSLDistro string        `json:"wslDistro,omitempty"`
	Messages  []loopMessage `json:"messages"`
}
type loopTask struct {
	ID                  string           `json:"id"`
	RequestID           string           `json:"requestId"`
	Conversation        loopConversation `json:"conversation"`
	Content             string           `json:"content"`
	Origin              string           `json:"origin"`
	State               string           `json:"state"`
	Blocking            bool             `json:"blocking"`
	Attempt             int              `json:"attempt"`
	CreatedAt           time.Time        `json:"createdAt"`
	UpdatedAt           time.Time        `json:"updatedAt"`
	Reply               string           `json:"reply,omitempty"`
	ExitCode            *int             `json:"exitCode,omitempty"`
	Error               string           `json:"error,omitempty"`
	Verification        string           `json:"verification,omitempty"`
	Fingerprint         string           `json:"fingerprint,omitempty"`
	SubmittedWorkdir    string           `json:"submittedWorkdir"`
	WorkspaceUnresolved bool             `json:"workspaceUnresolved,omitempty"`
}
type loopChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type loopDecision struct {
	ID      string       `json:"id"`
	TaskID  string       `json:"taskId"`
	Version int          `json:"version"`
	State   string       `json:"state"`
	Reason  string       `json:"reason"`
	Message string       `json:"message"`
	Choices []loopChoice `json:"choices"`
	Choice  string       `json:"choice,omitempty"`
}
type loopEvent struct {
	ID      int64     `json:"id"`
	TaskID  string    `json:"taskId"`
	Stage   string    `json:"stage"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}
type loopSnapshot struct {
	Revision     int64          `json:"revision"`
	Tasks        []loopTask     `json:"tasks"`
	Decisions    []loopDecision `json:"decisions"`
	Events       []loopEvent    `json:"events"`
	StorageError string         `json:"storageError,omitempty"`
}
type loopReplay struct {
	RequestID  string `json:"requestId"`
	Hash       string `json:"hash"`
	DecisionID string `json:"decisionId,omitempty"`
	Choice     string `json:"choice,omitempty"`
}
type loopDisk struct {
	Version int `json:"version"`
	loopSnapshot
	Requests  []loopReplay `json:"requests"`
	NextEvent int64        `json:"nextEvent"`
}
type loopWorkRequest struct {
	RequestID    string           `json:"requestId"`
	Conversation loopConversation `json:"conversation"`
	Content      string           `json:"content"`
}
type loopObserveRequest struct {
	RequestID    string           `json:"requestId"`
	Conversation loopConversation `json:"conversation"`
	Pending      bool             `json:"pending"`
	LastError    string           `json:"lastError,omitempty"`
}
type loopChoiceRequest struct {
	RequestID string `json:"requestId"`
	Choice    string `json:"choice"`
	Version   int    `json:"version"`
}
