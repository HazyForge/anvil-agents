package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	apiProbeTimeout = 2500 * time.Millisecond
	issuerCacheTTL  = time.Minute
)

// APIStatus is a bounded, unauthenticated probe of the configured OIDC API origin.
type APIStatus struct {
	Origin    string `json:"origin,omitempty"`
	Reachable bool   `json:"reachable"`
	Message   string `json:"message,omitempty"`
}

// WrapperTool is one of the two built-in wrapper agent tools.
type WrapperTool struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Notes       string `json:"notes"`
}

// WrapperInfo describes the built-in wrapper agent. It is not a second console.
type WrapperInfo struct {
	Tools   []WrapperTool `json:"tools"`
	Message string        `json:"message"`
}

// Snapshot is the payload for GET /local/v1/snapshot.
type Snapshot struct {
	ProductTitle string       `json:"productTitle"`
	ListenAddr   string       `json:"listenAddr,omitempty"`
	Prefs        Prefs        `json:"prefs"`
	API          APIStatus    `json:"api"`
	Harnesses    []Discovered `json:"harnesses"`
	Wrapper      WrapperInfo  `json:"wrapper"`
}

func defaultWrapper() WrapperInfo {
	return WrapperInfo{
		Tools: []WrapperTool{
			{
				ID:          "anvil-api",
				DisplayName: "anvil-agents API",
				Notes:       "Call the remote OIDC AgentRun API the same way the browser console does: Bearer in Authorization, never in a query string. Runs are append-only. The host never stores the access token.",
			},
			{
				ID:          "local-harness",
				DisplayName: "local harness",
				Notes:       "Delegate a prompt to a catalog CLI already on this machine. The CLI uses its own local auth files. The OIDC token is not copied into argv, env, or the prompt file.",
			},
		},
		Message: "Anvil Agents Desktop is a wrapper agent with those two tools. It is not a Kubernetes operator UI and not a second Anvil Agents Console.",
	}
}

func (s *Server) snapshot(ctx context.Context) Snapshot {
	prefs := s.currentPrefs()
	return Snapshot{
		ProductTitle: ProductTitle,
		ListenAddr:   s.opts.Listen,
		Prefs:        prefs,
		API:          s.probeAPI(ctx, prefs.APIOrigin),
		Harnesses:    s.opts.Discoverer.Discover(ctx),
		Wrapper:      defaultWrapper(),
	}
}

func (s *Server) probeAPI(ctx context.Context, origin string) APIStatus {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return APIStatus{Message: "Set the anvil-agents OIDC API origin, then sign in. Anvil Agents Desktop does not use kubeconfig."}
	}
	health := strings.TrimRight(origin, "/") + "/healthz"
	probeCtx, cancel := context.WithTimeout(ctx, apiProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, health, nil)
	if err != nil {
		return APIStatus{Origin: origin, Message: err.Error()}
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return APIStatus{Origin: origin, Message: "API /healthz failed: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return APIStatus{Origin: origin, Message: fmt.Sprintf("API /healthz returned HTTP %d", resp.StatusCode)}
	}
	s.refreshIssuerCache(probeCtx, origin)
	return APIStatus{Origin: origin, Reachable: true, Message: "OIDC API origin is healthy"}
}

type uiConfigBody struct {
	OIDC struct {
		Issuer string `json:"issuer"`
	} `json:"oidc"`
}

func (s *Server) refreshIssuerCache(ctx context.Context, apiOrigin string) string {
	apiOrigin = strings.TrimSpace(apiOrigin)
	if apiOrigin == "" {
		return ""
	}
	s.mu.Lock()
	if s.issuer.apiOrigin == apiOrigin && time.Since(s.issuer.at) < issuerCacheTTL {
		out := s.issuer.connect
		s.mu.Unlock()
		return out
	}
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiOrigin, "/")+"/ui-config.json", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var body uiConfigBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	connect := originOf(body.OIDC.Issuer)
	s.mu.Lock()
	s.issuer = issuerCache{apiOrigin: apiOrigin, connect: connect, at: time.Now()}
	s.mu.Unlock()
	return connect
}
