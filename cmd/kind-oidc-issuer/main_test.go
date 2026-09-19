package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-logr/logr"

	"github.com/hazyforge/anvil-agents/internal/runapi"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestDiscoveryAndJWKSShapes(t *testing.T) {
	key := testKey(t)
	issuer := "http://127.0.0.1:18081"
	server := httptest.NewServer(newIssuerHandler(issuer, publicJWK(&key.PublicKey)))
	defer server.Close()

	response, err := http.Get(server.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var document struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.Issuer != issuer {
		t.Fatalf("issuer = %q, want %q", document.Issuer, issuer)
	}
	if document.JWKSURI != issuer+"/keys" {
		t.Fatalf("jwks_uri = %q, want %q", document.JWKSURI, issuer+"/keys")
	}

	keysResponse, err := http.Get(server.URL + "/keys")
	if err != nil {
		t.Fatal(err)
	}
	defer keysResponse.Body.Close()
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(keysResponse.Body).Decode(&set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 || set.Keys[0].KeyID != issuerKeyID || !set.Keys[0].Valid() {
		t.Fatalf("unexpected JWKS: %+v", set.Keys)
	}
}

// TestMintedTokenVerifiesThroughRealAuthenticator is the composition proof
// for the Kind-local story: a token minted by this helper verifies through
// the production OIDCAuthenticator with allowInsecureIssuer, and the example
// role binding authorizes chat write in the agents namespace.
func TestMintedTokenVerifiesThroughRealAuthenticator(t *testing.T) {
	key := testKey(t)
	// Build the handler once the httptest URL is known so discovery serves
	// the exact issuer the API is configured with.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		newIssuerHandler(server.URL, publicJWK(&key.PublicKey)).ServeHTTP(writer, request)
	}))
	defer server.Close()

	config := runapi.DefaultConfig()
	config.OIDC.Issuer = server.URL
	config.OIDC.Audiences = []string{"anvil-agents"}
	config.OIDC.AllowInsecureIssuer = true
	config.Authorization.Bindings = []runapi.AuthorizationBinding{{
		Name:        "kind-local-desktop",
		Roles:       []string{"kind-local-desktop"},
		Permissions: []string{runapi.PermissionChatRead, runapi.PermissionChatWrite},
		Namespaces:  []string{"agents"},
	}}
	authenticator, err := runapi.NewOIDCAuthenticator(config.OIDC, config.Authorization, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go authenticator.Start(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for !authenticator.Ready() {
		if time.Now().After(deadline) {
			t.Fatalf("authenticator never became ready: %v", authenticator.LastError())
		}
		time.Sleep(50 * time.Millisecond)
	}

	raw, err := mintToken(key, mintOptions{
		issuer:         server.URL,
		audiences:      []string{"anvil-agents"},
		subject:        "kind-local-desktop",
		ttl:            15 * time.Minute,
		roles:          []string{"kind-local-desktop"},
		namespaces:     []string{"agents"},
		namespaceClaim: "anvil_agents_namespaces",
		scope:          "openid profile email",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := authenticator.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("minted token did not verify: %v", err)
	}
	if principal.Subject != "kind-local-desktop" || principal.Issuer != server.URL {
		t.Fatalf("unexpected principal: %#v", principal)
	}
	authorizer := runapi.NewAuthorizer(config.Authorization)
	if !authorizer.Allowed(principal, runapi.PermissionChatWrite, "agents") {
		t.Fatalf("example binding did not authorize chat write: %#v", principal)
	}
	if authorizer.Allowed(principal, runapi.PermissionChatWrite, "other") {
		t.Fatal("example binding unexpectedly authorized another namespace")
	}

	wrongAudience, err := mintToken(key, mintOptions{
		issuer:         server.URL,
		audiences:      []string{"someone-else"},
		subject:        "kind-local-desktop",
		ttl:            15 * time.Minute,
		namespaceClaim: "anvil_agents_namespaces",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.Verify(context.Background(), wrongAudience); err == nil {
		t.Fatal("wrong-audience token unexpectedly verified")
	}
}

func TestServeRefusesNonLoopbackListen(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:18081", "192.168.1.10:18081", "[::]:18081", "example.com:18081", "no-port", ":18081"} {
		if _, _, err := ensureLoopbackListen(listen); err == nil {
			t.Fatalf("listen %q unexpectedly accepted", listen)
		}
	}
	for _, listen := range []string{"127.0.0.1:18081", "127.0.0.2:18081", "localhost:18081", "[::1]:18081"} {
		if _, _, err := ensureLoopbackListen(listen); err != nil {
			t.Fatalf("listen %q unexpectedly refused: %v", listen, err)
		}
	}
}

func TestValidateIssuerURL(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:18081", "https://identity.example.com", "https://identity.example.com/tenant/"} {
		if err := validateIssuerURL(raw); err != nil {
			t.Fatalf("issuer %q unexpectedly refused: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "not-a-url", "http://127.0.0.1:18081?x=1", "http://127.0.0.1:18081#frag", "ftp://host/x"} {
		if err := validateIssuerURL(raw); err == nil {
			t.Fatalf("issuer %q unexpectedly accepted", raw)
		}
	}
}

func TestMintTokenGuards(t *testing.T) {
	key := testKey(t)
	base := mintOptions{
		issuer:         "http://127.0.0.1:18081",
		audiences:      []string{"anvil-agents"},
		subject:        "kind-local-desktop",
		ttl:            15 * time.Minute,
		namespaceClaim: "anvil_agents_namespaces",
	}
	if _, err := mintToken(nil, base); err == nil {
		t.Fatal("nil key unexpectedly accepted")
	}
	withoutSubject := base
	withoutSubject.subject = ""
	if _, err := mintToken(key, withoutSubject); err == nil {
		t.Fatal("empty subject unexpectedly accepted")
	}
	withoutAudience := base
	withoutAudience.audiences = nil
	if _, err := mintToken(key, withoutAudience); err == nil {
		t.Fatal("empty audience unexpectedly accepted")
	}
	if _, err := mintToken(key, base); err != nil {
		t.Fatalf("valid mint failed: %v", err)
	}
}

func TestMintTTLCapped(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "kind-oidc.key.json")
	if _, _, err := loadOrGenerateKey(keyFile); err != nil {
		t.Fatal(err)
	}
	err := runMint([]string{"--key-file", keyFile, "--ttl", "2h"})
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("expected TTL cap error, got %v", err)
	}
	if err := runMint([]string{"--ttl", "15m"}); err == nil || !strings.Contains(err.Error(), "--key-file is required") {
		t.Fatalf("expected key-file error, got %v", err)
	}
}

func TestKeyFileRoundTripIsPrivate(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "kind-oidc.key.json")
	key, _, err := loadOrGenerateKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("key file perm = %o, want no group/other access", perm)
	}
	loaded, err := loadKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.D.Cmp(key.D) != 0 {
		t.Fatal("reloaded key does not match generated key")
	}
	if _, _, err := loadOrGenerateKey(keyFile); err != nil {
		t.Fatalf("existing key file was not reused: %v", err)
	}
	if err := os.WriteFile(keyFile, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadKey(keyFile); err == nil {
		t.Fatal("corrupt key file unexpectedly accepted")
	}
}
