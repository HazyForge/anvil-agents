package harnessprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ProtocolVersion defines the current version of the Anvil Harness Protocol.
const ProtocolVersion = "anvil.harness/v1alpha1"

// MessageType indicates whether a message is an event, request, or response.
type MessageType string

const (
	MessageTypeEvent    MessageType = "event"
	MessageTypeRequest  MessageType = "request"
	MessageTypeResponse MessageType = "response"
)

// Envelope is the standard framing for all programmatic harness communication.
type Envelope struct {
	Protocol  string          `json:"protocol"`
	SessionID string          `json:"sessionId"`
	Type      MessageType     `json:"type"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

// StateTransitionPayload represents lifecycle changes of the harness.
type StateTransitionPayload struct {
	FromState string `json:"fromState"`
	ToState   string `json:"toState"`
	Reason    string `json:"reason,omitempty"`
}

// SteerPayload injects guidance or mid-run instruction into an active thought loop.
type SteerPayload struct {
	Instruction string `json:"instruction"`
	Urgent      bool   `json:"urgent,omitempty"`
}

// InterruptPayload requests cancellation of the current generation while preserving state.
type InterruptPayload struct {
	Reason    string `json:"reason"`
	SaveState bool   `json:"saveState"`
}

// ToolCallPayload describes a granular tool invocation request or completion.
type ToolCallPayload struct {
	CallID    string          `json:"callId"`
	ToolName  string          `json:"toolName"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    string          `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Handler handles incoming protocol messages.
type Handler interface {
	HandleMessage(ctx context.Context, env *Envelope) (*Envelope, error)
}

// HarnessAdapter bridges native engine protocols to Anvil's programmatic protocol.
type HarnessAdapter struct {
	sessionID string
	mu        sync.RWMutex
	state     string
	handlers  map[string]func(ctx context.Context, env *Envelope) (*Envelope, error)
	events    chan *Envelope
}

// NewHarnessAdapter creates a new protocol adapter for a session.
func NewHarnessAdapter(sessionID string) *HarnessAdapter {
	adapter := &HarnessAdapter{
		sessionID: sessionID,
		state:     "initialized",
		handlers:  make(map[string]func(ctx context.Context, env *Envelope) (*Envelope, error)),
		events:    make(chan *Envelope, 100),
	}
	adapter.registerDefaultHandlers()
	return adapter
}

func (a *HarnessAdapter) registerDefaultHandlers() {
	a.RegisterHandler("harness.steer", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		var steer SteerPayload
		if err := json.Unmarshal(env.Payload, &steer); err != nil {
			return nil, fmt.Errorf("invalid steer payload: %w", err)
		}
		a.EmitEvent("state.transition", StateTransitionPayload{
			FromState: a.GetState(),
			ToState:   "steering",
			Reason:    steer.Instruction,
		})
		return a.newResponse(env.ID, "harness.steer.ack", map[string]string{"status": "accepted"})
	})

	a.RegisterHandler("harness.interrupt", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		var interrupt InterruptPayload
		if err := json.Unmarshal(env.Payload, &interrupt); err != nil {
			return nil, fmt.Errorf("invalid interrupt payload: %w", err)
		}
		a.SetState("interrupted")
		a.EmitEvent("state.transition", StateTransitionPayload{
			FromState: a.GetState(),
			ToState:   "interrupted",
			Reason:    interrupt.Reason,
		})
		return a.newResponse(env.ID, "harness.interrupt.ack", map[string]string{"status": "interrupted"})
	})
}

// RegisterHandler registers an RPC request handler.
func (a *HarnessAdapter) RegisterHandler(name string, handler func(ctx context.Context, env *Envelope) (*Envelope, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handlers[name] = handler
}

// DispatchRequest routes an incoming RPC request to the registered handler.
func (a *HarnessAdapter) DispatchRequest(ctx context.Context, env *Envelope) (*Envelope, error) {
	a.mu.RLock()
	handler, ok := a.handlers[env.Name]
	a.mu.RUnlock()

	if !ok {
		return nil, errors.New("unknown method: " + env.Name)
	}
	return handler(ctx, env)
}

// EmitEvent broadcasts a programmatic event into the event channel.
func (a *HarnessAdapter) EmitEvent(name string, payload interface{}) {
	raw, _ := json.Marshal(payload)
	env := &Envelope{
		Protocol:  ProtocolVersion,
		SessionID: a.sessionID,
		Type:      MessageTypeEvent,
		Name:      name,
		Payload:   raw,
		Timestamp: time.Now().UTC(),
	}
	select {
	case a.events <- env:
	default:
	}
}

// Events returns the outbound event channel.
func (a *HarnessAdapter) Events() <-chan *Envelope {
	return a.events
}

// SetState updates the current state.
func (a *HarnessAdapter) SetState(state string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

// GetState returns the current state.
func (a *HarnessAdapter) GetState() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *HarnessAdapter) newResponse(reqID, name string, payload interface{}) (*Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Envelope{
		Protocol:  ProtocolVersion,
		SessionID: a.sessionID,
		Type:      MessageTypeResponse,
		ID:        reqID,
		Name:      name,
		Payload:   raw,
		Timestamp: time.Now().UTC(),
	}, nil
}
