package substrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// actorDNSDomainSuffix is the upstream Substrate actor DNS suffix served by
// atenet DNS/router. A router request addresses an actor through the Host
// header as <actor>.<atespace>.actors.resources.substrate.ate.dev; the router
// parses that authority, calls ateapi ResumeActor(ObjectRef{atespace, name})
// on every request, and forwards to the current worker pod. See
// https://learn.agentsubstrate.dev/components/atenet and
// https://learn.agentsubstrate.dev/flows/request-path.
const actorDNSDomainSuffix = "actors.resources.substrate.ate.dev"

// DefaultAtespace is the Substrate tenancy the live client addresses when no
// atespace is configured. Atespaces are Substrate-native records (not
// Kubernetes namespaces) and must be pre-created on the cluster, for example
// with `kubectl ate create atespace agents`.
const DefaultAtespace = "agents"

// maxRouterLabelLen bounds a single DNS label in the router Host authority.
// ActorNameForThread can emit up to 253 chars, so longer names are flattened
// with a content hash to stay routable.
const maxRouterLabelLen = 63

// liveBodyLimit caps lifecycle responses so a misbehaving endpoint cannot
// force unbounded reads out of the controller.
const liveBodyLimit = 1 << 20

// ShimTemplateLabel is the optional ActorSpec label carrying a per-actor
// `namespace/name` ActorTemplate override for CreateActor through the control
// shim. When absent, LiveConfig.ActorTemplate (or the shim server default)
// applies.
const ShimTemplateLabel = "control.anvil.hazyforge.io/substrate-template"

// ErrLiveControlPlaneRequired is returned by Create/Suspend/Pause when no
// control-plane shim is configured. Those operations are ateapi gRPC-only
// upstream (CreateActor/SuspendActor/PauseActor); the atenet-router HTTP data
// plane can only resume-on-request and probe. Deploy the spike shim
// (cmd/substrate-ate-shim, a thin kubectl-ate bridge) or pre-provision actors
// with kubectl ate, then point ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT at it.
var ErrLiveControlPlaneRequired = errors.New("substrate control plane is required: configure ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT (cmd/substrate-ate-shim) for create/suspend/pause")

// LiveConfig configures the live transport. Endpoint is the atenet-router
// origin (scheme + host), for example http://localhost:8000 via
// `kubectl -n ate-system port-forward svc/atenet-router 8000:8080`, or the
// in-cluster Service origin. Atespace selects the Substrate tenancy; empty
// means DefaultAtespace. ActorTemplate is the `namespace/name` ActorTemplate
// used for CreateActor through the shim when set. ShimEndpoint is the
// optional cmd/substrate-ate-shim origin backing create/suspend/pause and
// full status reads. AuthToken is optional and, when set, is only ever sent
// as an Authorization header.
type LiveConfig struct {
	Endpoint      string
	Atespace      string
	ActorTemplate string
	ShimEndpoint  string
	AuthToken     string
	HTTPClient    *http.Client
	Timeout       time.Duration
}

// LiveClient is the opt-in live substrate.Client. Resume and describe speak
// the real atenet-router HTTP data plane (Host-addressed actor authority,
// resume-on-request per the upstream request path); create, suspend, and
// pause go through the documented kubectl-ate control shim because those
// operations are ateapi gRPC-only upstream. No Substrate API types are
// vendored: the client only tracks the (atespace, name) identity and the
// stable Active/Suspended/Paused states.
type LiveClient struct {
	endpoint      string
	atespace      string
	actorTemplate string
	shimEndpoint  string
	authToken     string
	httpClient    *http.Client
	timeout       time.Duration
}

// NewLiveClient validates the endpoints and returns a live Client. The gate
// itself lives in GateConfig; this constructor only guards transport shape.
func NewLiveClient(cfg LiveConfig) (*LiveClient, error) {
	endpoint, err := normalizeOrigin(cfg.Endpoint, "substrate endpoint is required")
	if err != nil {
		return nil, err
	}
	atespace := strings.TrimSpace(cfg.Atespace)
	if atespace == "" {
		atespace = DefaultAtespace
	}
	if err := validateDNSLabel(atespace, "substrate atespace"); err != nil {
		return nil, err
	}
	template := strings.TrimSpace(cfg.ActorTemplate)
	if template != "" {
		if err := validateActorTemplate(template); err != nil {
			return nil, err
		}
	}
	shim := strings.TrimSpace(cfg.ShimEndpoint)
	if shim != "" {
		shim, err = normalizeOrigin(shim, "substrate shim endpoint is required")
		if err != nil {
			return nil, err
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &LiveClient{
		endpoint:      endpoint,
		atespace:      atespace,
		actorTemplate: template,
		shimEndpoint:  shim,
		authToken:     cfg.AuthToken,
		httpClient:    httpClient,
		timeout:       timeout,
	}, nil
}

func normalizeOrigin(raw, emptyErr string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return "", errors.New(emptyErr)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("substrate endpoint must be an absolute URL with a host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("substrate endpoint scheme must be http or https")
	}
	if strings.TrimSpace(parsed.User.String()) != "" || strings.TrimSpace(parsed.RawQuery) != "" || strings.TrimSpace(parsed.Fragment) != "" {
		return "", errors.New("substrate endpoint must not carry userinfo, query, or fragment")
	}
	return strings.TrimRight(endpoint, "/"), nil
}

func validateDNSLabel(label, what string) error {
	if label == "" || len(label) > maxRouterLabelLen {
		return fmt.Errorf("%s %q must be 1-%d DNS-1123 characters", what, label, maxRouterLabelLen)
	}
	for i, r := range label {
		lower := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
		if !lower {
			return fmt.Errorf("%s %q must be lowercase alphanumeric or '-'", what, label)
		}
		if i == 0 && r == '-' {
			return fmt.Errorf("%s %q must start with an alphanumeric", what, label)
		}
		if i == len(label)-1 && r == '-' {
			return fmt.Errorf("%s %q must end with an alphanumeric", what, label)
		}
	}
	return nil
}

func validateActorTemplate(template string) error {
	parts := strings.Split(template, "/")
	if len(parts) != 2 {
		return fmt.Errorf("substrate actor template %q must look like namespace/name", template)
	}
	if err := validateDNSLabel(parts[0], "substrate actor template namespace"); err != nil {
		return err
	}
	if err := validateDNSSubdomain(parts[1], "substrate actor template name"); err != nil {
		return err
	}
	return nil
}

func validateDNSSubdomain(value, what string) error {
	if value == "" || len(value) > 253 {
		return fmt.Errorf("%s %q must be 1-253 DNS-subdomain characters", what, value)
	}
	for _, segment := range strings.Split(value, ".") {
		if err := validateDNSLabel(segment, what); err != nil {
			return err
		}
	}
	return nil
}

// liveActorLabel flattens an Anvil actor name into the single DNS label the
// atenet router expects in the Host authority. The same label addresses the
// shim control calls so both planes converge on one upstream actor identity.
func liveActorLabel(name string) (string, error) {
	flattened := strings.ToLower(strings.TrimSpace(name))
	flattened = strings.ReplaceAll(flattened, ".", "-")
	var out strings.Builder
	for _, r := range flattened {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	flattened = strings.Trim(out.String(), "-")
	if flattened == "" {
		return "", errors.New("substrate actor name is required")
	}
	if len(flattened) <= maxRouterLabelLen {
		return flattened, nil
	}
	digest := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(digest[:])[:12]
	keep := maxRouterLabelLen - len(suffix) - 1
	return flattened[:keep] + "-" + suffix, nil
}

// actorHost returns the Host authority the atenet router parses into
// (atespace, actor): <actor>.<atespace>.actors.resources.substrate.ate.dev.
func (c *LiveClient) actorHost(name string) (actorLabel, host string, err error) {
	actorLabel, err = liveActorLabel(name)
	if err != nil {
		return "", "", err
	}
	return actorLabel, actorLabel + "." + c.atespace + "." + actorDNSDomainSuffix, nil
}

// NewLiveClientFromEnv builds the live Client from GateConfigFromEnv. It
// returns (nil, false, nil) when the gate is off so callers keep the safe
// hold without branching on env parsing themselves.
func NewLiveClientFromEnv() (*LiveClient, bool, error) {
	gate := GateConfigFromEnv()
	if !gate.LiveEnabled() {
		return nil, false, nil
	}
	client, err := NewLiveClient(LiveConfig{
		Endpoint:      gate.Endpoint,
		Atespace:      gate.Atespace,
		ActorTemplate: gate.ActorTemplate,
		ShimEndpoint:  gate.ShimEndpoint,
		AuthToken:     gate.Token,
	})
	if err != nil {
		return nil, false, err
	}
	return client, true, nil
}

// Endpoint returns the configured router origin without credentials. The auth
// token is never exposed through this or any other accessor.
func (c *LiveClient) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// Atespace returns the configured Substrate tenancy backing this client.
func (c *LiveClient) Atespace() string {
	if c == nil {
		return ""
	}
	return c.atespace
}

func (c *LiveClient) shimBase() string {
	if c == nil {
		return ""
	}
	return c.shimEndpoint
}

func (c *LiveClient) newRequest(ctx context.Context, method, requestURL, host string, body any) (*http.Request, context.CancelFunc, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	request, err := http.NewRequestWithContext(callCtx, method, requestURL, reader)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if host != "" {
		request.Host = host
	}
	if strings.TrimSpace(c.authToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.authToken))
	}
	return request, cancel, nil
}

func (c *LiveClient) roundTrip(request *http.Request) (int, []byte, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, liveBodyLimit+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if len(raw) > liveBodyLimit {
		return response.StatusCode, nil, errors.New("substrate response exceeds the size limit")
	}
	return response.StatusCode, raw, nil
}

// probeRouter issues one Host-addressed request through atenet-router. The
// router resumes the actor on every request and forwards to the worker, so a
// probe both observes and warms: 2xx means the actor is running, 404 means
// ateapi has no such actor (upstream maps gRPC NotFound to HTTP 404), and
// 503/504 surface pool exhaustion or parked-request timeouts for the caller
// to retry with backoff.
func (c *LiveClient) probeRouter(ctx context.Context, namespace, name string) (ActorHandle, error) {
	actorLabel, host, err := c.actorHost(name)
	if err != nil {
		return ActorHandle{}, err
	}
	request, cancel, err := c.newRequest(ctx, http.MethodGet, c.endpoint+"/", host, nil)
	if err != nil {
		return ActorHandle{}, err
	}
	defer cancel()
	status, _, err := c.roundTrip(request)
	if err != nil {
		return ActorHandle{}, err
	}
	key := ActorKey(namespace, name)
	switch {
	case status == http.StatusNotFound:
		return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, key)
	case status >= 200 && status < 300:
		// The router forwarded to a worker, so the actor is running. The
		// router exposes no resume counter; the data-plane probe reports
		// liveness, while ResumeActor callers observe warm-vs-cold through
		// EnsureTurnActor like the fake backend does.
		return ActorHandle{Namespace: namespace, Name: name, ID: c.atespace + "/" + actorLabel, State: ActorStateActive}, nil
	case status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return ActorHandle{}, fmt.Errorf("substrate router overloaded (status %d) for %s; retry with backoff", status, key)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ActorHandle{}, fmt.Errorf("substrate router denied (status %d) for %s", status, key)
	default:
		return ActorHandle{}, fmt.Errorf("substrate router call failed with status %d for %s", status, key)
	}
}

// shimPayload is the spike-adapter JSON exchanged with
// cmd/substrate-ate-shim, which bridges to ateapi through kubectl ate. Field
// names stay provider-neutral on the Anvil side; the shim owns the kubectl
// command mapping.
type shimPayload struct {
	Atespace      string            `json:"atespace,omitempty"`
	Name          string            `json:"name,omitempty"`
	ID            string            `json:"id,omitempty"`
	State         string            `json:"state,omitempty"`
	ActorClass    string            `json:"actorClass,omitempty"`
	Pool          string            `json:"pool,omitempty"`
	HarnessKind   string            `json:"harnessKind,omitempty"`
	ActorTemplate string            `json:"actorTemplate,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Reused        bool              `json:"reused,omitempty"`
}

func (c *LiveClient) shimCall(ctx context.Context, method, path string, body *shimPayload, namespace, name, defaultState string) (ActorHandle, error) {
	if strings.TrimSpace(c.shimEndpoint) == "" {
		return ActorHandle{}, ErrLiveControlPlaneRequired
	}
	request, cancel, err := c.newRequest(ctx, method, c.shimEndpoint+path, "", body)
	if err != nil {
		return ActorHandle{}, err
	}
	defer cancel()
	status, raw, err := c.roundTrip(request)
	if err != nil {
		return ActorHandle{}, err
	}
	key := ActorKey(namespace, name)
	if status == http.StatusNotFound {
		return ActorHandle{}, fmt.Errorf("%w: %s", ErrActorNotFound, key)
	}
	if status < 200 || status >= 300 {
		return ActorHandle{}, fmt.Errorf("substrate control shim call failed with status %d for %s", status, key)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ActorHandle{Namespace: namespace, Name: name, ID: c.atespace + "/" + mustLiveLabel(name), State: ActorState(defaultState)}, nil
	}
	var payload shimPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ActorHandle{}, fmt.Errorf("decode substrate control shim response: %w", err)
	}
	handle := ActorHandle{Namespace: namespace, Name: name, ID: strings.TrimSpace(payload.ID), State: ActorState(strings.TrimSpace(payload.State))}
	if handle.ID == "" {
		actorLabel, _, labelErr := c.actorHost(name)
		if labelErr != nil {
			return ActorHandle{}, labelErr
		}
		handle.ID = c.atespace + "/" + actorLabel
	}
	if strings.TrimSpace(string(handle.State)) == "" {
		handle.State = ActorState(defaultState)
	}
	return handle, nil
}

func mustLiveLabel(name string) string {
	label, err := liveActorLabel(name)
	if err != nil {
		return strings.TrimSpace(name)
	}
	return label
}

func shimActorPath(atespace, actorLabel, action string) string {
	path := "/shim/v1/actors/" + url.PathEscape(atespace) + "/" + url.PathEscape(actorLabel)
	if strings.TrimSpace(action) != "" {
		path += ":" + url.PathEscape(strings.TrimSpace(action))
	}
	return path
}

// CreateActor creates the actor through the control shim (kubectl-ate
// CreateActor against ateapi) or reuses the existing actor when the shim
// reports AlreadyExists as a reuse. It fails closed with
// ErrLiveControlPlaneRequired when no shim is configured because the
// atenet-router data plane cannot create actors.
func (c *LiveClient) CreateActor(ctx context.Context, spec ActorSpec) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if err := ValidateSpec(spec); err != nil {
		return ActorHandle{}, err
	}
	actorLabel, _, err := c.actorHost(spec.Name)
	if err != nil {
		return ActorHandle{}, err
	}
	template := c.actorTemplate
	if strings.TrimSpace(template) == "" {
		template = strings.TrimSpace(spec.Labels[ShimTemplateLabel])
	}
	body := &shimPayload{
		Atespace:      c.atespace,
		Name:          actorLabel,
		ActorClass:    strings.TrimSpace(spec.ActorClass),
		Pool:          strings.TrimSpace(spec.Pool),
		HarnessKind:   strings.TrimSpace(spec.HarnessKind),
		ActorTemplate: template,
		Labels:        spec.Labels,
	}
	return c.shimCall(ctx, http.MethodPut, shimActorPath(c.atespace, actorLabel, ""), body, spec.Namespace, spec.Name, string(ActorStateActive))
}

// ResumeActor resumes the actor through atenet-router: one Host-addressed
// HTTP request, which the router turns into an ateapi ResumeActor plus a
// forward to the worker. Warm actors answer from a Redis read; cold actors
// restore from snapshot, which is the latency the spike measures.
func (c *LiveClient) ResumeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return ActorHandle{}, errors.New("substrate actor namespace and name are required")
	}
	return c.probeRouter(ctx, namespace, name)
}

// SuspendActor persists actor state through the control shim (kubectl-ate
// SuspendActor: snapshot to external storage, worker released).
func (c *LiveClient) SuspendActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return ActorHandle{}, errors.New("substrate actor namespace and name are required")
	}
	actorLabel, _, err := c.actorHost(name)
	if err != nil {
		return ActorHandle{}, err
	}
	return c.shimCall(ctx, http.MethodPost, shimActorPath(c.atespace, actorLabel, "suspend"), nil, namespace, name, string(ActorStateSuspended))
}

// PauseActor checkpoints the actor through the control shim (kubectl-ate
// PauseActor: snapshot stays on the node VM, resume prefers that node).
func (c *LiveClient) PauseActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return ActorHandle{}, errors.New("substrate actor namespace and name are required")
	}
	actorLabel, _, err := c.actorHost(name)
	if err != nil {
		return ActorHandle{}, err
	}
	return c.shimCall(ctx, http.MethodPost, shimActorPath(c.atespace, actorLabel, "pause"), nil, namespace, name, string(ActorStatePaused))
}

// DescribeActor probes the actor through atenet-router. A 2xx probe means the
// actor is running; 404 means ateapi has no such actor. Probing resumes as a
// side effect (the router resumes on every request), so the router cannot
// distinguish Suspended from Paused — full status reads go through the
// control shim when it is configured.
func (c *LiveClient) DescribeActor(ctx context.Context, namespace, name string) (ActorHandle, error) {
	if c == nil {
		return ActorHandle{}, errors.New("substrate live client is not configured")
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" {
		return ActorHandle{}, errors.New("substrate actor namespace and name are required")
	}
	if strings.TrimSpace(c.shimEndpoint) != "" {
		actorLabel, _, err := c.actorHost(name)
		if err != nil {
			return ActorHandle{}, err
		}
		return c.shimCall(ctx, http.MethodGet, shimActorPath(c.atespace, actorLabel, ""), nil, namespace, name, string(ActorStateActive))
	}
	return c.probeRouter(ctx, namespace, name)
}
