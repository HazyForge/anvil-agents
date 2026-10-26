package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type browserLoginOptions struct {
	clientID       string
	redirectURIs   []string
	allowedOrigins []string
	mint           mintOptions
}

func (opts browserLoginOptions) validate() error {
	if opts.clientID == "" {
		return fmt.Errorf("--login-client-id is required with --browser-login")
	}
	if len(opts.redirectURIs) == 0 {
		return fmt.Errorf("at least one --login-redirect-uri is required with --browser-login")
	}
	for _, raw := range opts.redirectURIs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || parsed.User != nil || strings.Contains(raw, "#") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("--login-redirect-uri must be an absolute http(s) URL without user info or fragment")
		}
	}
	for _, raw := range opts.allowedOrigins {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.ContainsAny(raw, "*#") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("--login-allowed-origin must be an exact http(s) origin without wildcard, path, query or fragment")
		}
	}
	if opts.mint.ttl <= 0 || opts.mint.ttl > maxMintTTL {
		return fmt.Errorf("--login-ttl must be positive and at most %s", maxMintTTL)
	}
	if opts.mint.subject == "" || len(opts.mint.audiences) == 0 || opts.mint.namespaceClaim == "" {
		return fmt.Errorf("--login-subject, --audience and --login-namespace-claim must not be empty")
	}
	return nil
}

type authorizationCode struct {
	clientID    string
	redirectURI string
	challenge   string
	expiresAt   time.Time
}

// browserLogin is TEST ONLY: the configured local identity is auto-approved.
// Codes never leave memory except in the vetted authorization redirect.
type browserLogin struct {
	key      *rsa.PrivateKey
	opts     browserLoginOptions
	fallback http.Handler
	now      func() time.Time
	mu       sync.Mutex
	codes    map[string]authorizationCode
}

func newBrowserLoginHandler(key *rsa.PrivateKey, opts browserLoginOptions, fallback http.Handler) *browserLogin {
	return &browserLogin{
		key: key, opts: opts, fallback: fallback, now: time.Now,
		codes: make(map[string]authorizationCode),
	}
}

func (login *browserLogin) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/.well-known/openid-configuration", "/keys", "/token":
		writer.Header().Add("Vary", "Origin")
		if origin := request.Header.Get("Origin"); origin != "" && slices.Contains(login.opts.allowedOrigins, origin) {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			if request.Method == http.MethodOptions {
				writer.Header().Set("Access-Control-Allow-Methods", "POST, GET")
				writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			}
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	switch request.URL.Path {
	case "/.well-known/openid-configuration":
		base := strings.TrimRight(login.opts.mint.issuer, "/")
		writeJSON(writer, map[string]any{
			"issuer":                                login.opts.mint.issuer,
			"jwks_uri":                              base + "/keys",
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	case "/authorize":
		login.authorize(writer, request)
	case "/token":
		login.token(writer, request)
	default:
		login.fallback.ServeHTTP(writer, request)
	}
}

func (login *browserLogin) authorize(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodGet {
		http.Error(writer, "GET required", http.StatusBadRequest)
		return
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		http.Error(writer, "invalid query", http.StatusBadRequest)
		return
	}
	for _, field := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method"} {
		if len(query[field]) != 1 || query.Get(field) == "" {
			http.Error(writer, "missing or repeated "+field, http.StatusBadRequest)
			return
		}
	}
	if query.Get("response_type") != "code" || query.Get("client_id") != login.opts.clientID {
		http.Error(writer, "invalid response_type or client_id", http.StatusBadRequest)
		return
	}
	// Redirect only to the configured URI the request names, never to the
	// request's own copy of it.
	configured := slices.Index(login.opts.redirectURIs, query.Get("redirect_uri"))
	if configured < 0 {
		http.Error(writer, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	redirectURI := login.opts.redirectURIs[configured]
	challenge := query.Get("code_challenge")
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	if query.Get("code_challenge_method") != "S256" || err != nil || len(decoded) != sha256.Size || base64.RawURLEncoding.EncodeToString(decoded) != challenge {
		http.Error(writer, "S256 code_challenge required", http.StatusBadRequest)
		return
	}
	if len(query["state"]) > 1 {
		http.Error(writer, "repeated state", http.StatusBadRequest)
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		http.Error(writer, "code generation failed", http.StatusInternalServerError)
		return
	}
	code := base64.RawURLEncoding.EncodeToString(random[:])
	login.mu.Lock()
	now := login.now()
	for existing, entry := range login.codes {
		if !now.Before(entry.expiresAt) {
			delete(login.codes, existing)
		}
	}
	login.codes[code] = authorizationCode{
		clientID: login.opts.clientID, redirectURI: redirectURI,
		challenge: challenge, expiresAt: now.Add(time.Minute),
	}
	login.mu.Unlock()

	// Only the configured redirect's own query, code and original state survive.
	redirect, _ := url.Parse(redirectURI) // Validated at startup.
	values := redirect.Query()
	values.Set("code", code)
	if state, present := query["state"]; present {
		values.Set("state", state[0])
	}
	redirect.RawQuery = values.Encode()
	http.Redirect(writer, request, redirect.String(), http.StatusFound)
}

func (login *browserLogin) token(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	if request.Method != http.MethodPost {
		writeOAuthError(writer, "invalid_request")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		writeOAuthError(writer, "invalid_request")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	if err := request.ParseForm(); err != nil {
		writeOAuthError(writer, "invalid_request")
		return
	}
	form := request.PostForm // Never accept credentials from a URL query.
	if len(form["code"]) != 1 || form.Get("code") == "" {
		writeOAuthError(writer, "invalid_request")
		return
	}
	// Atomically consume before checking the grant, bindings or verifier. A
	// failed redemption must burn the code, just like a successful redemption.
	login.mu.Lock()
	entry, found := login.codes[form.Get("code")]
	delete(login.codes, form.Get("code"))
	unexpired := found && login.now().Before(entry.expiresAt)
	login.mu.Unlock()
	for _, field := range []string{"grant_type", "client_id", "redirect_uri", "code_verifier"} {
		if len(form[field]) != 1 || form.Get(field) == "" {
			writeOAuthError(writer, "invalid_request")
			return
		}
	}
	if form.Get("grant_type") != "authorization_code" {
		writeOAuthError(writer, "unsupported_grant_type")
		return
	}
	verifier := form.Get("code_verifier")
	digest := sha256.Sum256([]byte(verifier))
	if !unexpired || entry.clientID != form.Get("client_id") || entry.redirectURI != form.Get("redirect_uri") || !validCodeVerifier(verifier) || base64.RawURLEncoding.EncodeToString(digest[:]) != entry.challenge {
		writeOAuthError(writer, "invalid_grant")
		return
	}
	raw, err := mintToken(login.key, login.opts.mint)
	if err != nil {
		writeOAuthError(writer, "server_error")
		return
	}
	writeJSON(writer, map[string]any{
		"access_token": raw,
		"token_type":   "Bearer",
		"expires_in":   int64(login.opts.mint.ttl / time.Second),
		"scope":        login.opts.mint.scope,
	})
}

func validCodeVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, char := range verifier {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", char)) {
			return false
		}
	}
	return true
}

func writeOAuthError(writer http.ResponseWriter, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusBadRequest)
	writeJSON(writer, map[string]string{"error": code})
}
