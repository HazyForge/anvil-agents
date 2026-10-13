package actoracp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxStreamBytes = 512 << 10

// Config is operator-owned. HTTP callers cannot select an executable, working
// directory, model, credentials or MCP servers. Sessions survive turns only
// while this actor/harness connection survives; session/load is not supported.
type Config struct {
	Workdir        string
	TurnTimeout    time.Duration
	MaxSessions    int
	ExpectedModel  string
	ExpectedMode   string
	CatalogTimeout time.Duration
}

type session struct {
	nativeID          string
	busy              bool
	promptSent        bool
	cancelRequested   bool
	setupCancel       context.CancelFunc
	dispatchCancelled atomic.Bool
}

type Server struct {
	client        *Client
	cfg           Config
	mu            sync.Mutex
	sessions      map[string]*session
	setup         chan struct{}
	supportsClose bool
}

func NewServer(ctx context.Context, client *Client, cfg Config) (*Server, error) {
	if client == nil || !filepath.IsAbs(cfg.Workdir) {
		return nil, errors.New("ACP client and absolute working directory required")
	}
	if (cfg.ExpectedModel == "") != (cfg.ExpectedMode == "") {
		return nil, errors.New("expected-model and expected-mode must be set together")
	}
	if cfg.ExpectedModel != "" && (!validSelectionValue(cfg.ExpectedModel) || !strings.Contains(cfg.ExpectedModel, "/") || strings.HasPrefix(cfg.ExpectedModel, "/") || strings.HasSuffix(cfg.ExpectedModel, "/") || !validSelectionValue(cfg.ExpectedMode)) {
		return nil, errors.New("fixed provider/model and mode identifiers required")
	}
	if cfg.CatalogTimeout == 0 {
		cfg.CatalogTimeout = 5 * time.Second
	}
	if cfg.CatalogTimeout < 0 || cfg.CatalogTimeout > 30*time.Second {
		return nil, errors.New("catalog readiness timeout must be positive and at most 30 seconds")
	}
	if cfg.TurnTimeout <= 0 {
		cfg.TurnTimeout = 2 * time.Minute
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 64
	}
	if cfg.ExpectedModel != "" {
		client.trackCatalogs(cfg.MaxSessions)
	}
	result, err := client.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": "anvil-actor-acp", "version": "adapter"},
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}, "", nil, nil)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	var initialized struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if json.Unmarshal(result, &initialized) != nil || initialized.ProtocolVersion != 1 {
		_ = client.Close()
		return nil, ErrProtocol
	}
	_, supportsClose := initialized.AgentCapabilities.SessionCapabilities["close"]
	return &Server{client: client, cfg: cfg, sessions: map[string]*session{}, setup: make(chan struct{}, 1), supportsClose: supportsClose}, nil
}

func validSessionID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, " \t\r\n\x00")
}

func (s *Server) ensureSession(ctx context.Context, id string) (*session, error) {
	s.mu.Lock()
	current := s.sessions[id]
	if current != nil && current.nativeID != "" {
		s.mu.Unlock()
		return current, nil
	}
	if current == nil {
		if len(s.sessions) >= s.cfg.MaxSessions {
			s.mu.Unlock()
			return nil, errors.New("ACP session capacity reached")
		}
		current = &session{}
		s.sessions[id] = current
	}
	timeout := 5 * time.Second
	if s.selectionEnabled() {
		timeout = s.cfg.CatalogTimeout
	}
	setupCtx, cancel := context.WithTimeout(ctx, timeout)
	if s.selectionEnabled() {
		current.setupCancel = cancel
	}
	if current.cancelRequested && s.selectionEnabled() {
		cancel()
	}
	s.mu.Unlock()
	initialized := false
	defer func() {
		cancel()
		s.mu.Lock()
		current.setupCancel = nil
		if !initialized && s.sessions[id] == current && current.nativeID == "" && !current.busy {
			delete(s.sessions, id)
		}
		s.mu.Unlock()
	}()
	// The setup queue shares the same deadline as catalog readiness. Waiting
	// behind another conversation cannot extend the operator's setup bound.
	select {
	case s.setup <- struct{}{}:
	case <-setupCtx.Done():
		return nil, setupCtx.Err()
	}
	defer func() { <-s.setup }()
	s.mu.Lock()
	if current.nativeID != "" {
		s.mu.Unlock()
		initialized = true
		return current, nil
	}
	s.mu.Unlock()
	result, err := s.client.call(setupCtx, "session/new", map[string]any{"cwd": s.cfg.Workdir, "mcpServers": []any{}}, "", nil, nil)
	if err != nil {
		return nil, err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(result, &created) != nil || !validSessionID(created.SessionID) {
		_ = s.client.Close()
		return nil, ErrProtocol
	}
	selected := false
	defer func() {
		if !selected {
			s.releaseRejectedSession(created.SessionID)
		}
	}()
	if err := s.selectSession(setupCtx, created.SessionID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current.cancelRequested && s.selectionEnabled() {
		return nil, context.Canceled
	}
	for _, existing := range s.sessions {
		if existing.nativeID == created.SessionID {
			_ = s.client.Close()
			return nil, ErrProtocol
		}
	}
	current.nativeID = created.SessionID
	selected = true
	initialized = true
	return current, nil
}

func rpcResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func rpcFailure(w http.ResponseWriter, id json.RawMessage, code int, text string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: text}})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") {
		if r.URL.Path == "/readyz" && !s.client.ready() {
			http.Error(w, "harness unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/" {
		http.Error(w, "method or path unavailable", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFrameBytes+1))
	if err != nil || len(body) > maxFrameBytes {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var request message
	if json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" || len(request.Result) > 0 || request.Error != nil {
		rpcFailure(w, nil, -32600, "invalid request")
		return
	}
	if request.Method == "session/cancel" && len(request.ID) == 0 {
		s.cancel(w, r, request.Params)
		return
	}
	var idString string
	var idNumber json.Number
	if len(request.ID) == 0 || len(request.ID) > 128 || (json.Unmarshal(request.ID, &idString) != nil && json.Unmarshal(request.ID, &idNumber) != nil) || string(request.ID) == "null" {
		rpcFailure(w, nil, -32600, "request id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.TurnTimeout)
	defer cancel()
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ProtocolVersion != 1 {
			rpcFailure(w, request.ID, -32602, "protocol version 1 required")
			return
		}
		rpcResult(w, request.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "anvil-actor-acp", "version": "adapter"}, "agentCapabilities": map[string]any{"promptCapabilities": map[string]any{}}})
	case "session/new":
		var params struct {
			Cwd        string            `json:"cwd"`
			MCPServers []json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(request.Params, &params) != nil || (params.Cwd != "" && params.Cwd != s.cfg.Workdir) || len(params.MCPServers) != 0 {
			rpcFailure(w, request.ID, -32602, "session settings are operator owned")
			return
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			rpcFailure(w, request.ID, -32603, "session creation failed")
			return
		}
		id := hex.EncodeToString(random[:])
		if _, err := s.ensureSession(ctx, id); err != nil {
			rpcFailure(w, request.ID, -32000, "harness session unavailable")
			return
		}
		rpcResult(w, request.ID, map[string]any{"sessionId": id})
	case "session/prompt":
		s.prompt(ctx, w, request)
	default:
		rpcFailure(w, request.ID, -32601, "method unavailable")
	}
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &params) != nil || !validSessionID(params.SessionID) {
		http.Error(w, "invalid session", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	current := s.sessions[params.SessionID]
	busy := current != nil && current.busy
	defer s.mu.Unlock()
	if busy {
		current.cancelRequested = true
		current.dispatchCancelled.Store(true)
		if !current.promptSent {
			if current.setupCancel != nil {
				current.setupCancel()
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		err := s.client.notify(ctx, "session/cancel", map[string]any{"sessionId": current.nativeID})
		cancel()
		if err != nil {
			http.Error(w, "harness unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) prompt(ctx context.Context, w http.ResponseWriter, request message) {
	var params struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if json.Unmarshal(request.Params, &params) != nil || !validSessionID(params.SessionID) || len(params.Prompt) == 0 {
		rpcFailure(w, request.ID, -32602, "session and text prompt required")
		return
	}
	bytes := 0
	for _, block := range params.Prompt {
		if block.Type != "text" {
			rpcFailure(w, request.ID, -32602, "only text prompts supported")
			return
		}
		bytes += len(block.Text)
	}
	if bytes == 0 || bytes > 256<<10 {
		rpcFailure(w, request.ID, -32602, "prompt size invalid")
		return
	}
	s.mu.Lock()
	current := s.sessions[params.SessionID]
	if current == nil {
		if len(s.sessions) >= s.cfg.MaxSessions {
			s.mu.Unlock()
			rpcFailure(w, request.ID, -32000, "session capacity reached")
			return
		}
		current = &session{}
		s.sessions[params.SessionID] = current
	}
	if current.busy {
		s.mu.Unlock()
		rpcFailure(w, request.ID, -32000, "session busy")
		return
	}
	current.busy = true
	current.promptSent = false
	current.cancelRequested = false
	current.dispatchCancelled.Store(false)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		current.busy = false
		if current.nativeID == "" && s.sessions[params.SessionID] == current {
			delete(s.sessions, params.SessionID)
		}
		s.mu.Unlock()
	}()
	catalogCtx := ctx
	catalogCancel := func() {}
	if s.selectionEnabled() {
		catalogCtx, catalogCancel = context.WithTimeout(ctx, s.cfg.CatalogTimeout)
	}
	defer catalogCancel()
	if _, err := s.ensureSession(catalogCtx, params.SessionID); err != nil {
		s.mu.Lock()
		cancelled := current.cancelRequested
		s.mu.Unlock()
		if cancelled {
			rpcResult(w, request.ID, map[string]any{"stopReason": "cancelled"})
			return
		}
		rpcFailure(w, request.ID, -32000, "harness session unavailable")
		return
	}
	if s.selectionEnabled() {
		selectionCtx, selectionCancel := catalogCtx, catalogCancel
		s.mu.Lock()
		current.setupCancel = selectionCancel
		if current.cancelRequested {
			selectionCancel()
		}
		s.mu.Unlock()
		err := s.selectSession(selectionCtx, current.nativeID)
		selectionCancel()
		s.mu.Lock()
		current.setupCancel = nil
		cancelled := current.cancelRequested
		s.mu.Unlock()
		if cancelled {
			rpcResult(w, request.ID, map[string]any{"stopReason": "cancelled"})
			return
		}
		if err != nil {
			rpcFailure(w, request.ID, -32000, "harness selection unavailable")
			return
		}
	}
	s.mu.Lock()
	cancelled := current.cancelRequested
	s.mu.Unlock()
	if cancelled {
		rpcResult(w, request.ID, map[string]any{"stopReason": "cancelled"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	controller := http.NewResponseController(w)
	streamBytes := 0
	textBytes := 0
	write := func(value any, terminal bool) error {
		body, err := json.Marshal(value)
		limit := maxStreamBytes - 4096
		if terminal {
			limit = maxStreamBytes
		}
		if err != nil || streamBytes+len(body)+1 > limit {
			return errors.New("ACP output limit exceeded")
		}
		streamBytes += len(body) + 1
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err = w.Write(append(body, '\n')); err != nil {
			return err
		}
		return controller.Flush()
	}
	result, err := s.client.callChecked(ctx, "session/prompt", map[string]any{"sessionId": current.nativeID, "prompt": params.Prompt}, current.nativeID, func(raw json.RawMessage) error {
		var event struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &event) != nil || event.SessionID != current.nativeID {
			return ErrProtocol
		}
		if s.selectionEnabled() && event.Update.SessionUpdate == "config_option_update" {
			// Inspect the ordered frame itself. Later catalog snapshots cannot
			// wash away a violation observed during this active prompt.
			var catalogEvent struct {
				Update struct {
					Options json.RawMessage `json:"configOptions"`
				} `json:"update"`
			}
			if json.Unmarshal(raw, &catalogEvent) != nil || !parseCatalog(catalogEvent.Update.Options).selected(s.cfg.ExpectedModel, s.cfg.ExpectedMode) {
				return ErrSelection
			}
			return nil
		}
		// Publish only assistant text. Thoughts, tool traces and user echoes
		// never become AgentRun output or an assistant reply.
		if event.Update.SessionUpdate != "agent_message_chunk" {
			return nil
		}
		if event.Update.Content.Type != "text" {
			return ErrProtocol
		}
		textBytes += len(event.Update.Content.Text)
		return write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": params.SessionID, "update": event.Update}}, false)
	}, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current.promptSent = true
		if !current.cancelRequested {
			return nil
		}
		cancelCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		return s.client.notify(cancelCtx, "session/cancel", map[string]any{"sessionId": current.nativeID})
	}, func() error {
		if current.dispatchCancelled.Load() {
			return context.Canceled
		}
		if s.selectionEnabled() && !s.client.catalog(current.nativeID).selected(s.cfg.ExpectedModel, s.cfg.ExpectedMode) {
			return ErrSelection
		}
		return nil
	})
	var completed struct {
		StopReason string `json:"stopReason"`
	}
	if err == nil && s.selectionEnabled() && !s.client.catalog(current.nativeID).selected(s.cfg.ExpectedModel, s.cfg.ExpectedMode) {
		err = ErrSelection
	}
	if err == nil {
		if json.Unmarshal(result, &completed) != nil {
			err = ErrProtocol
		}
		switch completed.StopReason {
		case "end_turn", "max_tokens", "max_turn_requests", "refusal", "cancelled":
		default:
			err = ErrProtocol
		}
		if completed.StopReason == "end_turn" && textBytes == 0 {
			err = ErrProtocol
		}
	}
	if err != nil {
		if errors.Is(err, ErrProtocol) || errors.Is(err, ErrSelection) {
			_ = s.client.Close()
		}
		if err := write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": rpcError{Code: -32000, Message: "harness turn failed"}}, true); err != nil {
			panic(http.ErrAbortHandler)
		}
		return
	}
	if err := write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": completed}, true); err != nil {
		_ = s.client.Close()
		panic(http.ErrAbortHandler)
	}
}
