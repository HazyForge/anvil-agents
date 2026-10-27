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

type turnAdmission struct {
	TurnID   string `json:"turnId"`
	Queued   bool   `json:"queued"`
	Position int    `json:"position"`
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
		return turnAdmission{TurnID: h.turn.TurnID}, nil
	}
	for i, turn := range h.pending {
		if turn.RequestID == requestID {
			return turnAdmission{turn.TurnID, true, i + 1}, nil
		}
	}
	for _, finished := range h.finished {
		if finished.requestID == requestID {
			return turnAdmission{TurnID: finished.turnID}, nil
		}
	}
	if h.closing {
		return nil, &protocolError{"internal", "onechat is stopping; reconnect after it restarts"}
	}
	turn := &runningTurn{TurnID: uuid.NewString(), Text: text, RequestID: requestID, By: by, Events: []json.RawMessage{}}
	if h.turn != nil {
		h.pending = append(h.pending, turn)
		position := len(h.pending)
		h.eventLocked("turn.queued", time.Now().UTC(), map[string]any{"turnId": turn.TurnID, "position": position}, false)
		return turnAdmission{turn.TurnID, true, position}, nil
	}
	h.beginTurnLocked(turn)
	return turnAdmission{TurnID: turn.TurnID}, nil
}

func (h *Host) beginTurnLocked(turn *runningTurn) {
	id := uuid.MustParse(turn.TurnID)
	ctx, cancel := context.WithCancel(onechat.WithTurnID(h.ctx, id))
	turn.StartedAt, turn.cancel = time.Now().UTC(), cancel
	h.turn = turn
	h.eventLocked("status", time.Now().UTC(), h.statusLocked(), false)
	h.eventLocked("turn.started", time.Now().UTC(), map[string]any{"turnId": turn.TurnID, "text": turn.Text, "requestId": turn.RequestID, "by": turn.By}, true)
	h.workers.Add(1)
	go h.runTurn(ctx, turn)
}

func (h *Host) rememberTurnLocked(turn *runningTurn) {
	h.finished = append(h.finished, completedTurn{turn.RequestID, turn.TurnID})
	if len(h.finished) > 100 {
		h.finished = h.finished[len(h.finished)-100:]
	}
}

func (h *Host) queuePositionsLocked() {
	for i, turn := range h.pending {
		h.eventLocked("turn.queued", time.Now().UTC(), map[string]any{"turnId": turn.TurnID, "position": i + 1}, false)
	}
}

func (h *Host) cancelTurn(id string) *protocolError {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.turn != nil && h.turn.TurnID == strings.ToLower(id) {
		h.turn.cancel()
		return nil
	}
	for i, turn := range h.pending {
		if turn.TurnID != strings.ToLower(id) {
			continue
		}
		h.pending = append(h.pending[:i], h.pending[i+1:]...)
		h.rememberTurnLocked(turn)
		h.eventLocked("turn.finished", time.Now().UTC(), map[string]any{"turnId": turn.TurnID, "error": context.Canceled.Error(), "answer": "", "totals": onechat.Totals{}}, false)
		h.queuePositionsLocked()
		return nil
	}
	return &protocolError{"not_found", "that turn is not running or queued"}
}

func (h *Host) runTurn(ctx context.Context, turn *runningTurn) {
	defer h.workers.Done()
	var trace onechat.TurnTrace
	err := ctx.Err()
	if err == nil {
		trace, err = h.manager.TurnObserved(ctx, turn.Text, func(event onechat.Event) {
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
	h.eventLocked("turn.finished", time.Now().UTC(), map[string]any{"turnId": turn.TurnID, "error": failure, "answer": trace.Answer, "totals": trace.Totals}, true)
	sessions, _ := h.manager.LastInventory()
	h.eventLocked("sessions.changed", time.Now().UTC(), map[string]any{"sessions": withoutTranscripts(sessions)}, false)
	h.rememberTurnLocked(turn)
	turn.cancel()
	h.turn = nil
	if h.closing || h.ctx.Err() != nil {
		for _, queued := range h.pending {
			h.rememberTurnLocked(queued)
			h.eventLocked("turn.finished", time.Now().UTC(), map[string]any{"turnId": queued.TurnID, "error": "onechat stopped before this turn was dispatched", "answer": "", "totals": onechat.Totals{}}, false)
		}
		h.pending = nil
	}
	if len(h.pending) > 0 {
		next := h.pending[0]
		h.pending = h.pending[1:]
		h.beginTurnLocked(next)
		h.queuePositionsLocked()
	} else {
		h.eventLocked("status", time.Now().UTC(), h.statusLocked(), false)
	}
}
