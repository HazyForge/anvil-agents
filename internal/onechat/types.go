// Package onechat manages one conversation across local harnesses and cloud chat.
package onechat

import (
	"context"
	"encoding/json"
	"time"
)

type Setup struct {
	ID             string   `json:"id"`
	Location       string   `json:"location"`
	Harness        string   `json:"harness"`
	Model          string   `json:"model,omitempty"`
	Tags           []string `json:"tags"`
	Notes          string   `json:"notes"`
	PermissionMode string   `json:"permissionMode,omitempty"`
	AllowedTools   []string `json:"allowedTools,omitempty"`
	Sandbox        string   `json:"sandbox,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	Profile        string   `json:"profile,omitempty"`
}

type Message struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type Session struct {
	ID              string    `json:"id"`
	Location        string    `json:"location"`
	SetupID         string    `json:"setupId"`
	Harness         string    `json:"harness"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	State           string    `json:"state"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	Workdir         string    `json:"workdir,omitempty"`
	NativeSessionID string    `json:"nativeSessionId,omitempty"`
	Sandbox         string    `json:"sandbox,omitempty"`
	Transcript      []Message `json:"transcript"`
	BaseURL         string    `json:"baseURL,omitempty"`
	Namespace       string    `json:"namespace,omitempty"`
	ThreadID        string    `json:"threadId,omitempty"`
	Profile         string    `json:"profile,omitempty"`
}

// Pointer counts distinguish reported zero from unknown. Cached token counts are
// kept separate; providers disagree on whether inputTokens already includes them.
type Usage struct {
	Model                    string   `json:"model"`
	InputTokens              *int64   `json:"inputTokens"`
	OutputTokens             *int64   `json:"outputTokens"`
	CacheReadInputTokens     *int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens *int64   `json:"cacheCreationInputTokens"`
	CostUSD                  *float64 `json:"costUsd"`
	DurationMS               int64    `json:"durationMs"`
}

type Action struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	SetupID   string `json:"setup_id"`
	Title     string `json:"title"`
	Workdir   string `json:"workdir"`
	Message   string `json:"message"`
	Text      string `json:"text"`
}

type Decision struct {
	ReasoningSummary string          `json:"reasoning_summary"`
	Confidence       float64         `json:"confidence"`
	Actions          []Action        `json:"actions"`
	Raw              json.RawMessage `json:"-"`
}

type DecideInput struct {
	Instructions string    `json:"instructions"`
	Setups       []Setup   `json:"setups"`
	Inventory    []Session `json:"inventory"`
	History      []Message `json:"history"`
	Message      string    `json:"message"`
}

type ComposeInput struct {
	Message  string         `json:"message"`
	Decision Decision       `json:"decision"`
	Outcomes []ActionResult `json:"outcomes"`
}

type Decider interface {
	Decide(context.Context, DecideInput) (Decision, Usage, error)
	Compose(context.Context, ComposeInput) (string, Usage, error)
}

type Receipt struct {
	Local *LocalReceipt `json:"local,omitempty"`
	Cloud *CloudReceipt `json:"cloud,omitempty"`
}

type LocalReceipt struct {
	Binary          string `json:"binary"`
	Workdir         string `json:"workdir"`
	NativeSessionID string `json:"nativeSessionId"`
	ExitCode        *int   `json:"exitCode"`
	Sandbox         string `json:"sandbox,omitempty"`
}

type CloudReceipt struct {
	BaseURL    string   `json:"baseURL"`
	Namespace  string   `json:"namespace"`
	ThreadID   string   `json:"threadId"`
	TurnID     string   `json:"turnId"`
	RunName    string   `json:"runName"`
	MessageIDs []string `json:"messageIds"`
	RequestID  string   `json:"requestId,omitempty"`
	Created    *bool    `json:"created,omitempty"`
}

type ActionResult struct {
	ScenarioLabel string  `json:"scenarioLabel,omitempty"`
	SessionTitle  string  `json:"sessionTitle,omitempty"`
	Action        Action  `json:"action"`
	SessionID     string  `json:"sessionId"`
	SentText      string  `json:"sentText"`
	Reply         string  `json:"reply"`
	State         string  `json:"state"`
	Receipt       Receipt `json:"receipt"`
	Usage         Usage   `json:"usage"`
	DurationMS    int64   `json:"durationMs"`
	Error         string  `json:"error"`
	HarnessCalled bool    `json:"harnessCalled"`
	Harness       string  `json:"harness"`
}

type Totals struct {
	Usage
	HarnessCalls    map[string]int `json:"harnessCalls"`
	ManagerCalls    int            `json:"managerCalls"`
	CallsWithTokens int            `json:"callsWithTokens"`
	CallsWithCost   int            `json:"callsWithCost"`
}

type Check struct {
	Pass  bool     `json:"pass"`
	Notes []string `json:"notes"`
}

type TurnTrace struct {
	Version          string         `json:"version"`
	ID               string         `json:"id"`
	At               time.Time      `json:"at"`
	Message          string         `json:"message"`
	Inventory        []Session      `json:"inventory"`
	History          []Message      `json:"history"`
	Setups           []Setup        `json:"setups"`
	RawDecision      any            `json:"rawDecision"`
	Decision         Decision       `json:"decision"`
	ValidationErrors []string       `json:"validationErrors"`
	DeciderUsage     Usage          `json:"deciderUsage"`
	ComposeUsage     Usage          `json:"composeUsage"`
	Actions          []ActionResult `json:"actions"`
	Answer           string         `json:"answer"`
	Totals           Totals         `json:"totals"`
	Error            string         `json:"error"`
	Check            *Check         `json:"check,omitempty"`
	TracePath        string         `json:"tracePath"`
}

// Backend returns execution evidence even when an operation fails.
type Backend interface {
	List(context.Context) ([]Session, error)
	Create(context.Context, Setup, string, string) (Session, Receipt, error)
	Send(context.Context, Setup, Session, string) (ActionResult, error)
}

func addUsage(total *Totals, u Usage) {
	add := func(dst **int64, src *int64) {
		if src != nil {
			if *dst == nil {
				*dst = new(int64)
			}
			**dst += *src
		}
	}
	add(&total.InputTokens, u.InputTokens)
	add(&total.OutputTokens, u.OutputTokens)
	add(&total.CacheReadInputTokens, u.CacheReadInputTokens)
	add(&total.CacheCreationInputTokens, u.CacheCreationInputTokens)
	if u.InputTokens != nil || u.OutputTokens != nil {
		total.CallsWithTokens++
	}
	if u.CostUSD != nil {
		if total.CostUSD == nil {
			total.CostUSD = new(float64)
		}
		*total.CostUSD += *u.CostUSD
		total.CallsWithCost++
	}
	total.DurationMS += u.DurationMS
}
