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
	"time"

	"github.com/hazyforge/anvil-agents/internal/desktop/uifs"
)

const (
	defaultListen = "127.0.0.1:1738"
	maxPrefsBytes = 16 << 10
	uiIndexFile   = "index.html"
	uiDistRoot    = "dist"
)

// Options configure the local desktop host.
type Options struct {
	Listen       string
	UIDir        string
	Kubeconfig   string
	ConfigDir    string
	Discoverer   Discoverer
	ClusterProbe ClusterProbe
	ConsoleProbe ConsoleProbe
	OnListen     func(addr string)
}

// Server is the loopback desktop host.
type Server struct {
	opts  Options
	prefs Prefs
	mux   http.Handler
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
	if strings.TrimSpace(prefs.Kubeconfig) == "" {
		prefs.Kubeconfig = strings.TrimSpace(opts.Kubeconfig)
	}
	server := &Server{opts: opts, prefs: prefs}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.handleHealthz)
	mux.HandleFunc("GET /local/v1/snapshot", server.handleSnapshot)
	mux.HandleFunc("POST /local/v1/prefs", server.handlePrefs)
	mux.HandleFunc("/", server.handleUI)
	server.mux = securityHeaders(mux)
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

func (s *Server) Start(ctx context.Context) error {
	httpServer := &http.Server{
		Addr:              s.opts.Listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
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

func (s *Server) handleHealthz(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSnapshot(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, s.snapshot(request.Context()))
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
	if err := validatePrefs(next); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_prefs", err.Error())
		return
	}
	if err := savePrefs(s.opts.ConfigDir, next); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_prefs", err.Error())
		return
	}
	s.prefs = next
	writeJSON(writer, http.StatusOK, s.snapshot(request.Context()))
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
		return fmt.Errorf("anvil-desktop listens on loopback only (got %s)", addr)
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(path.Clean(request.URL.Path), "/local/") || path.Clean(request.URL.Path) == "/healthz" {
			writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		} else {
			writer.Header().Set("Content-Security-Policy", strings.Join([]string{
				"default-src 'self'",
				"base-uri 'self'",
				"object-src 'none'",
				"frame-ancestors 'none'",
				"img-src 'self' data:",
				"style-src 'self' 'unsafe-inline'",
				"font-src 'self'",
				"script-src 'self'",
				"connect-src 'self'",
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
