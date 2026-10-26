package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	browserTestIssuer   = "http://127.0.0.1:18181"
	browserTestClientID = "simulator"
	browserTestRedirect = "http://127.0.0.1:5173/auth/callback"
	// An independent S256 test vector (RFC 7636, Appendix B).
	browserTestVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	browserTestChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func browserTestHandler(t *testing.T, extra ...string) http.Handler {
	t.Helper()
	args := []string{
		"--listen", "127.0.0.1:18181", "--issuer", browserTestIssuer,
		"--key-file", filepath.Join(t.TempDir(), "issuer.json"),
		"--browser-login", "--login-client-id", browserTestClientID,
		"--login-redirect-uri", browserTestRedirect,
	}
	server, err := newIssuerServer(append(args, extra...))
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler
}

func browserTestAuthorizeQuery() url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {browserTestClientID},
		"redirect_uri": {browserTestRedirect}, "state": {"original + / ? & state"},
		"code_challenge": {browserTestChallenge}, "code_challenge_method": {"S256"},
		"scope": {"untrusted-request-scope"}, "nonce": {"request-nonce"},
		"untrusted": {"must-not-be-forwarded"},
	}
}

func browserTestAuthorize(t *testing.T, handler http.Handler, query url.Values) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/authorize?"+query.Encode(), nil))
	if response.Code != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authorization response must not be cached")
	}
	redirect, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal("invalid authorization redirect URL")
	}
	want, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		t.Fatal(err)
	}
	values := redirect.Query()
	code := values.Get("code")
	if code == "" || len(values["code"]) != 1 || values.Get("state") != query.Get("state") {
		t.Fatal("redirect must contain one code and the original state")
	}
	wantValues := want.Query()
	wantValues.Set("code", code)
	if _, present := query["state"]; present {
		wantValues.Set("state", query.Get("state"))
	}
	if !reflect.DeepEqual(values, wantValues) || redirect.Scheme != want.Scheme || redirect.Host != want.Host || redirect.Path != want.Path || redirect.Fragment != "" {
		t.Fatal("redirect changed the vetted destination or forwarded extra parameters")
	}
	return code
}

func browserTestTokenForm(code string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "client_id": {browserTestClientID},
		"redirect_uri": {browserTestRedirect}, "code": {code},
		"code_verifier": {browserTestVerifier},
	}
}

func browserTestToken(handler http.Handler, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func browserTestOAuthError(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("OAuth error status/type = %d/%q", response.Code, response.Header().Get("Content-Type"))
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal("OAuth error is not a JSON string object")
	}
	if len(payload) != 1 || payload["error"] != want {
		t.Fatalf("OAuth error = %q, want %q (with no credentials)", payload["error"], want)
	}
	if response.Header().Get("Location") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token errors must not redirect or be cached")
	}
}

func TestBrowserLoginDiscovery(t *testing.T) {
	server, err := newIssuerServer([]string{"--listen", "127.0.0.1:18181"})
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		handler := server.Handler
		if enabled {
			name = "enabled"
			handler = browserTestHandler(t)
		}
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("discovery status = %d", response.Code)
			}
			var document map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"issuer": browserTestIssuer, "jwks_uri": browserTestIssuer + "/keys"}
			if enabled {
				want["authorization_endpoint"] = browserTestIssuer + "/authorize"
				want["token_endpoint"] = browserTestIssuer + "/token"
				want["response_types_supported"] = []any{"code"}
				want["grant_types_supported"] = []any{"authorization_code"}
				want["code_challenge_methods_supported"] = []any{"S256"}
				want["token_endpoint_auth_methods_supported"] = []any{"none"}
			}
			if !reflect.DeepEqual(document, want) {
				t.Fatalf("discovery = %#v, want %#v", document, want)
			}
			if !enabled {
				for _, path := range []string{"/authorize", "/token"} {
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
					if response.Code != http.StatusNotFound {
						t.Fatalf("%s is available without --browser-login", path)
					}
				}
			}
		})
	}
}

func TestBrowserLoginHappyPath(t *testing.T) {
	for _, tc := range []struct {
		name, subject, namespaceClaim, scope, email string
		flags                                       []string
		audiences, roles, namespaces                []string
		ttl                                         int64
	}{
		{
			name: "mint defaults", subject: "kind-local-desktop", namespaceClaim: "anvil_agents_namespaces",
			audiences: []string{"anvil-agents"}, roles: []string{"kind-local-desktop"}, namespaces: []string{"agents"},
			scope: "openid profile email", ttl: 900,
		},
		{
			name: "configured claims", subject: "sim-user", namespaceClaim: "sim_namespaces",
			audiences: []string{"sim-api", "desktop-api"}, roles: []string{"operator", "viewer"}, namespaces: []string{"agents", "sandbox"},
			scope: "openid simulator", email: "sim@example.test", ttl: 3600,
			flags: []string{
				"--audience", "sim-api,desktop-api", "--login-subject", "sim-user",
				"--login-roles", "operator,viewer", "--login-namespaces", "agents,sandbox",
				"--login-namespace-claim", "sim_namespaces", "--login-email", "sim@example.test",
				"--login-ttl", "1h", "--login-scope", "openid simulator",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := browserTestHandler(t, tc.flags...)
			code := browserTestAuthorize(t, handler, browserTestAuthorizeQuery())
			response := browserTestToken(handler, browserTestTokenForm(code))
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("token status/type = %d/%q", response.Code, response.Header().Get("Content-Type"))
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" || response.Header().Get("Location") != "" {
				t.Fatal("access tokens must stay in the uncached response body")
			}
			var token struct {
				AccessToken  string `json:"access_token"`
				TokenType    string `json:"token_type"`
				ExpiresIn    int64  `json:"expires_in"`
				Scope        string `json:"scope"`
				RefreshToken string `json:"refresh_token"`
				IDToken      string `json:"id_token"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &token); err != nil {
				t.Fatal("invalid token response JSON")
			}
			if token.AccessToken == "" || token.TokenType != "Bearer" || token.ExpiresIn != tc.ttl || token.Scope != tc.scope || token.RefreshToken != "" || token.IDToken != "" {
				t.Fatal("unexpected token response fields")
			}
			keysResponse := httptest.NewRecorder()
			handler.ServeHTTP(keysResponse, httptest.NewRequest(http.MethodGet, "/keys", nil))
			var keys jose.JSONWebKeySet
			if err := json.Unmarshal(keysResponse.Body.Bytes(), &keys); err != nil || len(keys.Keys) != 1 || !keys.Keys[0].IsPublic() {
				t.Fatal("expected one public signing key from JWKS")
			}
			signed, err := jose.ParseSigned(token.AccessToken, []jose.SignatureAlgorithm{jose.RS256})
			if err != nil {
				t.Fatal("access token is not a signed RS256 JWT")
			}
			payload, err := signed.Verify(keys.Keys[0].Key)
			if err != nil {
				t.Fatal("access token signature does not verify with the served public key")
			}
			var claims struct {
				Issuer    string   `json:"iss"`
				Subject   string   `json:"sub"`
				Audience  []string `json:"aud"`
				Roles     []string `json:"roles"`
				Scope     string   `json:"scope"`
				Email     string   `json:"email"`
				Verified  bool     `json:"email_verified"`
				IssuedAt  int64    `json:"iat"`
				ExpiresAt int64    `json:"exp"`
			}
			if err := json.Unmarshal(payload, &claims); err != nil {
				t.Fatal("invalid access token claims")
			}
			if claims.Issuer != browserTestIssuer || claims.Subject != tc.subject || !reflect.DeepEqual(claims.Audience, tc.audiences) || !reflect.DeepEqual(claims.Roles, tc.roles) || claims.Scope != tc.scope || claims.Email != tc.email || claims.Verified {
				t.Fatal("access token does not carry the configured identity, audience, roles, scope or email")
			}
			if claims.ExpiresAt-claims.IssuedAt != tc.ttl || claims.ExpiresAt <= time.Now().Unix() || claims.IssuedAt > time.Now().Unix() {
				t.Fatal("access token lifetime does not match the configured TTL")
			}
			var allClaims map[string]json.RawMessage
			if err := json.Unmarshal(payload, &allClaims); err != nil {
				t.Fatal("invalid JWT claims JSON")
			}
			var namespaces []string
			if err := json.Unmarshal(allClaims[tc.namespaceClaim], &namespaces); err != nil || !reflect.DeepEqual(namespaces, tc.namespaces) {
				t.Fatal("access token does not carry the configured namespaces under the configured claim")
			}
		})
	}
}

func TestBrowserLoginAuthorizeValidation(t *testing.T) {
	handler := browserTestHandler(t)
	for _, tc := range []struct {
		name, field, value string
	}{
		{"unvetted redirect", "redirect_uri", "https://untrusted.example/callback"},
		{"redirect suffix", "redirect_uri", browserTestRedirect + "/extra"},
		{"redirect query", "redirect_uri", browserTestRedirect + "?extra=1"},
		{"wrong client", "client_id", "someone-else"},
		{"wrong response type", "response_type", "token"},
		{"missing challenge", "code_challenge", ""},
		{"short challenge", "code_challenge", "abc"},
		{"padded challenge", "code_challenge", browserTestChallenge + "="},
		{"missing method", "code_challenge_method", ""},
		{"plain PKCE", "code_challenge_method", "plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := browserTestAuthorizeQuery()
			query.Set(tc.field, tc.value)
			if tc.value == "" {
				query.Del(tc.field)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/authorize?"+query.Encode(), nil))
			if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") || response.Body.Len() == 0 {
				t.Fatal("invalid authorization must return a plain-text 400 without redirecting")
			}
		})
	}
}

func TestBrowserLoginRepeatedRedirectURIs(t *testing.T) {
	secondRedirect := "http://localhost:1738/callback?fixed=value"
	handler := browserTestHandler(t, "--login-redirect-uri", secondRedirect)
	for _, redirect := range []string{browserTestRedirect, secondRedirect} {
		query := browserTestAuthorizeQuery()
		query.Set("redirect_uri", redirect)
		code := browserTestAuthorize(t, handler, query)
		form := browserTestTokenForm(code)
		form.Set("redirect_uri", redirect)
		if response := browserTestToken(handler, form); response.Code != http.StatusOK {
			t.Fatalf("configured redirect %q failed redemption: %d", redirect, response.Code)
		}
	}
}

func TestBrowserLoginTokenRejectsAndBurnsCode(t *testing.T) {
	handler := browserTestHandler(t)
	for _, tc := range []struct {
		name, field, value, errorCode string
	}{
		{"wrong verifier", "code_verifier", strings.Repeat("a", 43), "invalid_grant"},
		{"wrong client", "client_id", "wrong-client", "invalid_grant"},
		{"wrong redirect", "redirect_uri", "http://localhost:1738/callback", "invalid_grant"},
		{"missing verifier", "code_verifier", "", "invalid_request"},
		{"unsupported grant", "grant_type", "refresh_token", "unsupported_grant_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := browserTestAuthorize(t, handler, browserTestAuthorizeQuery())
			form := browserTestTokenForm(code)
			form.Set(tc.field, tc.value)
			browserTestOAuthError(t, browserTestToken(handler, form), tc.errorCode)
			browserTestOAuthError(t, browserTestToken(handler, browserTestTokenForm(code)), "invalid_grant")
		})
	}
}

func TestBrowserLoginCodeReuse(t *testing.T) {
	handler := browserTestHandler(t)
	code := browserTestAuthorize(t, handler, browserTestAuthorizeQuery())
	form := browserTestTokenForm(code)
	if response := browserTestToken(handler, form); response.Code != http.StatusOK {
		t.Fatalf("initial redemption status = %d", response.Code)
	}
	browserTestOAuthError(t, browserTestToken(handler, form), "invalid_grant")
	browserTestOAuthError(t, browserTestToken(handler, browserTestTokenForm("unknown-code")), "invalid_grant")
}

func TestBrowserLoginExpiredCode(t *testing.T) {
	handler := browserTestHandler(t)
	now := time.Now()
	handler.(*browserLogin).now = func() time.Time { return now }
	for _, age := range []time.Duration{59 * time.Second, time.Minute, 61 * time.Second} {
		t.Run(age.String(), func(t *testing.T) {
			code := browserTestAuthorize(t, handler, browserTestAuthorizeQuery())
			now = now.Add(age)
			response := browserTestToken(handler, browserTestTokenForm(code))
			if age < time.Minute {
				if response.Code != http.StatusOK {
					t.Fatalf("unexpired code status = %d", response.Code)
				}
			} else {
				browserTestOAuthError(t, response, "invalid_grant")
			}
		})
	}
}

func TestBrowserLoginConcurrentRedemption(t *testing.T) {
	handler := browserTestHandler(t)
	code := browserTestAuthorize(t, handler, browserTestAuthorizeQuery())
	const count = 8
	responses := make(chan *httptest.ResponseRecorder, count)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			<-start
			responses <- browserTestToken(handler, browserTestTokenForm(code))
		})
	}
	close(start)
	group.Wait()
	close(responses)
	succeeded := 0
	for response := range responses {
		if response.Code == http.StatusOK {
			succeeded++
		} else {
			browserTestOAuthError(t, response, "invalid_grant")
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent successful redemptions = %d, want 1", succeeded)
	}
}

func TestBrowserLoginCORS(t *testing.T) {
	handler := browserTestHandler(t, "--login-allowed-origin", "http://127.0.0.1:5173", "--login-allowed-origin", "http://localhost:5173")
	for _, path := range []string{"/.well-known/openid-configuration", "/keys", "/token"} {
		for _, origin := range []string{"http://127.0.0.1:5173", "http://localhost:5173", "http://127.0.0.1:5174", "https://127.0.0.1:5173", "http://localhost:5173.untrusted.example", "null", ""} {
			t.Run(path+"/"+origin, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				if path == "/token" {
					request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(browserTestTokenForm("unknown").Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				request.Header.Set("Origin", origin)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				wantOrigin := ""
				if origin == "http://127.0.0.1:5173" || origin == "http://localhost:5173" {
					wantOrigin = origin
				}
				if response.Header().Get("Access-Control-Allow-Origin") != wantOrigin || response.Header().Get("Access-Control-Allow-Credentials") != "" {
					t.Fatal("CORS must use only an exact configured origin and no credentials")
				}
				if response.Header().Get("Vary") != "Origin" {
					t.Fatal("CORS responses must vary by Origin")
				}
			})
		}
	}
}

func TestBrowserLoginPreflight(t *testing.T) {
	handler := browserTestHandler(t, "--login-allowed-origin", "http://127.0.0.1:5173")
	for _, path := range []string{"/.well-known/openid-configuration", "/keys", "/token"} {
		for _, origin := range []string{"http://127.0.0.1:5173", "http://untrusted.example"} {
			t.Run(path+"/"+origin, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodOptions, path, nil)
				request.Header.Set("Origin", origin)
				request.Header.Set("Access-Control-Request-Method", "POST")
				request.Header.Set("Access-Control-Request-Headers", "content-type")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
					t.Fatalf("preflight status/body length = %d/%d", response.Code, response.Body.Len())
				}
				if origin == "http://127.0.0.1:5173" {
					if response.Header().Get("Access-Control-Allow-Origin") != origin || response.Header().Get("Access-Control-Allow-Methods") != "POST, GET" || response.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
						t.Fatal("allowed origin missing preflight CORS headers")
					}
				} else {
					for name := range response.Header() {
						if strings.HasPrefix(name, "Access-Control-") {
							t.Fatal("untrusted origin received CORS headers")
						}
					}
				}
			})
		}
	}
}

func TestBrowserLoginStartupValidation(t *testing.T) {
	for _, tc := range []struct {
		name, flag, value, want string
	}{
		{"missing key", "--key-file", "", "--key-file is required"},
		{"missing client", "--login-client-id", "", "--login-client-id is required"},
		{"missing redirect", "", "", "--login-redirect-uri is required"},
		{"relative redirect", "--login-redirect-uri", "/callback", "--login-redirect-uri must be"},
		{"redirect fragment", "--login-redirect-uri", browserTestRedirect + "#fragment", "--login-redirect-uri must be"},
		{"wildcard origin", "--login-allowed-origin", "*", "--login-allowed-origin must be"},
		{"wildcard subdomain", "--login-allowed-origin", "http://*.example.test", "--login-allowed-origin must be"},
		{"origin path", "--login-allowed-origin", "http://localhost:5173/", "--login-allowed-origin must be"},
		{"origin query", "--login-allowed-origin", "http://localhost:5173?extra=1", "--login-allowed-origin must be"},
		{"zero TTL", "--login-ttl", "0", "--login-ttl must be positive"},
		{"negative TTL", "--login-ttl", "-1s", "--login-ttl must be positive"},
		{"TTL cap", "--login-ttl", "1h1s", "--login-ttl must be positive"},
		{"empty subject", "--login-subject", "", "--login-subject, --audience and --login-namespace-claim"},
		{"empty audience", "--audience", "", "--login-subject, --audience and --login-namespace-claim"},
		{"empty namespace claim", "--login-namespace-claim", "", "--login-subject, --audience and --login-namespace-claim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--browser-login", "--key-file", filepath.Join(t.TempDir(), "issuer.json"), "--login-client-id", browserTestClientID}
			if tc.name != "missing redirect" {
				args = append(args, "--login-redirect-uri", browserTestRedirect)
			}
			if tc.flag != "" {
				args = append(args, tc.flag, tc.value)
			}
			// Construction exercises the same startup checks without opening sockets.
			if _, err := newIssuerServer(args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("startup error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBrowserLoginNonLoopbackListenRefused(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:18181", "[::]:18181", "192.168.1.10:18181", "example.test:18181", ":18181"} {
		t.Run(listen, func(t *testing.T) {
			err := runServe([]string{
				"--browser-login", "--listen", listen,
				"--key-file", filepath.Join(t.TempDir(), "issuer.json"),
				"--login-client-id", browserTestClientID, "--login-redirect-uri", browserTestRedirect,
			})
			if err == nil || !strings.Contains(err.Error(), "refusing non-loopback") {
				t.Fatalf("non-loopback serve error = %v", err)
			}
		})
	}
}
