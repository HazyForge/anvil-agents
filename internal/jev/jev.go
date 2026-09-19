// Package jev is a small internal client for TypeSafe's Jev System One
// model (POST https://api.typesafe.ai/v1/systemone), used for runtime
// decision making only — never for chat generation.
//
// Austin's 2026-09-18 direction: use Jev when something needs decision
// making at runtime (e.g. decide user intent and route based on intent).
// Standing in-process harnesses plus WebSocket delivery remain the primary
// interactive chat path, Jobs stay the default for scouts/batch, and
// Substrate stays optional. Jev decides; the harness chat model (Codex,
// OpenCode, ...) still generates replies.
//
// API shape source of truth: https://docs.typesafe.ai/api.md (fetched live
// for this spike) plus the TypeSafe agent skill
// (https://github.com/typesafe-ai/skills, skills/typesafe-ai/SKILL.md).
// Field names below mirror the API reference exactly: requests carry
// state + model + questions, questions are noul / choice / score with
// instructions + criteria, and answers come back under the same question
// IDs with calibrated probabilities/confidence. Nothing here invents API
// fields; live responses are never fabricated — tests drive FakeBackend
// (no network in CI) and the live path requires TYPESAFE_API_KEY.
//
// Boundaries that do not move in this spike:
//
//   - AgentRun stays append-only; new execution intent creates a new AgentRun.
//   - create-agent stays Wrapper/manager-only. The intent router in
//     intent.go may classify a message as create_agent_request, but that
//     routes to requesting a manager — peers never create agents directly.
//   - No Secret access: the API key comes from the TYPESAFE_API_KEY
//     environment variable only and is never logged, persisted, or returned.
//
// See docs/jev-intent-routing.md for when to use Jev vs the harness chat
// model and where the intent router plugs into the standing turn path.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the TypeSafe System One evaluation endpoint.
const DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"

// DefaultModel is the stable Jev alias. Pin a versioned ID (e.g.
// "jev-1.13.0") instead when thresholds are tuned against one version.
const DefaultModel = "jev-latest"

// EnvAPIKey is the only credential source. The key is never hardcoded,
// never logged, and never persisted outside the process environment.
const EnvAPIKey = "TYPESAFE_API_KEY"

// Question types. They mirror the API reference exactly.
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// Limits from the API docs: state plus the longest single question must fit
// ~32k tokens, all questions share ~64k tokens, Choice supports up to 255
// options, Score needs 2-10 levels.
const (
	MaxChoiceOptions = 255
	MinScoreLevels   = 2
	MaxScoreLevels   = 10
)

// Request is the body of POST /v1/systemone. State is the content to
// evaluate (string, object, or array); Questions maps caller-chosen IDs to
// typed questions; answers come back under the same IDs.
type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

// Question is one typed judgment. Type selects the primitive; Instructions
// carries the full question (IDs are for code and are not sent to the
// model, so instructions must be self-contained); Criteria defines the
// possible answers and is shaped per type:
//
//   - noul: omitted, or {"true": "...", "false": "..."} clarifications.
//   - choice: required map of option to rubric description (null when an
//     option needs no extra detail).
//   - score: required ordered array of 2-10 level descriptions, low to high.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// NoulCriteria clarifies what yes/no mean for a noul question.
type NoulCriteria struct {
	True  string `json:"true,omitempty"`
	False string `json:"false,omitempty"`
}

// Desc marks a Choice option description. A nil description marshals as
// null ("no extra detail", the documented escape-bucket convention).
func Desc(s string) *string { return &s }

// NoulQuestion builds a yes/no question. Empty yes/no clarifications omit
// criteria entirely.
func NoulQuestion(instructions, yes, no string) Question {
	q := Question{Type: TypeNoul, Instructions: instructions}
	if strings.TrimSpace(yes) != "" || strings.TrimSpace(no) != "" {
		q.Criteria = NoulCriteria{True: yes, False: no}
	}
	return q
}

// ChoiceQuestion builds a pick-one question over option->description.
// A nil description means "no extra detail" (used for other/escape buckets).
func ChoiceQuestion(instructions string, criteria map[string]*string) Question {
	return Question{Type: TypeChoice, Instructions: instructions, Criteria: criteria}
}

// ScoreQuestion builds a position-on-a-scale question. Levels are ordered
// low to high; each level's index is its level number starting at 0.
func ScoreQuestion(instructions string, levels []string) Question {
	return Question{Type: TypeScore, Instructions: instructions, Criteria: levels}
}

// Validate checks the question against the API shape constraints.
func (q Question) Validate(id string) error {
	label := strings.TrimSpace(id)
	if label == "" {
		label = "<unnamed>"
	}
	if strings.TrimSpace(q.Instructions) == "" {
		return fmt.Errorf("jev question %q: instructions are required", label)
	}
	switch q.Type {
	case TypeNoul:
		return nil
	case TypeChoice:
		criteria, ok := q.Criteria.(map[string]*string)
		if !ok {
			// Accept the map[string]string form too so callers without
			// nullable descriptions still validate.
			if alt, ok := q.Criteria.(map[string]string); ok {
				return validateChoiceOptions(label, len(alt))
			}
			return fmt.Errorf("jev question %q: choice criteria must be a map of option to description", label)
		}
		return validateChoiceOptions(label, len(criteria))
	case TypeScore:
		criteria, ok := q.Criteria.([]string)
		if !ok {
			return fmt.Errorf("jev question %q: score criteria must be an ordered array of level descriptions", label)
		}
		if len(criteria) < MinScoreLevels || len(criteria) > MaxScoreLevels {
			return fmt.Errorf("jev question %q: score criteria must hold %d-%d levels, got %d", label, MinScoreLevels, MaxScoreLevels, len(criteria))
		}
		for i, level := range criteria {
			if strings.TrimSpace(level) == "" {
				return fmt.Errorf("jev question %q: score level %d must describe a concrete situation", label, i)
			}
		}
		return nil
	default:
		return fmt.Errorf("jev question %q: unknown type %q (want noul, choice, or score)", label, q.Type)
	}
}

func validateChoiceOptions(label string, n int) error {
	if n < 1 {
		return fmt.Errorf("jev question %q: choice criteria must contain at least one option", label)
	}
	if n > MaxChoiceOptions {
		return fmt.Errorf("jev question %q: choice criteria must hold at most %d options, got %d", label, MaxChoiceOptions, n)
	}
	return nil
}

// Validate checks the request against the API shape constraints before any
// network call: state must be a string, object, or array; model defaults
// are applied by the client; at least one question is required.
func (req Request) Validate() error {
	if !isState(req.State) {
		return fmt.Errorf("jev request: state must be a string, object, or array")
	}
	if len(req.Questions) == 0 {
		return fmt.Errorf("jev request: at least one question is required")
	}
	for id, q := range req.Questions {
		if err := q.Validate(id); err != nil {
			return err
		}
	}
	return nil
}

// isState reports whether value marshals as a JSON string, object, or
// array — the only state shapes the API accepts.
func isState(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return true
	case map[string]any:
		return true
	case []any:
		return true
	case json.RawMessage:
		return isStateRaw(v)
	default:
		// Re-marshal through encoding/json so named map/slice types and
		// structs (objects) validate by their wire shape, not their Go type.
		raw, err := json.Marshal(v)
		if err != nil {
			return false
		}
		return isStateRaw(raw)
	}
}

func isStateRaw(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '"', '{', '[':
		return json.Valid(trimmed)
	default:
		return false
	}
}

// Usage carries the token counts from a System One response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is a decoded System One response. Answers stay raw so callers
// decode each under its question ID with the matching accessor; the wire
// shape per answer type is pinned by the API reference.
type Response struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   Usage                      `json:"usage"`
}

// NoulAnswer is the yes/no answer: probability the answer is yes.
type NoulAnswer struct {
	Type string  `json:"type"`
	Noul float64 `json:"noul"`
}

// ChoiceAnswer picks the highest-probability option and reports the full
// distribution plus a concentration-derived confidence.
type ChoiceAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// ScoreAnswer is the probability-weighted position across ordered levels.
type ScoreAnswer struct {
	Type          string             `json:"type"`
	Score         float64            `json:"score"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func decodeAnswer[T any](resp Response, id, wantType string) (T, error) {
	var zero T
	raw, ok := resp.Answers[id]
	if !ok {
		return zero, fmt.Errorf("jev response: missing answer %q", id)
	}
	var typed struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return zero, fmt.Errorf("jev response: answer %q is not valid JSON: %w", id, err)
	}
	if typed.Type != wantType {
		return zero, fmt.Errorf("jev response: answer %q has type %q, want %q", id, typed.Type, wantType)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("jev response: answer %q does not match %s shape: %w", id, wantType, err)
	}
	return out, nil
}

// Noul decodes a noul answer under id.
func (resp Response) Noul(id string) (NoulAnswer, error) {
	return decodeAnswer[NoulAnswer](resp, id, TypeNoul)
}

// Choice decodes a choice answer under id.
func (resp Response) Choice(id string) (ChoiceAnswer, error) {
	return decodeAnswer[ChoiceAnswer](resp, id, TypeChoice)
}

// Score decodes a score answer under id.
func (resp Response) Score(id string) (ScoreAnswer, error) {
	return decodeAnswer[ScoreAnswer](resp, id, TypeScore)
}

// Backend evaluates System One requests. HTTPClient is the live
// implementation; FuncBackend/FakeBackend serve tests and the probe's
// --fake mode with no network.
type Backend interface {
	Evaluate(ctx context.Context, req Request) (Response, error)
}

// APIError is a non-2xx response from the System One endpoint. Status
// follows the API reference: 401 bad key, 422 validation (body names the
// field), 429/529 retryable overload.
type APIError struct {
	Status  int
	Message string
}

func (err *APIError) Error() string {
	return "jev systemone: HTTP " + strconv.Itoa(err.Status) + strings.TrimSuffix(" "+strings.TrimSpace(err.Message), " ")
}

// Retryable reports whether the request may be retried with backoff
// (429 rate limit, 529 overload per the API reference).
func (err *APIError) Retryable() bool {
	return err != nil && (err.Status == http.StatusTooManyRequests || err.Status == 529)
}

// HTTPClient is the live Backend. It reads no files and no process state
// beyond its fields; construct it with NewHTTPClient or ClientFromEnv.
type HTTPClient struct {
	Endpoint   string
	APIKey     string
	HTTP       *http.Client
	MaxRetries int
	RetryWait  time.Duration
}

// NewHTTPClient builds a live client. An empty endpoint selects
// DefaultEndpoint; an empty key fails fast on Evaluate (never silently
// unauthenticated).
func NewHTTPClient(apiKey string) *HTTPClient {
	return &HTTPClient{
		Endpoint:   DefaultEndpoint,
		APIKey:     apiKey,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 3,
		RetryWait:  200 * time.Millisecond,
	}
}

// ClientFromEnv builds a live client from TYPESAFE_API_KEY. It returns
// ok=false (not an error) when the key is unset so callers can fall back
// to Fake/no-Jev behavior with guidance instead of failing.
func ClientFromEnv() (client *HTTPClient, ok bool) {
	key := strings.TrimSpace(envAPIKey())
	if key == "" {
		return nil, false
	}
	return NewHTTPClient(key), true
}

func envAPIKey() string {
	return strings.TrimSpace(os.Getenv(EnvAPIKey))
}

// Evaluate posts one request and decodes the response. It validates
// locally first, defaults an empty model to jev-latest, and retries
// 429/529 with exponential backoff.
func (client *HTTPClient) Evaluate(ctx context.Context, req Request) (Response, error) {
	if client == nil || client.HTTP == nil {
		return Response{}, fmt.Errorf("jev client is not configured")
	}
	if strings.TrimSpace(client.APIKey) == "" {
		return Response{}, fmt.Errorf("jev client: %s is not set", EnvAPIKey)
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = DefaultModel
	}
	if err := req.Validate(); err != nil {
		return Response{}, err
	}
	endpoint := strings.TrimSpace(client.Endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("jev request: cannot encode: %w", err)
	}
	maxRetries := client.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	wait := client.RetryWait
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		resp, err := client.post(ctx, endpoint, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		apiErr, ok := err.(*APIError)
		if !ok || !apiErr.Retryable() || attempt == maxRetries {
			return Response{}, err
		}
	}
	return Response{}, lastErr
}

func (client *HTTPClient) post(ctx context.Context, endpoint string, body []byte) (Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("jev request: cannot build: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+client.APIKey)
	httpResp, err := client.HTTP.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("jev request: %w", err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("jev response: cannot read: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		return Response{}, &APIError{Status: httpResp.StatusCode, Message: string(bytes.TrimSpace(raw))}
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("jev response: cannot decode: %w", err)
	}
	if resp.Answers == nil {
		resp.Answers = map[string]json.RawMessage{}
	}
	return resp, nil
}

// FuncBackend adapts a function to a Backend. Tests use it to return
// canned answers with no network.
type FuncBackend func(ctx context.Context, req Request) (Response, error)

// Evaluate implements Backend.
func (fn FuncBackend) Evaluate(ctx context.Context, req Request) (Response, error) {
	if fn == nil {
		return Response{}, fmt.Errorf("jev fake backend is not configured")
	}
	return fn(ctx, req)
}

// FakeBackend returns canned answers keyed by question ID. A nil answer map
// entry leaves the question unanswered so tests can pin missing-answer
// handling. It performs no I/O.
type FakeBackend struct {
	Model   string
	Answers map[string]json.RawMessage
	Err     error
}

// Evaluate implements Backend.
func (fake *FakeBackend) Evaluate(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if fake == nil || fake.Err != nil {
		if fake == nil {
			return Response{}, fmt.Errorf("jev fake backend is not configured")
		}
		return Response{}, fake.Err
	}
	if err := req.Validate(); err != nil {
		return Response{}, err
	}
	model := strings.TrimSpace(fake.Model)
	if model == "" {
		model = DefaultModel
	}
	answers := map[string]json.RawMessage{}
	for id, raw := range fake.Answers {
		answers[id] = raw
	}
	return Response{Model: model, Answers: answers}, nil
}

// MustAnswer marshals value to a canned fake answer, failing the test on
// encode errors. It keeps fake fixtures honest: fixtures are built from
// the same answer structs the accessors decode.
func MustAnswer[T any](t interface {
	Helper()
	Fatalf(string, ...any)
}, value T,
) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("jev fake answer: cannot encode: %v", err)
	}
	return raw
}
