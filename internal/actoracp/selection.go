package actoracp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrSelection      = errors.New("ACP model/mode selection could not be verified")
	ErrCatalogTimeout = errors.New("ACP model/mode catalog readiness timed out")
)

const maxCatalogOptions = 2048

type catalogOption struct {
	current string
	values  map[string]struct{}
}

// Native catalogs are replaced atomically at read time. Names/descriptions,
// provider settings and credentials never enter this selection proof.
type catalogSnapshot struct {
	options map[string]catalogOption
	err     error
}

type configOptionWire struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Current string            `json:"currentValue"`
	Options []json.RawMessage `json:"options"`
}

func validSelectionValue(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, " \t\r\n\x00")
}

func parseCatalog(raw json.RawMessage) catalogSnapshot {
	var list []configOptionWire
	if len(raw) == 0 {
		return catalogSnapshot{options: map[string]catalogOption{}}
	}
	if json.Unmarshal(raw, &list) != nil || len(list) > 64 {
		return catalogSnapshot{err: ErrSelection}
	}
	result := catalogSnapshot{options: map[string]catalogOption{}}
	for _, item := range list {
		if item.ID != "model" && item.ID != "mode" {
			continue
		}
		if _, duplicate := result.options[item.ID]; duplicate || item.Type != "select" || !validSelectionValue(item.Current) {
			return catalogSnapshot{err: ErrSelection}
		}
		option := catalogOption{current: item.Current, values: map[string]struct{}{}}
		count := 0
		var add func([]json.RawMessage, bool) bool
		add = func(entries []json.RawMessage, grouped bool) bool {
			for _, entry := range entries {
				count++
				if count > maxCatalogOptions {
					return false
				}
				var value struct {
					Value   string            `json:"value"`
					Options []json.RawMessage `json:"options"`
				}
				if json.Unmarshal(entry, &value) != nil {
					return false
				}
				if value.Options != nil {
					if grouped || value.Value != "" || !add(value.Options, true) {
						return false
					}
					continue
				}
				if !validSelectionValue(value.Value) {
					return false
				}
				if _, duplicate := option.values[value.Value]; duplicate {
					return false
				}
				option.values[value.Value] = struct{}{}
			}
			return true
		}
		if !add(item.Options, false) {
			return catalogSnapshot{err: ErrSelection}
		}
		if _, advertised := option.values[item.Current]; !advertised {
			return catalogSnapshot{err: ErrSelection}
		}
		result.options[item.ID] = option
	}
	return result
}

func (s catalogSnapshot) advertises(model, mode string) bool {
	if s.err != nil {
		return false
	}
	m, okModel := s.options["model"]
	a, okMode := s.options["mode"]
	_, hasModel := m.values[model]
	_, hasMode := a.values[mode]
	return okModel && okMode && hasModel && hasMode
}

func (s catalogSnapshot) selected(model, mode string) bool {
	return s.advertises(model, mode) && s.options["model"].current == model && s.options["mode"].current == mode
}

func (c *Client) trackCatalogs(limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.catalogLimit = limit
	c.catalogs = map[string]catalogSnapshot{}
}

func (c *Client) catalog(id string) catalogSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.catalogs[id]
	if !ok {
		return catalogSnapshot{err: ErrSelection}
	}
	return value
}

func (c *Client) forgetCatalog(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.catalogs, id)
}

// Called only by readLoop with mu held, before a result or update becomes
// visible to a caller. Idle updates therefore cannot race behind a prompt's
// dispatch-time selection check.
func (c *Client) recordCatalog(msg message, pending pendingCall) bool {
	if c.catalogLimit == 0 {
		return true
	}
	var id string
	var raw json.RawMessage
	if msg.Method == "session/update" {
		var params struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				Kind    string          `json:"sessionUpdate"`
				Options json.RawMessage `json:"configOptions"`
			} `json:"update"`
		}
		if json.Unmarshal(msg.Params, &params) != nil {
			return false
		}
		if params.Update.Kind != "config_option_update" {
			return true
		}
		id, raw = params.SessionID, params.Update.Options
		if _, known := c.catalogs[id]; !known {
			return true
		}
	} else if msg.Error == nil && (pending.method == "session/new" || pending.method == "session/set_config_option") {
		var result struct {
			SessionID string          `json:"sessionId"`
			Options   json.RawMessage `json:"configOptions"`
		}
		if json.Unmarshal(msg.Result, &result) != nil {
			return false
		}
		id, raw = pending.session, result.Options
		if pending.method == "session/new" {
			id = result.SessionID
			if !validSessionID(id) {
				return false
			}
			if _, exists := c.catalogs[id]; exists || len(c.catalogs) >= c.catalogLimit {
				return false
			}
		}
	} else {
		return true
	}
	if id == "" {
		return false
	}
	c.catalogs[id] = parseCatalog(raw)
	select {
	case c.catalogChanged <- struct{}{}:
	default:
	}
	return true
}

func (s *Server) selectionEnabled() bool { return s.cfg.ExpectedModel != "" }

func (s *Server) selectSession(ctx context.Context, id string) error {
	if !s.selectionEnabled() {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return ErrCatalogTimeout
			}
			return err
		}
		if !s.client.ready() {
			return ErrClosed
		}
		catalog := s.client.catalog(id)
		if catalog.err != nil {
			return ErrSelection
		}
		if catalog.advertises(s.cfg.ExpectedModel, s.cfg.ExpectedMode) {
			for _, choice := range []struct{ key, value string }{{"model", s.cfg.ExpectedModel}, {"mode", s.cfg.ExpectedMode}} {
				if s.client.catalog(id).options[choice.key].current == choice.value {
					continue
				}
				raw, err := s.client.call(ctx, "session/set_config_option", map[string]any{"sessionId": id, "configId": choice.key, "value": choice.value}, id, nil, nil)
				var acknowledged struct {
					Options json.RawMessage `json:"configOptions"`
				}
				if err != nil || json.Unmarshal(raw, &acknowledged) != nil {
					return ErrSelection
				}
				// Validate this correlated response before consulting mutable state.
				// A later notification cannot repair a rejected or malformed ack.
				ack := parseCatalog(acknowledged.Options)
				if !ack.advertises(s.cfg.ExpectedModel, s.cfg.ExpectedMode) || ack.options[choice.key].current != choice.value {
					return ErrSelection
				}
				confirmed := s.client.catalog(id)
				if !confirmed.advertises(s.cfg.ExpectedModel, s.cfg.ExpectedMode) || confirmed.options[choice.key].current != choice.value {
					return ErrSelection
				}
			}
			if !s.client.catalog(id).selected(s.cfg.ExpectedModel, s.cfg.ExpectedMode) {
				return ErrSelection
			}
			return nil
		}
		select {
		case <-ctx.Done():
		case <-s.client.closed:
			return ErrClosed
		case <-s.client.catalogChanged:
		// One coalesced global signal can be consumed by another setup. The
		// bounded observation tick reads cached metadata only; it sends no RPC.
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (s *Server) releaseRejectedSession(id string) {
	if id == "" {
		return
	}
	if s.supportsClose && s.client.ready() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := s.client.call(ctx, "session/close", map[string]any{"sessionId": id}, id, nil, nil)
		cancel()
		if err == nil {
			s.client.forgetCatalog(id)
			return
		}
	}
	// A harness without a close surface cannot prove the rejected session
	// was released. Close the owned connection; never replay or replace it.
	_ = s.client.Close()
}
