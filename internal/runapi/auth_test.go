package runapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-logr/logr"
)

func TestOIDCAuthenticatorVerifiesGenericAndZitadelClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}}
	mux.HandleFunc("/.well-known/openid-configuration", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"issuer": server.URL, "jwks_uri": server.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(jwks)
	})

	config := DefaultConfig()
	config.OIDC.Issuer = server.URL
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.OIDC.AllowInsecureIssuer = true
	authenticator, err := NewOIDCAuthenticator(config.OIDC, config.Authorization, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := authenticator.discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	authenticator.verifier = verifier
	rawToken := signedToken(t, key, map[string]any{
		"iss":                               server.URL,
		"sub":                               "user-1",
		"aud":                               []string{"other", "anvil-agents-api"},
		"iat":                               time.Now().Add(-time.Minute).Unix(),
		"exp":                               time.Now().Add(time.Hour).Unix(),
		"email":                             "Admin@Example.com",
		"email_verified":                    true,
		"scope":                             "openid anvil-agents:runs:read",
		"groups":                            []string{"operators"},
		"roles":                             []string{"generic_viewer"},
		"urn:zitadel:iam:org:project:roles": map[string]any{"anvil_agent_read": map[string]any{}},
		"anvil_agents_namespaces":           []string{"agents"},
	})
	principal, err := authenticator.Verify(context.Background(), rawToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "user-1" || principal.Email != "Admin@Example.com" || !principal.EmailVerified {
		t.Fatalf("unexpected identity: %#v", principal)
	}
	if !intersectsStrings(principal.Roles, []string{"generic_viewer", "anvil_agent_read"}, false) || len(principal.Roles) != 2 {
		t.Fatalf("unexpected roles: %#v", principal.Roles)
	}
	if !intersectsStrings(principal.Scopes, []string{"anvil-agents:runs:read"}, false) || !intersectsStrings(principal.Namespaces, []string{"agents"}, false) {
		t.Fatalf("unexpected claims: %#v", principal)
	}
}

func TestOIDCAuthenticatorRejectsWrongAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"expected"}
	verifier := localVerifier(t, key, config.OIDC.Issuer)
	authenticator := &OIDCAuthenticator{config: config.OIDC, authz: config.Authorization, verifier: verifier}
	rawToken := signedToken(t, key, map[string]any{
		"iss": config.OIDC.Issuer,
		"sub": "user-1",
		"aud": "wrong",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := authenticator.Verify(context.Background(), rawToken); err == nil {
		t.Fatal("expected audience rejection")
	}
}

func TestOIDCAuthenticatorPreservesTrailingSlashIssuer(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	issuer := server.URL + "/tenant/"
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}}
	mux.HandleFunc("/tenant/.well-known/openid-configuration", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]string{"issuer": issuer, "jwks_uri": server.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(jwks)
	})
	config := DefaultConfig()
	config.OIDC.Issuer = issuer
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.OIDC.AllowInsecureIssuer = true
	authenticator, err := NewOIDCAuthenticator(config.OIDC, config.Authorization, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := authenticator.discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	authenticator.verifier = verifier
	rawToken := signedToken(t, key, map[string]any{
		"iss": issuer,
		"sub": "user-1",
		"aud": "anvil-agents-api",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := authenticator.Verify(context.Background(), rawToken); err != nil {
		t.Fatalf("trailing-slash issuer failed verification: %v", err)
	}
}

func TestAuthorizerRequiresBindingAndNamespace(t *testing.T) {
	authorizer := NewAuthorizer(AuthorizationConfig{Bindings: []AuthorizationBinding{
		{
			Roles:       []string{"viewer"},
			Permissions: []string{PermissionRunsRead},
			Namespaces:  []string{"agents"},
		},
		{
			Groups:              []string{"operators"},
			Permissions:         []string{PermissionRunsStream},
			NamespacesFromClaim: true,
		},
	}})
	principal := Principal{Roles: []string{"viewer"}, Groups: []string{"operators"}, Namespaces: []string{"agents"}}
	if !authorizer.Allowed(principal, PermissionRunsRead, "agents") || !authorizer.Allowed(principal, PermissionRunsStream, "agents") {
		t.Fatal("expected matching grants")
	}
	if authorizer.Allowed(principal, PermissionRunsRead, "other") {
		t.Fatal("unexpected cross-namespace grant")
	}
}

func TestAuthorizerRequiresVerifiedEmailForEmailBinding(t *testing.T) {
	authorizer := NewAuthorizer(AuthorizationConfig{Bindings: []AuthorizationBinding{{
		Emails:      []string{"viewer@example.com"},
		Permissions: []string{PermissionRunsRead},
		Namespaces:  []string{"agents"},
	}}})
	principal := Principal{Email: "viewer@example.com"}
	if authorizer.Allowed(principal, PermissionRunsRead, "agents") {
		t.Fatal("unverified email unexpectedly authorized")
	}
	principal.EmailVerified = true
	if !authorizer.Allowed(principal, PermissionRunsRead, "agents") {
		t.Fatal("verified allowlisted email was not authorized")
	}
}

func TestGenericRoleClaimDoesNotGrantFalseValuedObjectKeys(t *testing.T) {
	claims := map[string]json.RawMessage{
		"roles": json.RawMessage(`{"admin":false,"viewer":true}`),
	}
	if roles := claimValues(claims["roles"], false, false); len(roles) != 0 {
		t.Fatalf("generic object role claim granted keys: %#v", roles)
	}
	if roles := claimValues(claims["roles"], false, true); len(roles) != 2 {
		t.Fatalf("explicit object-key claim did not expose keys: %#v", roles)
	}
}

func TestOIDCTransportsBoundBodiesAndThrottleJWKSRefresh(t *testing.T) {
	requests := 0
	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 64))),
		}, nil
	})
	bounded := boundedResponseTransport{next: base, maxBytes: 10}
	response, err := bounded.RoundTrip(httptest.NewRequest(http.MethodGet, "https://issuer.example/keys", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 11 {
		t.Fatalf("expected bounded body sentinel byte, got %d", len(body))
	}

	limiter := &jwksRefreshTransport{next: base, jwksURL: "https://issuer.example/keys", minInterval: time.Hour}
	request := httptest.NewRequest(http.MethodGet, limiter.jwksURL, nil)
	if _, err := limiter.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.RoundTrip(request); err == nil {
		t.Fatal("expected repeated JWKS refresh to be rate limited")
	}
}

func signedToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key")
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, options)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := object.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func localVerifier(t *testing.T, key *rsa.PrivateKey, issuer string) *oidc.IDTokenVerifier {
	t.Helper()
	keySet := &staticKeySet{key: &key.PublicKey}
	return oidc.NewVerifier(issuer, keySet, &oidc.Config{SkipClientIDCheck: true, SupportedSigningAlgs: []string{"RS256"}})
}

type staticKeySet struct {
	key *rsa.PublicKey
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (set *staticKeySet) VerifySignature(_ context.Context, raw string) ([]byte, error) {
	object, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, err
	}
	return object.Verify(set.key)
}
