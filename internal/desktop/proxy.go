package desktop

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

var errUnsupportedUIConfigEncoding = errors.New("unsupported ui-config content-encoding")

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
		// Browser fetches same-origin through the desktop host but send
		// Origin: http://127.0.0.1:<port>. Primaris denies that origin; the
		// proxy is server-side and must not forward browser Origin/Referer.
		req.Header.Del("Origin")
		req.Header.Del("Referer")
		req.Header.Del("Forwarded")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Set("X-Forwarded-Proto", target.Scheme)
		if path.Clean("/"+strings.TrimPrefix(req.URL.Path, "/")) == "/ui-config.json" {
			// Drop client gzip so Transport can decode, and so rewriteUIConfig
			// can prefer desktop.oidcClientId instead of leaving a compressed
			// Console User-Agent clientId in place.
			req.Header.Del("Accept-Encoding")
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Set-Cookie")
		if path.Clean(request.URL.Path) == "/ui-config.json" && resp.StatusCode == http.StatusOK {
			return s.rewriteUIConfig(resp)
		}
		return nil
	}
	proxy.ServeHTTP(writer, request)
}

func (s *Server) rewriteUIConfig(resp *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return err
	}
	decoded, uncompressed, err := decodeMaybeGzip(raw, resp.Header.Get("Content-Encoding"))
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(decoded, &body); err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		resp.ContentLength = int64(len(raw))
		return nil
	}
	body["productTitle"] = ProductTitle
	desktop, _ := body["desktop"].(map[string]any)
	if desktop == nil {
		desktop = map[string]any{}
	}
	desktop["wrapper"] = true
	desktop["kubernetes"] = false
	desktop["productName"] = ProductTitle
	desktop["wsl"] = true
	if path := strings.TrimSpace(s.opts.OIDCRedirectPath); path != "" {
		desktop["oidcRedirectPath"] = path
	}
	if clientID := desktopOIDCClientID(s.opts.OIDCClientID, body); clientID != "" {
		desktop["oidcClientId"] = clientID
		oidc, _ := body["oidc"].(map[string]any)
		if oidc == nil {
			oidc = map[string]any{}
			body["oidc"] = oidc
		}
		oidc["clientId"] = clientID
	}
	body["desktop"] = desktop
	rewritten, err := json.Marshal(body)
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		resp.ContentLength = int64(len(raw))
		return nil
	}
	if uncompressed {
		resp.Header.Del("Content-Encoding")
	}
	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	resp.Header.Set("Content-Type", "application/json")
	return nil
}

func decodeMaybeGzip(raw []byte, encodingHeader string) (decoded []byte, uncompressed bool, err error) {
	switch strings.ToLower(strings.TrimSpace(encodingHeader)) {
	case "", "identity":
		return raw, false, nil
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false, err
		}
		out, err := io.ReadAll(io.LimitReader(reader, 1<<20))
		closeErr := reader.Close()
		if err != nil {
			return nil, false, err
		}
		if closeErr != nil {
			return nil, false, closeErr
		}
		return out, true, nil
	default:
		return nil, false, errUnsupportedUIConfigEncoding
	}
}

func desktopOIDCClientID(cliOverride string, body map[string]any) string {
	if id := strings.TrimSpace(cliOverride); id != "" {
		return id
	}
	if desktop, ok := body["desktop"].(map[string]any); ok {
		if id, _ := desktop["oidcClientId"].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	// Empty keeps the API's oidc.clientId (Console User-Agent PKCE). Do not
	// substitute DefaultOIDCClientID: that Kind pattern name is not the
	// Zitadel Native app, and the SPA must warn instead of silently staying
	// on Console once desktop.oidcClientId exists.
	return ""
}
