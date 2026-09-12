package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/hazyforge/anvil-agents/internal/desktop/uifs"
)

const (
	defaultListen   = "127.0.0.1:1738"
	maxPrefsBytes   = 16 << 10
	maxDelegateJSON = maxPromptBytes + 8<<10
	uiIndexFile     = "index.html"
	uiDistRoot      = "dist"
)

// Options configure the local desktop host.
type Options struct {
	Listen           string
	UIDir            string
	APIOrigin        string
	ConfigDir        string
	OIDCClientID     string
	OIDCRedirectPath string
	Discoverer       Discoverer
	HTTPClient       *http.Client
	Transport        http.RoundTripper
	OnListen         func(addr string)
}

type issuerCache struct {
	apiOrigin string
	connect   string
	at        time.Time
}

// Server is the loopback desktop host.
type Server struct {
	opts       Options
	mu         sync.Mutex
	prefs      Prefs
	issuer     issuerCache
	httpClient *http.Client
	mux        http.Handler
}

func NewServer(opts Options) (*Server, error) {
	if strings.TrimSpace(opts.Listen) == "" {
		opts.Listen = defaultListen
	}
	if err := requireLoopback(opts.Listen); err != nil {
		return nil, err
	}
	prefs, err := loadPrefs(opts.ConfigDir)
	if err != nil {
		return nil, err
	}
	if origin := strings.TrimSpace(opts.APIOrigin); origin != "" {
		parsed, err := ParseAPIOrigin(origin)
		if err != nil {
			return nil, err
		}
		prefs.APIOrigin = parsed
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				if len(via) == 0 {
					return nil
				}
				orig := via[0].URL
				if req.URL.Scheme != orig.Scheme || req.URL.Host != orig.Host {
					return fmt.Errorf("redirect to a different origin is not allowed")
				}
				return nil
			},
		}
	}
	server := &Server{opts: opts, prefs: prefs, httpClient: client}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.handleHealthz)
	mux.HandleFunc("GET /local/v1/snapshot", server.handleSnapshot)
	mux.HandleFunc("GET /local/v1/api-health", server.handleAPIHealth)
	mux.HandleFunc("POST /local/v1/prefs", server.handlePrefs)
	mux.HandleFunc("POST /local/v1/delegate", server.handleDelegate)
	mux.HandleFunc("/ui-config.json", server.handleAPIProxy)
	mux.HandleFunc("/api/", server.handleAPIProxy)
	mux.HandleFunc("/", server.handleUI)
	server.mux = server.securityHeaders(rejectTokenQuery(mux))
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) ListenAddr() string {
	return s.opts.Listen
}

func (s *Server) Snapshot(ctx context.Context) Snapshot {
	return s.snapshot(ctx)
}

func (s *Server) currentPrefs() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs
}

func (s *Server) setPrefs(prefs Prefs) {
	s.mu.Lock()
	s.prefs = prefs
	s.issuer = issuerCache{}
	s.mu.Unlock()
}

func (s *Server) Start(ctx context.Context) error {
	httpServer := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      6 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	listener, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.opts.Listen, err)
	}
	s.opts.Listen = listener.Addr().String()
	if s.opts.OnListen != nil {
		s.opts.OnListen(s.opts.Listen)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealthz(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSnapshot(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, s.snapshot(request.Context()))
}

func (s *Server) handleAPIHealth(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, s.probeAPI(request.Context(), s.currentPrefs().APIOrigin))
}

func (s *Server) handlePrefs(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, maxPrefsBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_body", "unable to read prefs")
		return
	}
	if len(raw) > maxPrefsBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "too_large", "prefs payload is too large")
		return
	}
	var next Prefs
	if err := json.Unmarshal(raw, &next); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "prefs must be JSON")
		return
	}
	normalized, err := normalizePrefs(next)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_prefs", err.Error())
		return
	}
	if err := savePrefs(s.opts.ConfigDir, normalized); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_prefs", err.Error())
		return
	}
	s.setPrefs(normalized)
	writeJSON(writer, http.StatusOK, s.snapshot(request.Context()))
}

func (s *Server) handleDelegate(writer http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, maxDelegateJSON+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_body", "unable to read delegate request")
		return
	}
	if len(raw) > maxDelegateJSON {
		writeError(writer, http.StatusRequestEntityTooLarge, "too_large", "delegate payload is too large")
		return
	}
	var body DelegateRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "delegate request must be JSON")
		return
	}
	prompt := body.Prompt
	if strings.TrimSpace(prompt) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_prompt", "prompt is required")
		return
	}
	if len(prompt) > maxPromptBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "too_large", "prompt exceeds 64KiB")
		return
	}
	tool, bin, err := s.resolveDelegate(body.Harness)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_harness", err.Error())
		return
	}
	result, err := runDelegate(request.Context(), tool, bin, prompt, delegateTimeout(body.TimeoutSeconds))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "delegate_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleUI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are supported")
		return
	}
	root, err := s.uiFileSystem()
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "ui_unavailable", "desktop UI assets are unavailable")
		return
	}
	requestPath := cleanUIPath(request.URL.Path)
	servePath := requestPath
	if requestPath == "" || requestPath == "." {
		servePath = uiIndexFile
	}
	if file, err := root.Open(servePath); err == nil {
		info, statErr := file.Stat()
		if statErr == nil && !info.IsDir() {
			serveUIFile(writer, request, servePath, info, file)
			return
		}
		_ = file.Close()
	}
	if path.Ext(servePath) != "" {
		writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	index, err := root.Open(uiIndexFile)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "ui_unavailable", "desktop UI index is unavailable")
		return
	}
	info, err := index.Stat()
	if err != nil {
		_ = index.Close()
		writeError(writer, http.StatusServiceUnavailable, "ui_unavailable", "desktop UI index is unavailable")
		return
	}
	serveUIFile(writer, request, uiIndexFile, info, index)
}

func (s *Server) uiFileSystem() (http.FileSystem, error) {
	if dir := strings.TrimSpace(s.opts.UIDir); dir != "" {
		return http.FS(os.DirFS(dir)), nil
	}
	sub, err := fs.Sub(uifs.Dist, uiDistRoot)
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

func serveUIFile(writer http.ResponseWriter, request *http.Request, name string, info fs.FileInfo, file http.File) {
	defer file.Close()
	if strings.HasSuffix(name, ".html") {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	http.ServeContent(writer, request, name, info.ModTime(), file)
}

func cleanUIPath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	if strings.Contains(cleaned, "..") {
		return ""
	}
	return cleaned
}

func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("Anvil Agents Desktop listens on loopback only (got %s)", addr)
	}
	return nil
}

func rejectTokenQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if hasTokenQuery(request) {
			writeError(writer, http.StatusBadRequest, "token_in_query", "access tokens must not be placed in query strings")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		cleaned := path.Clean(request.URL.Path)
		if strings.HasPrefix(cleaned, "/local/") || cleaned == "/healthz" || strings.HasPrefix(cleaned, "/api/") || cleaned == "/ui-config.json" {
			writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		} else {
			connect := "'self'"
			if issuer := s.refreshIssuerCache(request.Context(), s.currentPrefs().APIOrigin); issuer != "" {
				connect += " " + issuer
			}
			writer.Header().Set("Content-Security-Policy", strings.Join([]string{
				"default-src 'self'",
				"base-uri 'self'",
				"object-src 'none'",
				"frame-ancestors 'none'",
				"img-src 'self' data:",
				"style-src 'self' 'unsafe-inline'",
				"font-src 'self'",
				"script-src 'self'",
				"connect-src " + connect,
			}, "; "))
		}
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]string{"error": code, "message": message})
}
