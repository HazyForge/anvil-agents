package runapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-logr/logr"
)

var (
	ErrAuthenticationUnavailable = errors.New("OIDC authentication is unavailable")
	ErrInvalidAccessToken        = errors.New("invalid access token")
)

type Principal struct {
	Subject       string
	Issuer        string
	Email         string
	EmailVerified bool
	Roles         []string
	Groups        []string
	Scopes        []string
	Namespaces    []string
	Expiry        time.Time
}

type AccessTokenAuthenticator interface {
	Verify(context.Context, string) (Principal, error)
	Ready() bool
}

type OIDCAuthenticator struct {
	config OIDCConfig
	authz  AuthorizationConfig
	log    logr.Logger
	client *http.Client

	mu       sync.RWMutex
	verifier *oidc.IDTokenVerifier
	lastErr  error
}

type oidcDiscoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

const (
	maxOIDCResponseBytes = 2 * 1024 * 1024
	minJWKSRefreshPeriod = time.Second
)

func NewOIDCAuthenticator(config OIDCConfig, authz AuthorizationConfig, log logr.Logger) (*OIDCAuthenticator, error) {
	client, err := oidcHTTPClient(config)
	if err != nil {
		return nil, err
	}
	return &OIDCAuthenticator{config: config, authz: authz, log: log, client: client}, nil
}

func (authenticator *OIDCAuthenticator) Start(ctx context.Context) {
	if authenticator == nil {
		return
	}
	delay := time.Duration(0)
	for {
		if !sleepContext(ctx, delay) {
			return
		}
		verifier, err := authenticator.discover(ctx)
		authenticator.mu.Lock()
		if err == nil {
			authenticator.verifier = verifier
			authenticator.lastErr = nil
		} else {
			authenticator.lastErr = err
		}
		hasVerifier := authenticator.verifier != nil
		authenticator.mu.Unlock()
		if err != nil {
			authenticator.log.Error(err, "OIDC discovery failed", "usingLastGoodVerifier", hasVerifier)
			delay = authenticator.config.DiscoveryRetryInterval.Duration
			continue
		}
		authenticator.log.Info("OIDC verifier ready", "issuer", authenticator.config.Issuer)
		delay = authenticator.config.DiscoveryRefreshInterval.Duration
	}
}

func (authenticator *OIDCAuthenticator) Ready() bool {
	if authenticator == nil {
		return false
	}
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	return authenticator.verifier != nil
}

func (authenticator *OIDCAuthenticator) LastError() error {
	if authenticator == nil {
		return ErrAuthenticationUnavailable
	}
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	return authenticator.lastErr
}

func (authenticator *OIDCAuthenticator) Verify(ctx context.Context, rawToken string) (Principal, error) {
	if authenticator == nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	authenticator.mu.RLock()
	verifier := authenticator.verifier
	authenticator.mu.RUnlock()
	if verifier == nil {
		return Principal{}, ErrAuthenticationUnavailable
	}
	token, err := verifier.Verify(ctx, strings.TrimSpace(rawToken))
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidAccessToken, err)
	}
	if !intersectsStrings(authenticator.config.Audiences, token.Audience, false) {
		return Principal{}, fmt.Errorf("%w: token audience is not accepted", ErrInvalidAccessToken)
	}
	if strings.TrimSpace(token.Subject) == "" {
		return Principal{}, fmt.Errorf("%w: token subject is missing", ErrInvalidAccessToken)
	}

	claims := map[string]json.RawMessage{}
	if err := token.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: decode claims: %v", ErrInvalidAccessToken, err)
	}
	principal := Principal{
		Subject:       strings.TrimSpace(token.Subject),
		Issuer:        strings.TrimSpace(token.Issuer),
		Email:         strings.TrimSpace(firstClaimString(claims, "email")),
		EmailVerified: firstClaimBool(claims, "email_verified"),
		Expiry:        token.Expiry,
	}
	for _, claim := range authenticator.authz.RoleClaims {
		principal.Roles = append(principal.Roles, claimValues(claims[claim], false, false)...)
	}
	for _, claim := range authenticator.authz.RoleObjectClaims {
		principal.Roles = append(principal.Roles, claimValues(claims[claim], false, true)...)
	}
	for _, claim := range authenticator.authz.GroupClaims {
		principal.Groups = append(principal.Groups, claimValues(claims[claim], false, false)...)
	}
	for _, claim := range authenticator.authz.ScopeClaims {
		principal.Scopes = append(principal.Scopes, claimValues(claims[claim], true, false)...)
	}
	if claim := authenticator.authz.NamespaceClaim; claim != "" {
		principal.Namespaces = claimValues(claims[claim], false, false)
	}
	principal.Roles = uniqueStrings(principal.Roles, false)
	principal.Groups = uniqueStrings(principal.Groups, false)
	principal.Scopes = uniqueStrings(principal.Scopes, false)
	principal.Namespaces = uniqueStrings(principal.Namespaces, false)
	return principal, nil
}

func (authenticator *OIDCAuthenticator) discover(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	discoveryURL := strings.TrimRight(authenticator.config.Issuer, "/") + "/.well-known/openid-configuration"
	requestCtx, cancel := context.WithTimeout(ctx, authenticator.config.DiscoveryRequestTimeout.Duration)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create discovery request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := authenticator.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request OIDC discovery: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery returned HTTP %d", response.StatusCode)
	}
	var document oidcDiscoveryDocument
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024*1024))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode OIDC discovery: %w", err)
	}
	if strings.TrimSpace(document.Issuer) != authenticator.config.Issuer {
		return nil, fmt.Errorf("OIDC discovery issuer %q does not match configured issuer", document.Issuer)
	}
	if err := authenticator.validateJWKSURL(document.JWKSURI); err != nil {
		return nil, err
	}
	keyContext := oidc.ClientContext(context.Background(), authenticator.jwksClient(document.JWKSURI))
	keySet := oidc.NewRemoteKeySet(keyContext, document.JWKSURI)
	return oidc.NewVerifier(authenticator.config.Issuer, keySet, &oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: append([]string(nil), authenticator.config.AllowedSigningAlgorithms...),
	}), nil
}

func (authenticator *OIDCAuthenticator) validateJWKSURL(raw string) error {
	jwksURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || jwksURL.Host == "" || jwksURL.RawQuery != "" || jwksURL.Fragment != "" {
		return fmt.Errorf("OIDC discovery returned an invalid jwks_uri")
	}
	if jwksURL.Scheme != "https" && !(authenticator.config.AllowInsecureIssuer && jwksURL.Scheme == "http") {
		return fmt.Errorf("OIDC jwks_uri must use HTTPS")
	}
	issuerURL, _ := url.Parse(authenticator.config.Issuer)
	allowedHosts := append([]string{strings.ToLower(issuerURL.Hostname())}, authenticator.config.AdditionalJWKSHosts...)
	if !slices.Contains(allowedHosts, strings.ToLower(jwksURL.Hostname())) {
		return fmt.Errorf("OIDC jwks_uri host %q is not the issuer host or an allowed additional host", jwksURL.Hostname())
	}
	return nil
}

func oidcHTTPClient(config OIDCConfig) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("OIDC CA file contains no valid certificates")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	return &http.Client{
		Transport: boundedResponseTransport{next: transport, maxBytes: maxOIDCResponseBytes},
		Timeout:   config.DiscoveryRequestTimeout.Duration,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func (authenticator *OIDCAuthenticator) jwksClient(jwksURL string) *http.Client {
	copy := *authenticator.client
	copy.Transport = &jwksRefreshTransport{
		next:        authenticator.client.Transport,
		jwksURL:     jwksURL,
		minInterval: minJWKSRefreshPeriod,
	}
	return &copy
}

type boundedResponseTransport struct {
	next     http.RoundTripper
	maxBytes int64
}

func (transport boundedResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.LimitReader(response.Body, transport.maxBytes+1), Closer: response.Body}
	return response, nil
}

type jwksRefreshTransport struct {
	next        http.RoundTripper
	jwksURL     string
	minInterval time.Duration
	mu          sync.Mutex
	lastRequest time.Time
}

func (transport *jwksRefreshTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() == transport.jwksURL {
		transport.mu.Lock()
		if !transport.lastRequest.IsZero() && time.Since(transport.lastRequest) < transport.minInterval {
			transport.mu.Unlock()
			return nil, fmt.Errorf("JWKS refresh is rate limited")
		}
		transport.lastRequest = time.Now()
		transport.mu.Unlock()
	}
	return transport.next.RoundTrip(request)
}

type Authorizer struct {
	config AuthorizationConfig
}

func NewAuthorizer(config AuthorizationConfig) Authorizer {
	return Authorizer{config: config}
}

func (authorizer Authorizer) Allowed(principal Principal, permission, namespace string) bool {
	for _, binding := range authorizer.config.Bindings {
		if !bindingMatchesPrincipal(binding, principal) || !slices.Contains(binding.Permissions, permission) {
			continue
		}
		if slices.Contains(binding.Namespaces, "*") || slices.Contains(binding.Namespaces, namespace) {
			return true
		}
		if binding.NamespacesFromClaim && (slices.Contains(principal.Namespaces, "*") || slices.Contains(principal.Namespaces, namespace)) {
			return true
		}
	}
	return false
}

func bindingMatchesPrincipal(binding AuthorizationBinding, principal Principal) bool {
	if len(binding.Roles) > 0 && !intersectsStrings(binding.Roles, principal.Roles, false) {
		return false
	}
	if len(binding.Groups) > 0 && !intersectsStrings(binding.Groups, principal.Groups, false) {
		return false
	}
	if len(binding.Subjects) > 0 && !slices.Contains(binding.Subjects, principal.Subject) {
		return false
	}
	if len(binding.Emails) > 0 {
		if !principal.EmailVerified || !slices.Contains(binding.Emails, strings.ToLower(principal.Email)) {
			return false
		}
	}
	if len(binding.Scopes) > 0 && !intersectsStrings(binding.Scopes, principal.Scopes, false) {
		return false
	}
	return true
}

func claimValues(raw json.RawMessage, splitSpace, objectKeys bool) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if splitSpace {
			return strings.Fields(value)
		}
		return []string{value}
	}
	if objectKeys {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err == nil {
			values = make([]string, 0, len(object))
			for key := range object {
				values = append(values, key)
			}
			return values
		}
	}
	return nil
}

func firstClaimString(claims map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(claims[name], &value)
	return value
}

func firstClaimBool(claims map[string]json.RawMessage, name string) bool {
	var value bool
	_ = json.Unmarshal(claims[name], &value)
	return value
}

func intersectsStrings(expected, actual []string, lower bool) bool {
	for _, expectedValue := range expected {
		if lower {
			expectedValue = strings.ToLower(expectedValue)
		}
		for _, actualValue := range actual {
			if lower {
				actualValue = strings.ToLower(actualValue)
			}
			if expectedValue == actualValue {
				return true
			}
		}
	}
	return false
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
