package desktop

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

func proxyPathAllowed(method, rawPath string) bool {
	cleaned := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(rawPath), "/"))
	if strings.Contains(rawPath, "..") || strings.Contains(cleaned, "..") {
		return false
	}
	switch cleaned {
	case "/ui-config.json":
		return method == http.MethodGet || method == http.MethodHead
	}
	if !strings.HasPrefix(cleaned, "/api/v1/namespaces/") {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) handleAPIProxy(writer http.ResponseWriter, request *http.Request) {
	if hasTokenQuery(request) {
		writeError(writer, http.StatusBadRequest, "token_in_query", "access tokens must not be placed in query strings")
		return
	}
	if !proxyPathAllowed(request.Method, request.URL.Path) {
		writeError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	origin := s.currentPrefs().APIOrigin
	if origin == "" {
		writeError(writer, http.StatusServiceUnavailable, "api_origin_unset", "set the anvil-agents OIDC API origin first")
		return
	}
	target, err := url.Parse(origin)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_prefs", "api origin is invalid")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if s.opts.Transport != nil {
		proxy.Transport = s.opts.Transport
	}
	proxy.FlushInterval = 200 * time.Millisecond
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, http.StatusBadGateway, "api_unreachable", "anvil-agents API origin is unreachable")
	}
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.Header.Del("Forwarded")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Set("X-Forwarded-Proto", target.Scheme)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if path.Clean(request.URL.Path) == "/ui-config.json" && resp.StatusCode == http.StatusOK {
			return rewriteUIConfig(resp)
		}
		return nil
	}
	proxy.ServeHTTP(writer, request)
}

func rewriteUIConfig(resp *http.Response) error {
	if strings.TrimSpace(resp.Header.Get("Content-Encoding")) != "" {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		resp.ContentLength = int64(len(raw))
		return nil
	}
	body["productTitle"] = ProductTitle
	body["desktop"] = map[string]any{
		"wrapper":     true,
		"kubernetes":  false,
		"productName": ProductTitle,
	}
	rewritten, err := json.Marshal(body)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		resp.ContentLength = int64(len(raw))
		return nil
	}
	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	resp.Header.Set("Content-Type", "application/json")
	return nil
}
