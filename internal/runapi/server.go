package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type principalContextKey struct{}

const (
	listPageSize         int64 = 500
	responseWriteTimeout       = 15 * time.Second
)

type Server struct {
	config        Config
	authenticator AccessTokenAuthenticator
	authorizer    Authorizer
	runs          client.Reader
	logs          AgentRunLogSource
	log           logr.Logger
	httpServer    *http.Server
	limiter       *streamLimiter
}

func NewServer(config Config, authenticator AccessTokenAuthenticator, runs client.Reader, logs AgentRunLogSource, log logr.Logger) (*Server, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if authenticator == nil || runs == nil || logs == nil {
		return nil, fmt.Errorf("authenticator, AgentRun reader, and log source are required")
	}
	server := &Server{
		config:        config,
		authenticator: authenticator,
		authorizer:    NewAuthorizer(config.Authorization),
		runs:          runs,
		logs:          logs,
		log:           log,
		limiter:       newStreamLimiter(config.Stream.MaxConnections, config.Stream.MaxConnectionsPerSubject),
	}
	server.httpServer = &http.Server{
		Addr:              config.BindAddress,
		Handler:           server.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       75 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	return server, nil
}

func (server *Server) Start(ctx context.Context) error {
	if starter, ok := server.authenticator.(interface{ Start(context.Context) }); ok {
		go starter.Start(ctx)
	}
	listener, err := net.Listen("tcp", server.config.BindAddress)
	if err != nil {
		return fmt.Errorf("listen on AgentRun API address: %w", err)
	}
	server.log.Info("AgentRun API listening", "address", listener.Addr().String())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down AgentRun API: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve AgentRun API: %w", err)
	}
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.handleHealth)
	mux.HandleFunc("GET /readyz", server.handleReady)
	mux.Handle("GET /api/v1/namespaces/{namespace}/agent-runs", server.authenticate(http.HandlerFunc(server.handleListRuns)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/agent-runs/{name}", server.authenticate(http.HandlerFunc(server.handleGetRun)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/agent-runs/{name}/events", server.authenticate(http.HandlerFunc(server.handleRunEvents)))
	return server.securityHeaders(server.cors(mux))
}

func (server *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (server *Server) handleReady(writer http.ResponseWriter, _ *http.Request) {
	if !server.authenticator.Ready() {
		writeAPIError(writer, http.StatusServiceUnavailable, "authentication_unavailable", "OIDC verifier is not ready")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ready": true})
}

func (server *Server) handleListRuns(writer http.ResponseWriter, request *http.Request) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.authorizer.Allowed(principal, PermissionRunsRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeAPIError(writer, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	list := &agentsv1alpha1.AgentRunList{}
	continueToken := ""
	for {
		page := &agentsv1alpha1.AgentRunList{}
		pageLimit := min(listPageSize, server.config.List.MaxItems+1-int64(len(list.Items)))
		opts := []client.ListOption{client.InNamespace(namespace), client.Limit(pageLimit)}
		if continueToken != "" {
			opts = append(opts, client.Continue(continueToken))
		}
		if err := server.runs.List(request.Context(), page, opts...); err != nil {
			server.log.Error(err, "list AgentRuns", "subject", principal.Subject, "namespace", namespace)
			writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "AgentRun state is unavailable")
			return
		}
		list.Items = append(list.Items, page.Items...)
		if int64(len(list.Items)) > server.config.List.MaxItems || (int64(len(list.Items)) == server.config.List.MaxItems && page.Continue != "") {
			writeAPIError(writer, http.StatusUnprocessableEntity, "list_too_large", "namespace contains too many AgentRuns to list safely")
			return
		}
		continueToken = page.Continue
		if continueToken == "" {
			break
		}
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].CreationTimestamp.After(list.Items[j].CreationTimestamp.Time)
	})
	if len(list.Items) > limit {
		list.Items = list.Items[:limit]
	}
	response := AgentRunListResponse{Items: make([]AgentRunView, 0, len(list.Items))}
	for i := range list.Items {
		response.Items = append(response.Items, NewAgentRunView(&list.Items[i], false))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleGetRun(writer http.ResponseWriter, request *http.Request) {
	run, principal, ok := server.authorizedRun(writer, request, PermissionRunsRead)
	if !ok {
		return
	}
	server.log.Info("AgentRun read", "subject", principal.Subject, "issuer", principal.Issuer, "namespace", run.Namespace, "agentRun", run.Name)
	writeJSON(writer, http.StatusOK, NewAgentRunView(run, true))
}

func (server *Server) authorizedRun(writer http.ResponseWriter, request *http.Request, permissions ...string) (*agentsv1alpha1.AgentRun, Principal, bool) {
	namespace := request.PathValue("namespace")
	name := request.PathValue("name")
	principal := principalFromContext(request.Context())
	for _, permission := range permissions {
		if !server.authorizer.Allowed(principal, permission, namespace) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return nil, principal, false
		}
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.runs.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, run); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		} else {
			server.log.Error(err, "get AgentRun", "subject", principal.Subject, "namespace", namespace, "agentRun", name)
			writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "AgentRun state is unavailable")
		}
		return nil, principal, false
	}
	return run, principal, true
}

func (server *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Has("access_token") {
			writeAPIError(writer, http.StatusBadRequest, "query_token_rejected", "access tokens are accepted only in the Authorization header")
			return
		}
		rawToken, ok := bearerToken(request.Header.Values("Authorization"))
		if !ok {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents"`)
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "a bearer access token is required")
			return
		}
		principal, err := server.authenticator.Verify(request.Context(), rawToken)
		if err != nil {
			if errors.Is(err, ErrAuthenticationUnavailable) {
				writeAPIError(writer, http.StatusServiceUnavailable, "authentication_unavailable", "OIDC verification is temporarily unavailable")
				return
			}
			writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents", error="invalid_token"`)
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "the bearer access token is invalid")
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)))
	})
}

func (server *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, origin := range server.config.CORS.AllowedOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin != "" {
			if _, ok := allowed[origin]; !ok {
				writeAPIError(writer, http.StatusForbidden, "origin_denied", "request origin is not allowed")
				return
			}
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Last-Event-ID")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.Header().Add("Vary", "Origin")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func bearerToken(headers []string) (string, bool) {
	if len(headers) != 1 {
		return "", false
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return parts[1], true
}

func principalFromContext(ctx context.Context) Principal {
	principal, _ := ctx.Value(principalContextKey{}).(Principal)
	return principal
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	controller := http.NewResponseController(writer)
	_ = controller.SetWriteDeadline(time.Now().Add(responseWriteTimeout))
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

type streamLimiter struct {
	mu            sync.Mutex
	max           int
	maxPerSubject int
	total         int
	bySubject     map[string]int
}

func newStreamLimiter(max, maxPerSubject int) *streamLimiter {
	return &streamLimiter{max: max, maxPerSubject: maxPerSubject, bySubject: map[string]int{}}
}

func (limiter *streamLimiter) acquire(subject string) (func(), bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.total >= limiter.max || limiter.bySubject[subject] >= limiter.maxPerSubject {
		return nil, false
	}
	limiter.total++
	limiter.bySubject[subject]++
	return func() {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		limiter.total--
		limiter.bySubject[subject]--
		if limiter.bySubject[subject] == 0 {
			delete(limiter.bySubject, subject)
		}
	}, true
}
