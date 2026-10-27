package host

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
)

type runningTurn struct {
	TurnID    string            `json:"turnId"`
	Text      string            `json:"text"`
	StartedAt time.Time         `json:"startedAt"`
	RequestID string            `json:"requestId"`
	By        clientInfo        `json:"by"`
	Events    []json.RawMessage `json:"events"`
	cancel    context.CancelFunc
}
type completedTurn struct{ requestID, turnID string }
type status struct {
	State               string       `json:"state"`
	Turn                *runningTurn `json:"turn"`
	SessionsRefreshedAt time.Time    `json:"sessionsRefreshedAt"`
}

func (h *Host) statusLocked() status {
	_, at := h.manager.LastInventory()
	result := status{State: "idle", SessionsRefreshedAt: at}
	if h.turn != nil {
		turn := *h.turn
		turn.Events = append([]json.RawMessage{}, turn.Events...)
		result.State, result.Turn = "busy", &turn
	}
	return result
}
func (h *Host) broadcastLocked(raw []byte) {
	for c := range h.clients {
		c.enqueue(raw)
	}
}
func (h *Host) eventLocked(name string, at time.Time, body any, inTurn bool) {
	event := struct {
		Type   string    `json:"type"`
		Event  string    `json:"event"`
		TurnID string    `json:"turnId,omitempty"`
		Seq    int       `json:"seq,omitempty"`
		At     time.Time `json:"at"`
		Body   any       `json:"body"`
	}{Type: "event", Event: name, At: at, Body: body}
	if inTurn {
		event.TurnID, event.Seq = h.turn.TurnID, len(h.turn.Events)+1
	}
	raw := frame(event)
	if inTurn {
		h.turn.Events = append(h.turn.Events, json.RawMessage(raw))
	}
	h.broadcastLocked(raw)
}

func (h *Host) startTurn(text, requestID string, by clientInfo) (any, *protocolError) {
	if len(text) > 64<<10 {
		return nil, &protocolError{"too_large", "turn text exceeds 64 KiB"}
	}
	if strings.TrimSpace(text) == "" {
		return nil, invalid("turn text is required")
	}
	if !validUUID(requestID) {
		return nil, invalid("requestId must be a canonical UUID")
	}
	requestID = strings.ToLower(requestID)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.turn != nil && h.turn.RequestID == requestID {
		return map[string]string{"turnId": h.turn.TurnID}, nil
	}
	for _, finished := range h.finished {
		if finished.requestID == requestID {
			return map[string]string{"turnId": finished.turnID}, nil
		}
	}
	if h.turn != nil {
		return nil, &protocolError{"busy", "onechat is busy; wait for idle status before starting another turn"}
	}
	if h.closing {
		return nil, &protocolError{"internal", "onechat is stopping; reconnect after it restarts"}
	}
	id := uuid.New()
	ctx, cancel := context.WithCancel(onechat.WithTurnID(h.ctx, id))
	h.turn = &runningTurn{TurnID: id.String(), Text: text, StartedAt: time.Now().UTC(), RequestID: requestID, By: by, Events: []json.RawMessage{}, cancel: cancel}
	h.eventLocked("status", time.Now().UTC(), h.statusLocked(), false)
	h.eventLocked("turn.started", time.Now().UTC(), map[string]any{"turnId": id.String(), "text": text, "requestId": requestID, "by": by}, true)
	h.workers.Add(1)
	go h.runTurn(ctx, text, h.refreshDone)
	return map[string]string{"turnId": id.String()}, nil
}

func (h *Host) runTurn(ctx context.Context, text string, refreshDone <-chan struct{}) {
	defer h.workers.Done()
	// A turn takes priority over new refreshes, but an existing refresh must
	// release the manager before the turn reads its own inventory.
	if refreshDone != nil {
		select {
		case <-refreshDone:
		case <-ctx.Done():
		}
	}
	var trace onechat.TurnTrace
	err := ctx.Err()
	if err == nil {
		trace, err = h.manager.TurnObserved(ctx, text, func(event onechat.Event) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.eventLocked(event.Name, event.At, event.Body, true)
		})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	failure := trace.Error
	if failure == "" && err != nil {
		failure = err.Error()
	}
	if ctx.Err() != nil {
		failure = ctx.Err().Error()
	}
	h.eventLocked("turn.finished", time.Now().UTC(), map[string]any{"turnId": h.turn.TurnID, "error": failure, "answer": trace.Answer, "totals": trace.Totals}, true)
	sessions, _ := h.manager.LastInventory()
	h.eventLocked("sessions.changed", time.Now().UTC(), map[string]any{"sessions": withoutTranscripts(sessions)}, false)
	h.finished = append(h.finished, completedTurn{h.turn.RequestID, h.turn.TurnID})
	if len(h.finished) > 100 {
		h.finished = h.finished[len(h.finished)-100:]
	}
	h.turn.cancel()
	h.turn = nil
	h.eventLocked("status", time.Now().UTC(), h.statusLocked(), false)
}
