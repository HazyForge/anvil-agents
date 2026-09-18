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

// liveAPIVersion pins the spike to one lifecycle path prefix. Substrate is
// early and its APIs will churn; keeping the versioned prefix in a single
// constant lets a future transport swap re-point it without touching callers.
const liveAPIVersion = "v1"

// liveBodyLimit caps lifecycle responses so a misbehaving gateway cannot force
// unbounded reads out of the controller.
const liveBodyLimit = 1 << 20

// LiveConfig configures the HTTP lifecycle transport. Endpoint is the gateway
// origin (scheme + host, for example http://substrate-gateway.substrate:8080
// on the Kind spike cluster). AuthToken is optional and, when set, is only
// ever sent as an Authorization header.
type LiveConfig struct {
	Endpoint   string
	AuthToken  string
	HTTPClient *http.Client
	Timeout    time.Duration
}

// liveActorPayload is the JSON body exchanged with the gateway for every
// lifecycle op. Field names stay provider-neutral on purpose: the Anvil side
// binds only to stable lifecycle concepts, never to vendored Substrate types.
type liveActorPayload struct {
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name,omitempty"`
	ID          string            `json:"id,omitempty"`
	State       string            `json:"state,omitempty"`
	Resumes     int               `json:"resumes,omitempty"`
	ActorClass  string            `json:"actorClass,omitempty"`
	Pool        string            `json:"pool,omitempty"`
	HarnessKind string            `json:"harnessKind,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// LiveClient is the opt-in live substrate.Client. It speaks the stable actor
// lifecycle (create-or-reuse, resume, suspend, pause, describe) over HTTPS to
// a gateway without vendoring Substrate API types.
type LiveClient struct {
	endpoint   string
	authToken  string
	httpClient *http.Client
	timeout    time.Duration
}

// NewLiveClient validates the endpoint and returns a live Client. The gate
// itself lives in GateConfig; this constructor only guards transport shape.
func NewLiveClient(cfg LiveConfig) (*LiveClient, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("substrate endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("substrate endpoint must be an absolute URL with a host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("substrate endpoint scheme must be http or https")
	}
	if strings.TrimSpace(parsed.User.String()) != "" || strings.TrimSpace(parsed.RawQuery) != "" || strings.TrimSpace(parsed.Fragment) != "" {
		return nil, errors.New("substrate endpoint must not carry userinfo, query, or fragment")
	}
	endpoint = strings.TrimRight(endpoint, "/")
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &LiveClient{endpoint: endpoint, authToken: cfg.AuthToken, httpClient: httpClient, timeout: timeout}, nil
}

// NewLiveClientFromEnv builds the live Client from GateConfigFromEnv. It
// returns (nil, false, nil) when the gate is off so callers keep the safe
// hold without branching on env parsing themselves.
func NewLiveClientFromEnv() (*LiveClient, bool, error) {
	gate := GateConfigFromEnv()
	if !gate.LiveEnabled() {
		return nil, false, nil
	}
	client, err := NewLiveClient(LiveConfig{Endpoint: gate.Endpoint, AuthToken: gate.Token})
	if err != nil {
		return nil, false, err
	}
	return client, true, nil
}

// Endpoint returns the configured gateway origin without credentials. The auth
// token is never exposed through this or any other accessor.
func (c *LiveClient) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

func (c *LiveClient) actorPath(namespace, name string, action string) (string, error) {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return "", errors.New("substrate actor namespace and name are required")
	}
	path := "/" + liveAPIVersion + "/actors/" + url.PathEscape(namespace) + "/" + url.PathEscape(name)
	if strings.TrimSpace(action) != "" {
		path += "/" + url.PathEscape(strings.TrimSpace(action))
	}
	return c.endpoint + path, nil
}

func (c *LiveClient) do(ctx context.Context, method, requestURL, namespace, name string, body *liveActorPayload) (liveActorPayload, int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return liveActorPayload{}, 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, method, requestURL, reader)
	if err != nil {
		return liveActorPayload{}, 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(c.authToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.authToken))
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return liveActorPayload{}, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, liveBodyLimit+1))
	if err != nil {
		return liveActorPayload{}, response.StatusCode, err
	}
	if len(raw) > liveBodyLimit {
		return liveActorPayload{}, response.StatusCode, errors.New("substrate response exceeds the size limit")
	}
	if response.StatusCode == http.StatusNotFound {
		return liveActorPayload{}, response.StatusCode, fmt.Errorf("%w: %s", ErrActorNotFound, ActorKey(namespace, name))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return liveActorPayload{}, response.StatusCode, fmt.Errorf("substrate lifecycle call failed with status %d", response.StatusCode)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return liveActorPayload{}, response.StatusCode, nil
	}
	var payload liveActorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return liveActorPayload{}, response.StatusCode, fmt.Errorf("decode substrate lifecycle response: %w", err)
	}
	return payload, response.StatusCode, nil
}

func payloadToHandle(payload liveActorPayload, namespace, name string) ActorHandle {
	resolvedNamespace := strings.TrimSpace(payload.Namespace)
	if resolvedNamespace == "" {
		resolvedNamespace = strings.TrimSpace(namespace)
	}
	resolvedName := strings.TrimSpace(payload.Name)
	if resolvedName == "" {
		resolvedName = strings.TrimSpace(name)
	}
	return ActorHandle{
		Namespace: resolvedNamespace,
		Name:      resolvedName,
		ID:        strings.TrimSpace(payload.ID),
		State:     ActorState(strings.TrimSpace(payload.State)),
		Resumes:   payload.Resumes,
	}
}

func specToPayload(spec ActorSpec) *liveActorPayload {
	return &liveActorPayload{
		Namespace:   strings.TrimSpace(spec.Namespace),
		Name:        strings.TrimSpace(spec.Name),
		ActorClass:  strings.TrimSpace(spec.ActorClass),
		Pool:        strings.TrimSpace(spec.Pool),
		HarnessKind: strings.TrimSpace(spec.HarnessKind),
		Labels:      spec.Labels,
	}
}

// CreateActor creates the actor or reuses the existing warm actor. The gateway
// upserts on PUT so retries of the same turn stay idempotent.
func (c *LiveClient) CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if err := ValidateSpec(spec); err != nil {
		return ActorHandle{}, err
	}
	requestURL, err := c.actorPath(spec.Namespace, spec.Name, "")
	if err != nil {
		return ActorHandle{}, err
	}
	payload, _, err := c.do(ctx, http.MethodPut, requestURL, spec.Namespace, spec.Name, specToPayload(spec))
	if err != nil {
		return ActorHandle{}, err
	}
	handle := payloadToHandle(payload, spec.Namespace, spec.Name)
	if handle.ID == "" {
		handle.ID = fakeID(spec.Namespace, spec.Name)
	}
	if strings.TrimSpace(string(handle.State)) == "" {
		handle.State = ActorStateActive
	}
	return handle, nil
}

// ResumeActor resumes a suspended actor before a turn.
func (c *LiveClient) ResumeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	requestURL, err := c.actorPath(namespace, name, "resume")
	if err != nil {
		return ActorHandle{}, err
	}
	payload, _, err := c.do(ctx, http.MethodPost, requestURL, namespace, name, nil)
	if err != nil {
		if errors.Is(err, ErrActorNotFound) {
			return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, ActorKey(namespace, name))
		}
		return ActorHandle{}, err
	}
	handle := payloadToHandle(payload, namespace, name)
	if strings.TrimSpace(string(handle.State)) == "" {
		handle.State = ActorStateActive
	}
	return handle, nil
}

// SuspendActor persists actor state and releases the worker.
func (c *LiveClient) SuspendActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	requestURL, err := c.actorPath(namespace, name, "suspend")
	if err != nil {
		return ActorHandle{}, err
	}
	payload, _, err := c.do(ctx, http.MethodPost, requestURL, namespace, name, nil)
	if err != nil {
		if errors.Is(err, ErrActorNotFound) {
			return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, ActorKey(namespace, name))
		}
		return ActorHandle{}, err
	}
	handle := payloadToHandle(payload, namespace, name)
	if strings.TrimSpace(string(handle.State)) == "" {
		handle.State = ActorStateSuspended
	}
	return handle, nil
}

// PauseActor keeps the actor resident but unscheduled.
func (c *LiveClient) PauseActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	requestURL, err := c.actorPath(namespace, name, "pause")
	if err != nil {
		return ActorHandle{}, err
	}
	payload, _, err := c.do(ctx, http.MethodPost, requestURL, namespace, name, nil)
	if err != nil {
		if errors.Is(err, ErrActorNotFound) {
			return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, ActorKey(namespace, name))
		}
		return ActorHandle{}, err
	}
	handle := payloadToHandle(payload, namespace, name)
	if strings.TrimSpace(string(handle.State)) == "" {
		handle.State = ActorStatePaused
	}
	return handle, nil
}

// DescribeActor returns the current handle without changing lifecycle state.
func (c *LiveClient) DescribeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	requestURL, err := c.actorPath(namespace, name, "")
	if err != nil {
		return ActorHandle{}, err
	}
	payload, _, err := c.do(ctx, http.MethodGet, requestURL, namespace, name, nil)
	if err != nil {
		if errors.Is(err, ErrActorNotFound) {
			return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, ActorKey(namespace, name))
		}
		return ActorHandle{}, err
	}
	return payloadToHandle(payload, namespace, name), nil
}
