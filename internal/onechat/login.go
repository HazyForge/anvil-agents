package onechat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const notSignedIn = "not signed in; run `onechat login`"

type CloudSession struct {
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"clientId"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type loginResult struct {
	session CloudSession
	err     error
}

// LoginAttempt is the single-use callback and authorization URL. Separating the
// handler from the CLI listener permits complete protocol tests without sockets.
type LoginAttempt struct {
	AuthorizationURL string
	redirectURI      string
	callbackPath     string
	state            string
	nonce            string
	verifier         string
	issuer           string
	clientID         string
	sessionFile      string
	provider         *oidc.Provider
	client           *http.Client
	ctx              context.Context
	once             sync.Once
	done             chan loginResult
}

func authHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 30 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copyClient
}

func authURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("invalid OIDC endpoint URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
		return nil
	}
	return fmt.Errorf("OIDC endpoints must use HTTPS or loopback HTTP")
}

func authJSON(ctx context.Context, client *http.Client, method, endpoint string, form url.Values, dest any) error {
	if err := authURL(endpoint); err != nil {
		return err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("invalid sign-in request")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sign-in request failed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, outputLimit+1))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sign-in endpoint returned HTTP %d", resp.StatusCode)
	}
	if err != nil || len(raw) > outputLimit {
		return fmt.Errorf("sign-in response unreadable or exceeds limit")
	}
	if json.Unmarshal(raw, dest) != nil {
		return fmt.Errorf("invalid sign-in JSON response")
	}
	return nil
}

func discoverOIDC(ctx context.Context, client *http.Client, issuer string) (*oidc.Provider, error) {
	if err := authURL(issuer); err != nil {
		return nil, err
	}
	u, _ := url.Parse(issuer)
	if u.RawQuery != "" {
		return nil, fmt.Errorf("OIDC issuer cannot contain a query")
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery failed")
	}
	if err := authURL(provider.Endpoint().AuthURL); err != nil {
		return nil, err
	}
	if err := authURL(provider.Endpoint().TokenURL); err != nil {
		return nil, err
	}
	// Validate the signing-key endpoint too; the verifier fetches it lazily.
	var metadata struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if provider.Claims(&metadata) != nil || authURL(metadata.JWKSURI) != nil {
		return nil, fmt.Errorf("invalid OIDC signing-key endpoint")
	}
	return provider, nil
}

func randomLoginValue() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("could not generate sign-in challenge")
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func PrepareLogin(ctx context.Context, config Config, client *http.Client) (*LoginAttempt, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.Cloud.BaseURL == "" {
		return nil, fmt.Errorf("cloud.baseURL is required for login")
	}
	client = authHTTPClient(client)
	var ui struct {
		OIDC struct {
			Issuer   string          `json:"issuer"`
			ClientID string          `json:"clientId"`
			Scopes   json.RawMessage `json:"scopes"`
		} `json:"oidc"`
		Desktop struct {
			ClientID     string `json:"oidcClientId"`
			RedirectPath string `json:"oidcRedirectPath"`
		} `json:"desktop"`
	}
	if err := authJSON(ctx, client, http.MethodGet, config.Cloud.BaseURL+"/ui-config.json", nil, &ui); err != nil {
		return nil, err
	}
	clientID := strings.TrimSpace(ui.Desktop.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(ui.OIDC.ClientID)
	}
	if clientID == "" {
		return nil, fmt.Errorf("API did not advertise an OIDC client id")
	}
	var scopes []string
	if json.Unmarshal(ui.OIDC.Scopes, &scopes) != nil {
		var text string
		if json.Unmarshal(ui.OIDC.Scopes, &text) != nil {
			return nil, fmt.Errorf("API did not advertise OIDC scopes")
		}
		scopes = strings.Fields(text)
	}
	hasOpenID := false
	for _, scope := range scopes {
		if scope == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		return nil, fmt.Errorf("OIDC scopes must include openid")
	}
	path := ui.Desktop.RedirectPath
	if path == "" {
		path = "/auth/callback"
	}
	pathURL, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || pathURL.Host != "" || pathURL.RawQuery != "" || pathURL.Fragment != "" || pathURL.RawPath != "" {
		return nil, fmt.Errorf("invalid OIDC redirect path")
	}
	provider, err := discoverOIDC(ctx, client, ui.OIDC.Issuer)
	if err != nil {
		return nil, err
	}
	attempt := &LoginAttempt{issuer: ui.OIDC.Issuer, clientID: clientID, callbackPath: path, provider: provider, client: client, ctx: ctx, sessionFile: config.Cloud.SessionFile, done: make(chan loginResult, 1)}
	attempt.redirectURI = "http://127.0.0.1:" + strconv.Itoa(config.Cloud.Login.Port) + path
	if attempt.state, err = randomLoginValue(); err != nil {
		return nil, err
	}
	if attempt.nonce, err = randomLoginValue(); err != nil {
		return nil, err
	}
	if attempt.verifier, err = randomLoginValue(); err != nil {
		return nil, err
	}
	challenge := sha256.Sum256([]byte(attempt.verifier))
	authorize, _ := url.Parse(provider.Endpoint().AuthURL)
	q := authorize.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", attempt.redirectURI)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", attempt.state)
	q.Set("nonce", attempt.nonce)
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	authorize.RawQuery = q.Encode()
	attempt.AuthorizationURL = authorize.String()
	return attempt, nil
}

type tokenResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	IDToken      string      `json:"id_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
}

func exchangeToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (tokenResponse, error) {
	var tokens tokenResponse
	if err := authJSON(ctx, client, http.MethodPost, endpoint, form, &tokens); err != nil {
		return tokens, err
	}
	seconds, err := tokens.ExpiresIn.Int64()
	if err != nil || seconds <= 0 || seconds > 315360000 || tokens.AccessToken == "" || strings.ContainsAny(tokens.AccessToken, " \r\n\t") || !strings.EqualFold(tokens.TokenType, "Bearer") {
		return tokenResponse{}, fmt.Errorf("invalid token response")
	}
	return tokens, nil
}

func saveCloudSession(path string, session CloudSession) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("cannot create private cloud session directory")
	}
	if err := os.Chmod(parent, 0o700); err != nil { // #nosec G302 -- a directory needs the owner execute bit; 0700 keeps it private
		return fmt.Errorf("cannot secure cloud session directory")
	}
	if err := writeJSON(path, session); err != nil {
		return fmt.Errorf("cannot save cloud session")
	}
	return nil
}

func (a *LoginAttempt) exchange(code string) (CloudSession, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a.redirectURI}, "client_id": {a.clientID}, "code_verifier": {a.verifier}}
	tokens, err := exchangeToken(a.ctx, a.client, a.provider.Endpoint().TokenURL, form)
	if err != nil {
		return CloudSession{}, err
	}
	verified, err := a.provider.Verifier(&oidc.Config{ClientID: a.clientID}).Verify(oidc.ClientContext(a.ctx, a.client), tokens.IDToken)
	if err != nil {
		return CloudSession{}, fmt.Errorf("OIDC identity token verification failed")
	}
	if subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(a.nonce)) != 1 {
		return CloudSession{}, fmt.Errorf("OIDC nonce mismatch")
	}
	seconds, _ := tokens.ExpiresIn.Int64()
	session := CloudSession{Issuer: a.issuer, ClientID: a.clientID, AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ExpiresAt: time.Now().UTC().Add(time.Duration(seconds) * time.Second)}
	if err := saveCloudSession(a.sessionFile, session); err != nil {
		return CloudSession{}, err
	}
	return session, nil
}

func (a *LoginAttempt) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path != a.callbackPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handled := false
		a.once.Do(func() {
			handled = true
			var result loginResult
			q, parseErr := url.ParseQuery(r.URL.RawQuery)
			switch {
			case parseErr != nil:
				result.err = fmt.Errorf("invalid sign-in callback")
			case len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(a.state)) != 1:
				result.err = fmt.Errorf("sign-in state mismatch")
			case len(q["error"]) > 0:
				result.err = fmt.Errorf("sign-in provider returned an error")
			case len(q["code"]) != 1 || strings.TrimSpace(q.Get("code")) == "":
				result.err = fmt.Errorf("sign-in callback is missing a code")
			default:
				result.session, result.err = a.exchange(q.Get("code"))
			}
			message := "Signed in to one chat. You can close this tab."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if result.err != nil {
				message = result.err.Error()
				w.WriteHeader(http.StatusBadRequest)
			}
			_, _ = io.WriteString(w, "<!doctype html><html><body><p>"+html.EscapeString(message)+"</p></body></html>")
			a.done <- result
		})
		if !handled {
			http.Error(w, "sign-in callback already handled", http.StatusConflict)
		}
	})
}

func (a *LoginAttempt) finish(ctx context.Context, out io.Writer) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("sign-in canceled or timed out")
	case result := <-a.done:
		if result.err != nil {
			return result.err
		}
		_, err := fmt.Fprintf(out, "signed in, expires %s\n", result.session.ExpiresAt.Format(time.RFC3339))
		return err
	}
}

// Login opens a loopback-only listener when the operator explicitly runs login.
// Tests use PrepareLogin and Handler directly; no test starts this listener.
func Login(ctx context.Context, config Config, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	attempt, err := PrepareLogin(ctx, config, nil)
	if err != nil {
		return err
	}
	redirect, _ := url.Parse(attempt.redirectURI)
	listener, err := net.Listen("tcp4", redirect.Host)
	if err != nil {
		return fmt.Errorf("cannot listen for sign-in on %s", redirect.Host)
	}
	server := &http.Server{Handler: attempt.Handler(), ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = server.Shutdown(closeCtx)
		_ = listener.Close()
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			cancel()
		}
	}()
	if _, err := fmt.Fprintln(out, attempt.AuthorizationURL); err != nil {
		return err
	}
	return attempt.finish(ctx, out)
}

type fileTokens struct {
	path    string
	client  *http.Client
	session CloudSession
	secrets []string
}

func newFileTokens(path string, client *http.Client) (*fileTokens, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New(notSignedIn)
	}
	path, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New(notSignedIn)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read cloud session")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("cloud session must be a regular file with mode 0600")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read cloud session")
	}
	source := &fileTokens{path: path, client: authHTTPClient(client)}
	if json.Unmarshal(raw, &source.session) != nil || source.session.ClientID == "" || source.session.AccessToken == "" || source.session.ExpiresAt.IsZero() || authURL(source.session.Issuer) != nil {
		return nil, fmt.Errorf("invalid cloud session; run `onechat login`")
	}
	source.remember()
	return source, nil
}

func (f *fileTokens) remember() {
	for _, token := range []string{f.session.AccessToken, f.session.RefreshToken} {
		if token == "" {
			continue
		}
		exists := false
		for _, old := range f.secrets {
			if old == token {
				exists = true
				break
			}
		}
		if !exists {
			f.secrets = append(f.secrets, token)
		}
	}
}

func (f *fileTokens) Token(ctx context.Context, force bool) (string, error) {
	if !force && time.Until(f.session.ExpiresAt) > 2*time.Minute {
		return f.session.AccessToken, nil
	}
	if f.session.RefreshToken == "" {
		return "", fmt.Errorf("cloud session cannot refresh; run `onechat login`")
	}
	provider, err := discoverOIDC(ctx, f.client, f.session.Issuer)
	if err != nil {
		return "", err
	}
	tokens, err := exchangeToken(ctx, f.client, provider.Endpoint().TokenURL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {f.session.RefreshToken}, "client_id": {f.session.ClientID}})
	if err != nil {
		return "", fmt.Errorf("cloud session refresh failed; run `onechat login`")
	}
	next := f.session
	next.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		next.RefreshToken = tokens.RefreshToken
	}
	seconds, _ := tokens.ExpiresIn.Int64()
	next.ExpiresAt = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	// Remember rotated credentials even if disk persistence fails, so they cannot
	// appear in an error or trace if a later backend response echoes them.
	f.session = next
	f.remember()
	if err := saveCloudSession(f.path, next); err != nil {
		return "", err
	}
	return next.AccessToken, nil
}

func (f *fileTokens) Redact(text string) string {
	for _, token := range f.secrets {
		text = strings.ReplaceAll(text, token, "[REDACTED]")
	}
	return text
}
