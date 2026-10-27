package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hazyforge/anvil-agents/internal/onechat"
)

type clientInfo struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type request struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Op   string          `json:"op"`
	Body json.RawMessage `json:"body"`
}
type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func invalid(message string) *protocolError { return &protocolError{"invalid", message} }

type client struct {
	host  *Host
	conn  transport
	out   chan []byte
	done  chan struct{}
	once  sync.Once
	hello bool
	by    clientInfo
}

func (c *client) close() { c.once.Do(func() { close(c.done); _ = c.conn.Abort() }) }
func (c *client) enqueue(raw []byte) {
	if raw == nil {
		c.close()
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.out <- raw:
	default:
		c.close()
	}
}
func (c *client) reply(id string, body any, err *protocolError) {
	c.enqueue(frame(struct {
		Type  string         `json:"type"`
		ID    string         `json:"id"`
		OK    bool           `json:"ok"`
		Body  any            `json:"body,omitempty"`
		Error *protocolError `json:"error,omitempty"`
	}{"response", id, err == nil, body, err}))
}
func (c *client) writeLoop() {
	defer c.host.workers.Done()
	defer c.close()
	for {
		select {
		case <-c.done:
			return
		case raw := <-c.out:
			if err := c.conn.WriteText(raw); err != nil {
				return
			}
		}
	}
}
func (c *client) readLoop() {
	defer c.host.workers.Done()
	defer func() { c.close(); c.host.mu.Lock(); delete(c.host.clients, c); c.host.mu.Unlock() }()
	for {
		op, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var req request
		err = json.Unmarshal(raw, &req)
		if err != nil || op != 1 || req.Type != "request" || req.ID == "" || utf8.RuneCountInString(req.ID) > 64 || req.Op == "" || !objectBody(req.Body) {
			id := requestID(raw)
			if id != "" {
				c.reply(id, nil, invalid("send a JSON request with a string id (1–64 characters), op and object body"))
			}
			continue
		}
		if !c.hello && req.Op != "hello" {
			c.reply(req.ID, nil, invalid("send hello before other operations"))
			continue
		}
		body, failure := c.handle(req)
		c.reply(req.ID, body, failure)
	}
}

func objectBody(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

// Recover a top-level id even when a later field makes the frame malformed.
func requestID(raw []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return ""
	}
	id := ""
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			break
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			break
		}
		if key == "id" {
			_ = json.Unmarshal(value, &id)
		}
	}
	return id
}

func decodeBody(raw json.RawMessage, value any) *protocolError {
	if err := json.Unmarshal(raw, value); err != nil {
		return invalid("operation body has invalid fields")
	}
	return nil
}

func (c *client) handle(req request) (any, *protocolError) {
	h := c.host
	switch req.Op {
	case "hello":
		var body struct {
			Client clientInfo `json:"client"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		if strings.TrimSpace(body.Client.Name) == "" || utf8.RuneCountInString(body.Client.Name) > 128 {
			return nil, invalid("client.name must contain 1–128 characters")
		}
		switch body.Client.Kind {
		case "desktop", "ios", "cli", "test":
		default:
			return nil, invalid("client.kind must be desktop, ios, cli or test")
		}
		c.by, c.hello = body.Client, true
		h.mu.Lock()
		defer h.mu.Unlock()
		return map[string]any{"protocol": 1, "host": map[string]string{"id": h.info.HostID, "name": h.name, "version": h.info.Version}, "status": h.statusLocked()}, nil
	case "status":
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.statusLocked(), nil
	case "sessions.list":
		var body struct {
			Refresh bool `json:"refresh"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		return h.sessions(c, body.Refresh)
	case "session.get":
		var body struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		sessions, _ := h.manager.LastInventory()
		for _, session := range sessions {
			if session.ID == body.SessionID {
				return map[string]any{"session": session}, nil
			}
		}
		return nil, &protocolError{"not_found", "session is not in the latest inventory; refresh sessions when idle"}
	case "turn.start":
		var body struct {
			Text      string `json:"text"`
			RequestID string `json:"requestId"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		return h.startTurn(body.Text, body.RequestID, c.by)
	case "turn.cancel":
		var body struct {
			TurnID string `json:"turnId"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.turn == nil || h.turn.TurnID != strings.ToLower(body.TurnID) {
			return nil, &protocolError{"not_found", "that turn is not running"}
		}
		h.turn.cancel()
		return map[string]any{}, nil
	case "trace.get":
		var body struct {
			TurnID string `json:"turnId"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		trace, err := h.trace(body.TurnID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"trace": trace}, nil
	case "history.list":
		var body struct {
			Limit *int `json:"limit"`
		}
		if err := decodeBody(req.Body, &body); err != nil {
			return nil, err
		}
		limit := 50
		if body.Limit != nil {
			limit = *body.Limit
		}
		if limit < 1 || limit > 200 {
			return nil, invalid("history limit must be between 1 and 200")
		}
		turns, err := h.history(limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"messages": h.manager.History(limit), "turns": turns}, nil
	default:
		return nil, invalid("unknown operation")
	}
}

func (h *Host) sessions(c *client, refresh bool) (any, *protocolError) {
	h.mu.Lock()
	busy := h.turn != nil
	refresh = refresh && !busy && h.refreshDone == nil && !h.closing
	if refresh {
		h.refreshDone = make(chan struct{})
	}
	h.mu.Unlock()
	if refresh {
		ctx, cancel := context.WithCancel(h.ctx)
		defer cancel()
		go func() {
			select {
			case <-c.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		_, err := h.manager.Inventory(ctx)
		h.mu.Lock()
		close(h.refreshDone)
		h.refreshDone = nil
		busy = h.turn != nil
		h.mu.Unlock()
		if err != nil {
			return nil, &protocolError{"internal", "could not refresh inventory; check the onechat configuration and retry"}
		}
	}
	sessions, at := h.manager.LastInventory()
	return map[string]any{"sessions": withoutTranscripts(sessions), "refreshedAt": at, "stale": busy}, nil
}

// Inventory snapshots are detached copies, so list views can omit transcripts
// without changing the complete session returned by session.get.
func withoutTranscripts(sessions []onechat.Session) []onechat.Session {
	for i := range sessions {
		sessions[i].Transcript = []onechat.Message{}
	}
	return sessions
}

func (h *Host) trace(id string) (onechat.TurnTrace, *protocolError) {
	var trace onechat.TurnTrace
	if !validUUID(id) {
		return trace, invalid("turnId must be a canonical UUID")
	}
	raw, err := os.ReadFile(filepath.Join(h.dir, "traces", strings.ToLower(id)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return trace, &protocolError{"not_found", "no saved trace for that turn"}
	}
	if err != nil {
		return trace, &protocolError{"internal", "could not read the saved trace"}
	}
	if err = json.Unmarshal(raw, &trace); err != nil {
		return trace, &protocolError{"internal", "saved trace is invalid JSON"}
	}
	return trace, nil
}

type turnSummary struct {
	TurnID      string         `json:"turnId"`
	At          time.Time      `json:"at"`
	Message     string         `json:"message"`
	Answer      string         `json:"answer"`
	Error       string         `json:"error"`
	ActionCount int            `json:"actionCount"`
	Totals      onechat.Totals `json:"totals"`
}

func (h *Host) history(limit int) ([]turnSummary, *protocolError) {
	entries, err := os.ReadDir(filepath.Join(h.dir, "traces"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, &protocolError{"internal", "could not list saved traces"}
	}
	type candidate struct {
		id       string
		modified time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validUUID(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{id, info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
	out := make([]turnSummary, 0, limit)
	for _, file := range candidates {
		if len(out) == limit {
			break
		}
		trace, err := h.trace(file.id)
		if err != nil {
			continue
		}
		out = append(out, turnSummary{trace.ID, trace.At, trace.Message, trace.Answer, trace.Error, len(trace.Actions), trace.Totals})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}
