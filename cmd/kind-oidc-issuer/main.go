// Command kind-oidc-issuer is a minimal loopback-only OIDC test issuer for
// Kind-local Desktop standing-chat latency work. It is NOT a production IdP
// and must never be exposed: serve refuses non-loopback listen addresses,
// minted tokens are TTL-capped, and provider selection stays
// issuer/audience/client-id only (see docs/desktop.md).
//
// Serve discovery + JWKS for a loopback issuer:
//
//	kind-oidc-issuer --listen 127.0.0.1:18081 --key-file /tmp/kind-oidc.key.json
//
// Mint a signed RS256 access token against the same key (printed to stdout,
// nothing else):
//
//	kind-oidc-issuer mint --key-file /tmp/kind-oidc.key.json \
//	  --issuer http://127.0.0.1:18081 --audience anvil-agents \
//	  --subject kind-local-desktop --roles kind-local-desktop \
//	  --namespaces agents
//
// Configure the API with the same issuer + audience, allowInsecureIssuer for
// the http loopback issuer, and an explicit authorization binding; see
// examples/live-api/kind-local-api-config.yaml and
// docs/standing-inprocess-harness.md.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	defaultListenAddress = "127.0.0.1:18081"
	defaultIssuer        = "http://127.0.0.1:18081"
	defaultAudience      = "anvil-agents"
	defaultSubject       = "kind-local-desktop"
	issuerKeyID          = "kind-local-1"
	rsaKeyBits           = 2048
	maxMintTTL           = time.Hour
)

type mintOptions struct {
	issuer         string
	audiences      []string
	subject        string
	ttl            time.Duration
	roles          []string
	groups         []string
	namespaces     []string
	namespaceClaim string
	email          string
	emailVerified  bool
	scope          string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mint" {
		if err := runMint(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kind-oidc-issuer mint: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := runServe(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "kind-oidc-issuer: %v\n", err)
		os.Exit(1)
	}
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("kind-oidc-issuer", flag.ContinueOnError)
	listen := flags.String("listen", defaultListenAddress, "Loopback listen address (non-loopback addresses are refused).")
	keyFile := flags.String("key-file", "", "Persist the signing key to this path (0600) so mint can reuse it. Empty keeps an ephemeral in-memory key.")
	issuerOverride := flags.String("issuer", "", "Advertised issuer URL. Defaults to http://<listen>. Must have no query or fragment.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	host, port, err := ensureLoopbackListen(*listen)
	if err != nil {
		return err
	}
	issuer := strings.TrimSpace(*issuerOverride)
	if issuer == "" {
		issuer = "http://" + net.JoinHostPort(host, port)
	}
	if err := validateIssuerURL(issuer); err != nil {
		return err
	}
	key, publicJWK, err := loadOrGenerateKey(*keyFile)
	if err != nil {
		return err
	}
	_ = key
	handler := newIssuerHandler(issuer, publicJWK)
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	fmt.Fprintf(os.Stderr, "kind-oidc-issuer TEST ONLY: issuer=%s listen=%s (loopback only, never expose)\n", issuer, *listen)
	if *keyFile == "" {
		fmt.Fprintln(os.Stderr, "kind-oidc-issuer TEST ONLY: ephemeral key; mint is unavailable without --key-file")
	}
	return server.ListenAndServe()
}

func runMint(args []string) error {
	flags := flag.NewFlagSet("kind-oidc-issuer mint", flag.ContinueOnError)
	keyFile := flags.String("key-file", "", "Path to the signing key file written by serve (required).")
	issuer := flags.String("issuer", defaultIssuer, "Issuer URL to stamp in the iss claim (must match the API's oidc.issuer).")
	audiences := flags.String("audience", defaultAudience, "Comma-separated API audiences for the aud claim.")
	subject := flags.String("subject", defaultSubject, "Subject for the sub claim.")
	ttl := flags.Duration("ttl", 15*time.Minute, "Token lifetime (capped at 1h).")
	roles := flags.String("roles", "kind-local-desktop", "Comma-separated roles claim.")
	groups := flags.String("groups", "", "Comma-separated groups claim.")
	namespaces := flags.String("namespaces", "agents", "Comma-separated namespace claim values.")
	namespaceClaim := flags.String("namespace-claim", "anvil_agents_namespaces", "Namespace claim name (must match the API's authorization.namespaceClaim).")
	email := flags.String("email", "", "Optional email claim.")
	emailVerified := flags.Bool("email-verified", false, "Set email_verified when --email is given.")
	scope := flags.String("scope", "openid profile email", "Space-separated scope claim.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*keyFile) == "" {
		return fmt.Errorf("--key-file is required (run serve once with the same --key-file to create it)")
	}
	if err := validateIssuerURL(*issuer); err != nil {
		return err
	}
	if *ttl <= 0 || *ttl > maxMintTTL {
		return fmt.Errorf("--ttl must be positive and at most %s", maxMintTTL)
	}
	key, err := loadKey(*keyFile)
	if err != nil {
		return err
	}
	raw, err := mintToken(key, mintOptions{
		issuer:         strings.TrimSpace(*issuer),
		audiences:      splitList(*audiences),
		subject:        strings.TrimSpace(*subject),
		ttl:            *ttl,
		roles:          splitList(*roles),
		groups:         splitList(*groups),
		namespaces:     splitList(*namespaces),
		namespaceClaim: strings.TrimSpace(*namespaceClaim),
		email:          strings.TrimSpace(*email),
		emailVerified:  *emailVerified,
		scope:          strings.TrimSpace(*scope),
	})
	if err != nil {
		return err
	}
	fmt.Println(raw)
	return nil
}

// ensureLoopbackListen refuses anything that is not a loopback listen
// address so this test helper can never become a shared issuer.
func ensureLoopbackListen(listen string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", "", fmt.Errorf("invalid --listen %q: %v", listen, err)
	}
	if port == "" {
		return "", "", fmt.Errorf("invalid --listen %q: port is required", listen)
	}
	if strings.EqualFold(host, "localhost") {
		return host, port, nil
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !ip.IsLoopback() {
		return "", "", fmt.Errorf("refusing non-loopback --listen %q (TEST ONLY issuer must stay on loopback)", listen)
	}
	return host, port, nil
}

func validateIssuerURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("issuer must be an absolute URL without query or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("issuer must use http or https")
	}
	return nil
}

// loadOrGenerateKey loads the persisted signing key or creates one. Without a
// key file the key stays ephemeral in memory (serve works, mint refuses).
func loadOrGenerateKey(keyFile string) (*rsa.PrivateKey, jose.JSONWebKey, error) {
	trimmed := strings.TrimSpace(keyFile)
	if trimmed == "" {
		key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			return nil, jose.JSONWebKey{}, fmt.Errorf("generate ephemeral key: %w", err)
		}
		return key, publicJWK(&key.PublicKey), nil
	}
	if _, err := os.Stat(trimmed); err == nil {
		key, err := loadKey(trimmed)
		if err != nil {
			return nil, jose.JSONWebKey{}, err
		}
		return key, publicJWK(&key.PublicKey), nil
	} else if !os.IsNotExist(err) {
		return nil, jose.JSONWebKey{}, fmt.Errorf("stat key file: %w", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, jose.JSONWebKey{}, fmt.Errorf("generate key: %w", err)
	}
	stored := jose.JSONWebKey{Key: key, KeyID: issuerKeyID, Algorithm: string(jose.RS256), Use: "sig"}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return nil, jose.JSONWebKey{}, fmt.Errorf("encode key: %w", err)
	}
	if err := os.WriteFile(trimmed, encoded, 0o600); err != nil {
		return nil, jose.JSONWebKey{}, fmt.Errorf("write key file: %w", err)
	}
	return key, publicJWK(&key.PublicKey), nil
}

func loadKey(keyFile string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(strings.TrimSpace(keyFile))
	if err != nil {
		return nil, fmt.Errorf("read key file (run serve once with the same --key-file to create it): %w", err)
	}
	var stored jose.JSONWebKey
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode key file: %w", err)
	}
	key, ok := stored.Key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key file does not hold an RSA private key")
	}
	return key, nil
}

func publicJWK(public *rsa.PublicKey) jose.JSONWebKey {
	return jose.JSONWebKey{Key: public, KeyID: issuerKeyID, Algorithm: string(jose.RS256), Use: "sig"}
}

func newIssuerHandler(issuer string, public jose.JSONWebKey) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]string{"issuer": issuer, "jwks_uri": strings.TrimRight(issuer, "/") + "/keys"})
	})
	mux.HandleFunc("/keys", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{public}})
	})
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]string{"status": "ok"})
	})
	return mux
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

// mintToken signs a short-lived RS256 access token. Only the returned string
// carries authority; callers must keep it out of logs, query strings, and the
// repository.
func mintToken(key *rsa.PrivateKey, opts mintOptions) (string, error) {
	if key == nil {
		return "", fmt.Errorf("signing key is required")
	}
	if opts.subject == "" {
		return "", fmt.Errorf("subject is required")
	}
	if len(opts.audiences) == 0 {
		return "", fmt.Errorf("at least one audience is required")
	}
	if opts.namespaceClaim == "" {
		return "", fmt.Errorf("namespace claim name is required")
	}
	now := time.Now()
	claims := map[string]any{
		"iss":   opts.issuer,
		"sub":   opts.subject,
		"aud":   opts.audiences,
		"iat":   now.Unix(),
		"exp":   now.Add(opts.ttl).Unix(),
		"scope": opts.scope,
	}
	if len(opts.roles) > 0 {
		claims["roles"] = opts.roles
	}
	if len(opts.groups) > 0 {
		claims["groups"] = opts.groups
	}
	if len(opts.namespaces) > 0 {
		claims[opts.namespaceClaim] = opts.namespaces
	}
	if opts.email != "" {
		claims["email"] = opts.email
		claims["email_verified"] = opts.emailVerified
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", issuerKeyID),
	)
	if err != nil {
		return "", fmt.Errorf("create signer: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode claims: %w", err)
	}
	object, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	raw, err := object.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("serialize token: %w", err)
	}
	return raw, nil
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
