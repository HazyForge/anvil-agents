package onechat

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type loginFixture struct {
	t              *testing.T
	key            *rsa.PrivateKey
	nonce          string
	clientID       string
	desktopClient  string
	redirectPath   string
	tokenForms     []url.Values
	refreshes      int
	apiCalls       int
	rejectFirstAPI bool
	badNonce       bool
	badAudience    bool
	failToken      bool
}

func newLoginFixture(t *testing.T) *loginFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &loginFixture{t: t, key: key, clientID: "console-client", desktopClient: "native-client", redirectPath: "/auth/callback"}
}

func (f *loginFixture) signedID() string {
	client := f.desktopClient
	if client == "" {
		client = f.clientID
	}
	if f.badAudience {
		client = "wrong-client"
	}
	nonce := f.nonce
	if f.badNonce {
		nonce = "wrong-nonce"
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key"})
	claims, _ := json.Marshal(map[string]any{"iss": "https://issuer.invalid", "aud": client, "sub": "test-person", "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (f *loginFixture) client() *http.Client {
	return &http.Client{Transport: fakeTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/ui-config.json":
			if req.Header.Get("Authorization") != "" {
				f.t.Fatal("public discovery received bearer")
			}
			return response(200, map[string]any{"oidc": map[string]any{"issuer": "https://issuer.invalid", "clientId": f.clientID, "scopes": []string{"openid", "profile", "email", "offline_access", "test-audience"}}, "desktop": map[string]string{"oidcClientId": f.desktopClient, "oidcRedirectPath": f.redirectPath}}), nil
		case "/.well-known/openid-configuration":
			return response(200, map[string]any{"issuer": "https://issuer.invalid", "authorization_endpoint": "https://issuer.invalid/authorize", "token_endpoint": "https://issuer.invalid/token", "jwks_uri": "https://issuer.invalid/keys", "response_types_supported": []string{"code"}, "id_token_signing_alg_values_supported": []string{"RS256"}}), nil
		case "/keys":
			return response(200, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.PublicKey.E)).Bytes())}}}), nil
		case "/token":
			if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				f.t.Fatal("token request is not form-encoded POST")
			}
			if err := req.ParseForm(); err != nil {
				f.t.Fatal(err)
			}
			f.tokenForms = append(f.tokenForms, req.PostForm)
			if f.failToken {
				return response(400, map[string]string{"error": "fake-secret-that-must-not-be-printed"}), nil
			}
			if req.PostForm.Get("grant_type") == "refresh_token" {
				f.refreshes++
				return response(200, map[string]any{"access_token": "fake-access-rotated", "refresh_token": "fake-refresh-rotated", "token_type": "Bearer", "expires_in": 3600}), nil
			}
			return response(200, map[string]any{"access_token": "fake-access-initial", "refresh_token": "fake-refresh-initial", "token_type": "Bearer", "expires_in": 3600, "id_token": f.signedID()}), nil
		case "/api/v1/namespaces/agents/chat/threads":
			f.apiCalls++
			if f.rejectFirstAPI && f.apiCalls == 1 {
				if req.Header.Get("Authorization") != "Bearer fake-access-original" {
					f.t.Fatal("first request did not use existing session")
				}
				return response(401, map[string]string{"error": "expired"}), nil
			}
			if req.Header.Get("Authorization") != "Bearer fake-access-rotated" {
				f.t.Fatal("request did not use refreshed credential")
			}
			return response(200, map[string]any{"items": []any{}}), nil
		default:
			f.t.Fatalf("unexpected sign-in endpoint: %s", req.URL.Path)
			return nil, io.EOF
		}
	})}
}

func loginConfig(t *testing.T) Config {
	t.Helper()
	c := testConfig(t)
	c.Cloud.BaseURL = "https://api.invalid"
	c.Cloud.Namespaces = []string{"agents"}
	c.Cloud.SessionFile = filepath.Join(c.StateDir, "auth", "cloud-session.json")
	return c
}

func callbackRequest(a *LoginAttempt, query url.Values) *http.Request {
	return httptest.NewRequest(http.MethodGet, a.redirectURI+"?"+query.Encode(), nil)
}

func TestOneChatLoginAuthorizeCallbackAndSession(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "desktop", true: "fallback"}[fallback], func(t *testing.T) {
			fixture := newLoginFixture(t)
			config := loginConfig(t)
			if fallback {
				fixture.desktopClient = ""
				fixture.redirectPath = ""
			} else {
				config.Cloud.Login.Port = 1740
				fixture.redirectPath = "/native/callback"
			}
			if err := os.MkdirAll(filepath.Dir(config.Cloud.SessionFile), 0o755); err != nil {
				t.Fatal(err)
			}
			attempt, err := PrepareLogin(context.Background(), config, fixture.client())
			if err != nil {
				t.Fatal(err)
			}
			authorize, err := url.Parse(attempt.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			q := authorize.Query()
			wantClient, wantRedirect := "native-client", "http://127.0.0.1:1740/native/callback"
			if fallback {
				wantClient, wantRedirect = "console-client", "http://127.0.0.1:1738/auth/callback"
			}
			if authorize.Scheme != "https" || authorize.Host != "issuer.invalid" || authorize.Path != "/authorize" || q.Get("client_id") != wantClient || q.Get("redirect_uri") != wantRedirect || q.Get("response_type") != "code" || q.Get("scope") != "openid profile email offline_access test-audience" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("state") == q.Get("nonce") || q.Get("code_challenge_method") != "S256" {
				t.Fatalf("incorrect authorize parameters: %v", q)
			}
			fixture.nonce = q.Get("nonce")
			recorder := httptest.NewRecorder()
			attempt.Handler().ServeHTTP(recorder, callbackRequest(attempt, url.Values{"state": {q.Get("state")}, "code": {"approved-code"}}))
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Signed in to one chat. You can close this tab.") {
				t.Fatalf("callback %d: %s", recorder.Code, recorder.Body.String())
			}
			var output bytes.Buffer
			output.WriteString(attempt.AuthorizationURL + "\n")
			if err := attempt.finish(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			if len(fixture.tokenForms) != 1 {
				t.Fatal("expected exactly one code exchange")
			}
			form := fixture.tokenForms[0]
			digest := sha256.Sum256([]byte(form.Get("code_verifier")))
			if form.Get("grant_type") != "authorization_code" || form.Get("code") != "approved-code" || form.Get("client_id") != wantClient || form.Get("redirect_uri") != wantRedirect || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || len(form.Get("code_verifier")) < 43 {
				t.Fatal("code exchange does not match PKCE authorization")
			}
			raw, err := os.ReadFile(config.Cloud.SessionFile)
			if err != nil {
				t.Fatal(err)
			}
			var stored CloudSession
			if json.Unmarshal(raw, &stored) != nil || stored.Issuer != "https://issuer.invalid" || stored.ClientID != wantClient || stored.AccessToken != "fake-access-initial" || stored.RefreshToken != "fake-refresh-initial" || time.Until(stored.ExpiresAt) < 59*time.Minute {
				t.Fatal("stored session is incomplete")
			}
			for path, mode := range map[string]os.FileMode{config.Cloud.SessionFile: 0o600, filepath.Dir(config.Cloud.SessionFile): 0o700} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("incorrect private permissions for %s: %v", path, err)
				}
			}
			if lines := strings.Split(strings.TrimSpace(output.String()), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "signed in, expires ") {
				t.Fatalf("unexpected login output: %s", output.String())
			}
			for _, secret := range []string{stored.AccessToken, stored.RefreshToken} {
				if strings.Contains(output.String()+recorder.Body.String(), secret) {
					t.Fatal("token leaked into login output")
				}
			}
			again := httptest.NewRecorder()
			attempt.Handler().ServeHTTP(again, callbackRequest(attempt, url.Values{"state": {q.Get("state")}, "code": {"approved-code"}}))
			if again.Code != http.StatusConflict || len(fixture.tokenForms) != 1 {
				t.Fatal("callback was exchanged more than once")
			}
		})
	}
}

func TestOneChatLoginRejectsInvalidCallbacks(t *testing.T) {
	for _, kind := range []string{"state", "error", "missing-code", "nonce", "audience", "token-error"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newLoginFixture(t)
			config := loginConfig(t)
			attempt, err := PrepareLogin(context.Background(), config, fixture.client())
			if err != nil {
				t.Fatal(err)
			}
			q := url.Values{"state": {attempt.state}, "code": {"approved-code"}}
			fixture.nonce = attempt.nonce
			switch kind {
			case "state":
				q.Set("state", "wrong-state")
			case "error":
				q.Set("error", "fake-secret-that-must-not-be-printed")
			case "missing-code":
				q.Del("code")
			case "nonce":
				fixture.badNonce = true
			case "audience":
				fixture.badAudience = true
			case "token-error":
				fixture.failToken = true
			}
			recorder := httptest.NewRecorder()
			attempt.Handler().ServeHTTP(recorder, callbackRequest(attempt, q))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("accepted %s callback", kind)
			}
			var output bytes.Buffer
			err = attempt.finish(context.Background(), &output)
			if err == nil {
				t.Fatal("invalid callback did not fail login")
			}
			if _, err := os.Stat(config.Cloud.SessionFile); !os.IsNotExist(err) {
				t.Fatal("invalid callback persisted a session")
			}
			if strings.Contains(output.String()+recorder.Body.String()+err.Error(), "fake-secret-that-must-not-be-printed") || strings.Contains(recorder.Body.String(), "fake-access-initial") {
				t.Fatal("provider data leaked into error output")
			}
			if (kind == "state" || kind == "error" || kind == "missing-code") && len(fixture.tokenForms) != 0 {
				t.Fatal("invalid callback reached token endpoint")
			}
		})
	}
}

func TestOneChatLoginRefreshOnExpiryAnd401(t *testing.T) {
	for _, mode := range []string{"expiry-at-start", "expiry-before-request", "401"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newLoginFixture(t)
			config := loginConfig(t)
			expires := time.Now().UTC().Add(time.Hour)
			if mode == "expiry-at-start" {
				expires = time.Now().UTC().Add(time.Minute)
			}
			fixture.rejectFirstAPI = mode == "401"
			original := CloudSession{Issuer: "https://issuer.invalid", ClientID: "native-client", AccessToken: "fake-access-original", RefreshToken: "fake-refresh-original", ExpiresAt: expires}
			if err := saveCloudSession(config.Cloud.SessionFile, original); err != nil {
				t.Fatal(err)
			}
			backend, err := NewCloudBackend(context.Background(), config.Cloud, time.Second, fixture.client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expiry-before-request" {
				backend.sessionTokens.session.ExpiresAt = time.Now().Add(time.Minute)
			}
			if _, err := backend.List(context.Background()); err != nil {
				t.Fatal(err)
			}
			if fixture.refreshes != 1 || len(fixture.tokenForms) != 1 {
				t.Fatalf("unexpected refresh count %d", fixture.refreshes)
			}
			form := fixture.tokenForms[0]
			if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "fake-refresh-original" || form.Get("client_id") != "native-client" || form.Get("code_verifier") != "" {
				t.Fatal("incorrect refresh grant")
			}
			raw, err := os.ReadFile(config.Cloud.SessionFile)
			if err != nil {
				t.Fatal(err)
			}
			var stored CloudSession
			if json.Unmarshal(raw, &stored) != nil || stored.AccessToken != "fake-access-rotated" || stored.RefreshToken != "fake-refresh-rotated" || time.Until(stored.ExpiresAt) < 59*time.Minute {
				t.Fatal("refresh was not persisted")
			}
			secrets := "fake-access-original fake-refresh-original fake-access-rotated fake-refresh-rotated"
			decider := &fakeDecider{decide: func(DecideInput) (Decision, error) {
				return Decision{ReasoningSummary: "No work needed.", Confidence: 1, Actions: []Action{{Type: "answer", Text: secrets}}}, nil
			}, compose: func(ComposeInput) (string, error) { return secrets, nil }}
			manager, err := NewManager(config, decider, &fakeBackend{}, backend)
			if err != nil {
				t.Fatal(err)
			}
			trace, err := manager.Turn(context.Background(), "show sessions")
			if err != nil {
				t.Fatal(err)
			}
			evidence, _ := json.Marshal(trace)
			for _, token := range strings.Fields(secrets) {
				if strings.Contains(string(evidence), token) {
					t.Fatal("credential entered trace/output")
				}
			}
			for _, path := range []string{trace.TracePath, filepath.Join(config.StateDir, "onechat.json")} {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, token := range strings.Fields(secrets) {
					if strings.Contains(string(raw), token) {
						t.Fatal("credential entered persisted trace/history")
					}
				}
			}
		})
	}
}

func TestOneChatLoginMissingSessionAndHomeExpansion(t *testing.T) {
	config := loginConfig(t)
	_, err := NewCloudBackend(context.Background(), config.Cloud, time.Second, &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("missing session made a network request")
		return nil, io.EOF
	})}, nil)
	if err == nil || err.Error() != notSignedIn {
		t.Fatalf("missing session error: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	config.Cloud.SessionFile = "~/.config/onechat/cloud-session.json"
	config.StateDir = "~/state"
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.Cloud.SessionFile != filepath.Join(home, ".config", "onechat", "cloud-session.json") || config.StateDir != filepath.Join(home, "state") {
		t.Fatal("home paths not expanded")
	}
}

func TestOneChatLoginScenarioUsesSharedSessionFile(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "state-default", true: "explicit-path"}[explicit], func(t *testing.T) {
			config := loginConfig(t)
			if !explicit {
				config.Cloud.SessionFile = ""
			}
			wanted := config.Cloud.SessionFile
			if wanted == "" {
				wanted = filepath.Join(config.StateDir, "cloud-session.json")
			}
			captured := ""
			report, err := RunScenario(context.Background(), config, Scenario{Name: "auth-scope"}, t.TempDir(), func(_ context.Context, c Config) (*Manager, error) {
				captured = c.Cloud.SessionFile
				return NewManager(c, &fakeDecider{}, &fakeBackend{}, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			if captured != wanted || report.StateDir == config.StateDir || strings.HasPrefix(captured, report.StateDir+string(filepath.Separator)) {
				t.Fatal("scenario lost the person's shared login file")
			}
		})
	}
}
