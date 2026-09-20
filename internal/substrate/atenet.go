package substrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ActorDNSSuffix is the CoreDNS stub domain atenet programs. Every
	// actor Host is <name>.<atespace>.actors.resources.substrate.ate.dev
	// and resolves to atenet-router, which ResumeActor-routes to the worker.
	ActorDNSSuffix = "actors.resources.substrate.ate.dev"
	// DefaultAtenetEndpoint is the in-cluster atenet-router Service for
	// Helm release name substrate in namespace ate-system (HTTP :80).
	DefaultAtenetEndpoint = "atenet-router.ate-system.svc:80"
	// DefaultAtenetPath is the actor HTTP path. ACP agents serve JSON-RPC
	// at the root; the Host header selects the actor.
	DefaultAtenetPath    = "/"
	defaultAtenetTimeout = 2 * time.Minute
	maxGenerateBodyBytes = 1 << 20
)

// ErrGenerateTransient marks atenet failures the controller should retry
// without failing the AgentRun (parking 503, 429, 504, transport blips).
var ErrGenerateTransient = errors.New("substrate atenet generate transient")

// AtenetConfig is the HTTP client for generate-on-actor. Endpoint is
// host:port of atenet-router (or an httptest server). Token bytes are
// attached as Authorization and must never appear in errors, logs, or status.
type AtenetConfig struct {
	Endpoint  string
	Path      string
	TokenFile string
	Token     string
	Timeout   time.Duration
}

// Validate rejects generate configs that can never reach atenet.
func (c AtenetConfig) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" {
		return fmt.Errorf("substrate atenet endpoint is required (see %s)", GateAtenetEndpointEnvVar)
	}
	return nil
}

// ActorAuthority is the atenet :authority / Host for one actor.
func ActorAuthority(actorName, atespace string) string {
	name := strings.TrimSpace(actorName)
	space := strings.TrimSpace(atespace)
	if name == "" || space == "" {
		return ""
	}
	return name + "." + space + "." + ActorDNSSuffix
}

// NewAtenetClient builds a TurnGenerator that POSTs ACP session/prompt
// through atenet-router. Tests pass an httptest client via NewAtenetClientHTTP.
func NewAtenetClient(cfg AtenetConfig) (*AtenetClient, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultAtenetTimeout
	}
	return NewAtenetClientHTTP(cfg, &http.Client{Timeout: timeout})
}

// NewAtenetClientHTTP injects the HTTP client (httptest).
func NewAtenetClientHTTP(cfg AtenetConfig, client *http.Client) (*AtenetClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("substrate atenet HTTP client is required")
	}
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = DefaultAtenetPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	cfg.Path = path
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	return &AtenetClient{cfg: cfg, http: client}, nil
}

// AtenetClient implements TurnGenerator against atenet HTTP/ACP.
type AtenetClient struct {
	cfg  AtenetConfig
	http *http.Client
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type acpPromptParams struct {
	SessionID string            `json:"sessionId"`
	Prompt    []acpContentBlock `json:"prompt"`
}

type acpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Generate POSTs ACP session/prompt with Host set to the actor DNS name so
// atenet ExtProc can ResumeActor and forward. Token material is never
// copied into the returned error.
func (c *AtenetClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if c == nil || c.http == nil {
		return GenerateResult{}, fmt.Errorf("substrate atenet client is not configured")
	}
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return GenerateResult{}, fmt.Errorf("generate-on-actor prompt is empty")
	}
	authority := ActorAuthority(req.ActorName, req.Atespace)
	if authority == "" {
		return GenerateResult{}, fmt.Errorf("generate-on-actor actor name and atespace are required")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(req.ActorName)
	}
	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "session/prompt",
		Params: acpPromptParams{
			SessionID: sessionID,
			Prompt:    []acpContentBlock{{Type: "text", Text: prompt}},
		},
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("encode ACP session/prompt: %w", err)
	}
	target, err := c.requestURL()
	if err != nil {
		return GenerateResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("atenet request: %w", err)
	}
	httpReq.Host = authority
	httpReq.Header.Set("Host", authority)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, application/x-ndjson, text/plain")
	if err := c.attachToken(httpReq); err != nil {
		return GenerateResult{}, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("%w: atenet transport: %s", ErrGenerateTransient, sanitizeGenerateErr(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGenerateBodyBytes+1))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("%w: read atenet body: %s", ErrGenerateTransient, sanitizeGenerateErr(err))
	}
	if len(raw) > maxGenerateBodyBytes {
		return GenerateResult{}, fmt.Errorf("atenet reply exceeded %d bytes", maxGenerateBodyBytes)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusGatewayTimeout {
		return GenerateResult{}, fmt.Errorf("%w: atenet HTTP %d", ErrGenerateTransient, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GenerateResult{}, fmt.Errorf("atenet HTTP %d for actor %s", resp.StatusCode, authority)
	}
	result, err := parseGenerateBody(raw)
	if err != nil {
		return GenerateResult{}, err
	}
	if strings.TrimSpace(result.Text) == "" {
		return GenerateResult{}, fmt.Errorf("atenet actor %s returned an empty reply", authority)
	}
	return result, nil
}

func (c *AtenetClient) requestURL() (string, error) {
	endpoint := strings.TrimSpace(c.cfg.Endpoint)
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" {
			return "", fmt.Errorf("substrate atenet endpoint is invalid")
		}
		parsed.Path = c.cfg.Path
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.User = nil
		return parsed.String(), nil
	}
	return "http://" + endpoint + c.cfg.Path, nil
}

func (c *AtenetClient) attachToken(req *http.Request) error {
	token := strings.TrimSpace(c.cfg.Token)
	if path := strings.TrimSpace(c.cfg.TokenFile); path != "" {
		loaded, err := loadTokenFromFile(path)
		if err != nil {
			return err
		}
		token = loaded
	}
	if token == "" {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func parseGenerateBody(raw []byte) (GenerateResult, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return GenerateResult{}, fmt.Errorf("atenet reply is empty")
	}
	if msg := jsonRPCErrorMessage(trimmed); msg != "" {
		return GenerateResult{}, fmt.Errorf("atenet ACP error")
	}
	if text, ok := parseACPStream(trimmed); ok {
		return text, nil
	}
	var envelope struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &envelope) == nil && strings.TrimSpace(envelope.Text) != "" {
		return GenerateResult{Text: strings.TrimSpace(envelope.Text), StopReason: "end_turn"}, nil
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return GenerateResult{Text: string(trimmed), StopReason: "end_turn"}, nil
	}
	return GenerateResult{}, fmt.Errorf("atenet reply is not ACP session/prompt output")
}

func parseACPStream(raw []byte) (GenerateResult, bool) {
	var text strings.Builder
	stop := ""
	matched := false
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]json.RawMessage
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if jsonRPCErrorMessage(line) != "" {
			return GenerateResult{}, false
		}
		if methodRaw, ok := msg["method"]; ok {
			var method string
			if json.Unmarshal(methodRaw, &method) == nil && method == "session/update" {
				matched = true
				text.WriteString(extractACPUpdateText(msg["params"]))
			}
		}
		if resultRaw, ok := msg["result"]; ok && len(bytes.TrimSpace(resultRaw)) > 0 && string(resultRaw) != "null" {
			matched = true
			var result struct {
				StopReason string `json:"stopReason"`
				Text       string `json:"text"`
			}
			if json.Unmarshal(resultRaw, &result) == nil {
				if strings.TrimSpace(result.Text) != "" && text.Len() == 0 {
					text.WriteString(strings.TrimSpace(result.Text))
				}
				stop = strings.TrimSpace(result.StopReason)
			}
			text.WriteString(extractACPResultContent(resultRaw))
		}
	}
	out := strings.TrimSpace(text.String())
	if !matched {
		return GenerateResult{}, false
	}
	if stop == "" {
		stop = "end_turn"
	}
	return GenerateResult{Text: out, StopReason: stop}, true
}

func extractACPUpdateText(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var envelope struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &envelope) != nil {
		return extractACPContentText(params)
	}
	if len(envelope.Update) == 0 {
		return extractACPContentText(params)
	}
	var update struct {
		SessionUpdate string          `json:"sessionUpdate"`
		Content       json.RawMessage `json:"content"`
	}
	if json.Unmarshal(envelope.Update, &update) != nil {
		return extractACPContentText(envelope.Update)
	}
	switch strings.TrimSpace(update.SessionUpdate) {
	case "agent_message_chunk", "agent_message":
		return extractACPContentText(update.Content)
	default:
		return ""
	}
}

func extractACPResultContent(result json.RawMessage) string {
	var envelope struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(result, &envelope) != nil {
		return ""
	}
	return extractACPContentText(envelope.Content)
}

func extractACPContentText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var block acpContentBlock
	if json.Unmarshal(raw, &block) == nil && strings.EqualFold(block.Type, "text") {
		return block.Text
	}
	var blocks []acpContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var out strings.Builder
		for _, item := range blocks {
			if strings.EqualFold(item.Type, "text") {
				out.WriteString(item.Text)
			}
		}
		return out.String()
	}
	return ""
}

func jsonRPCErrorMessage(raw []byte) string {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &msg) != nil || msg.Error == nil {
			continue
		}
		if msg.Error.Code != 0 || strings.TrimSpace(msg.Error.Message) != "" {
			return "jsonrpc"
		}
	}
	return ""
}

func sanitizeGenerateErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}
