package harnessprotocol

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHarnessAdapterSteerAndInterrupt(t *testing.T) {
	sessionID := "test-session-123"
	adapter := NewHarnessAdapter(sessionID)

	// Verify steer
	steerReq := &Envelope{
		Protocol:  ProtocolVersion,
		SessionID: sessionID,
		Type:      MessageTypeRequest,
		ID:        "req-1",
		Name:      "harness.steer",
		Payload:   json.RawMessage(`{"instruction":"focus on unit tests","urgent":true}`),
	}

	resp, err := adapter.DispatchRequest(context.Background(), steerReq)
	if err != nil {
		t.Fatalf("unexpected error on steer: %v", err)
	}
	if resp.Name != "harness.steer.ack" {
		t.Fatalf("expected harness.steer.ack, got %s", resp.Name)
	}

	// Read event
	select {
	case ev := <-adapter.Events():
		if ev.Name != "state.transition" {
			t.Fatalf("expected state.transition event, got %s", ev.Name)
		}
	default:
		t.Fatal("expected event in queue")
	}

	// Verify interrupt
	interruptReq := &Envelope{
		Protocol:  ProtocolVersion,
		SessionID: sessionID,
		Type:      MessageTypeRequest,
		ID:        "req-2",
		Name:      "harness.interrupt",
		Payload:   json.RawMessage(`{"reason":"user requested stop","saveState":true}`),
	}

	resp, err = adapter.DispatchRequest(context.Background(), interruptReq)
	if err != nil {
		t.Fatalf("unexpected error on interrupt: %v", err)
	}
	if resp.Name != "harness.interrupt.ack" {
		t.Fatalf("expected harness.interrupt.ack, got %s", resp.Name)
	}

	if adapter.GetState() != "interrupted" {
		t.Fatalf("expected state interrupted, got %s", adapter.GetState())
	}
}

func TestHarnessAdapterAgySteerAndToolCall(t *testing.T) {
	sessionID := "agy-session-456"
	adapter := NewHarnessAdapter(sessionID)

	// Test tool registration / call
	adapter.RegisterHandler("harness.tool.execute", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		var toolCall ToolCallPayload
		if err := json.Unmarshal(env.Payload, &toolCall); err != nil {
			return nil, err
		}
		adapter.EmitEvent("tool.executed", ToolCallPayload{
			CallID:   toolCall.CallID,
			ToolName: toolCall.ToolName,
			Output:   "success",
		})
		return adapter.newResponse(env.ID, "harness.tool.execute.ack", map[string]string{
			"callId": toolCall.CallID,
			"status": "completed",
		})
	})

	toolReq := &Envelope{
		Protocol:  ProtocolVersion,
		SessionID: sessionID,
		Type:      MessageTypeRequest,
		ID:        "req-agy-1",
		Name:      "harness.tool.execute",
		Payload:   json.RawMessage(`{"callId":"call-1","toolName":"read_file","arguments":{"path":"README.md"}}`),
	}

	resp, err := adapter.DispatchRequest(context.Background(), toolReq)
	if err != nil {
		t.Fatalf("unexpected error on tool call: %v", err)
	}
	if resp.Name != "harness.tool.execute.ack" {
		t.Fatalf("expected harness.tool.execute.ack, got %s", resp.Name)
	}

	select {
	case ev := <-adapter.Events():
		if ev.Name != "tool.executed" {
			t.Fatalf("expected tool.executed event, got %s", ev.Name)
		}
	default:
		t.Fatal("expected event in queue")
	}
}
