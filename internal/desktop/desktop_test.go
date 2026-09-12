package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogIDsAreUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, tool := range Catalog() {
		if tool.ID == "" || len(tool.Binaries) == 0 {
			t.Fatalf("catalog entry missing id or binaries: %+v", tool)
		}
		if _, ok := seen[tool.ID]; ok {
			t.Fatalf("duplicate catalog id %q", tool.ID)
		}
		seen[tool.ID] = struct{}{}
		if tool.ID == "kubectl" || tool.ID == "anvil-agentctl" {
			t.Fatalf("kubernetes clients must not be in the desktop catalog: %q", tool.ID)
		}
		if tool.Kind == KindHarness && tool.Invoke.Mode == PromptFile && !strings.HasPrefix(tool.Invoke.FileFlag, "--") {
			t.Fatalf("%s file invoke needs a constant flag, got %q", tool.ID, tool.Invoke.FileFlag)
		}
	}
	if _, ok := seen["codex"]; !ok {
		t.Fatal("catalog missing codex")
	}
}

func TestDiscoverFindsCatalogBinariesOnPATH(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "codex"), "#!/bin/sh\necho 'codex-cli 0.42.0'\n")
	writeExec(t, filepath.Join(dir, "grok"), "#!/bin/sh\necho 'grok 1.0.0'\n")
	writeExec(t, filepath.Join(dir, "openclaw"), "#!/bin/sh\necho 'openclaw 0.9'\n")

	discoverer := Discoverer{Path: dir}
	found := discoverer.Discover(context.Background())
	present := map[string]Discovered{}
	for _, item := range found {
		if item.Present {
			present[item.ID] = item
		}
	}
	for _, id := range []string{"codex", "grok", "openclaw"} {
		item, ok := present[id]
		if !ok {
			t.Fatalf("expected %s to be present, got %#v", id, found)
		}
		if item.Path != filepath.Join(dir, item.Binaries[0]) {
			t.Fatalf("%s path = %q", id, item.Path)
		}
		if item.Version == "" {
			t.Fatalf("%s missing version", id)
		}
		if !item.Delegatable {
			t.Fatalf("%s should be delegatable", id)
		}
	}
	if _, ok := present["cursor"]; ok {
		t.Fatal("cursor should not be present in the isolated PATH")
	}
}

func TestSanitizeVersionStripsControls(t *testing.T) {
	got := sanitizeVersion("  grok 1.2.3\x1b[0m\nmore")
	if got != "grok 1.2.3" {
		t.Fatalf("sanitizeVersion = %q", got)
	}
}

func TestParseAPIOrigin(t *testing.T) {
	got, err := ParseAPIOrigin("https://agents.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://agents.example.com" {
		t.Fatalf("got %q", got)
	}
	if _, err := ParseAPIOrigin("https://user:token@agents.example.com"); err == nil {
		t.Fatal("expected credential URL to fail")
	}
	if _, err := ParseAPIOrigin("https://agents.example.com/api"); err == nil {
		t.Fatal("expected path to fail")
	}
	if _, err := ParseAPIOrigin("https://agents.example.com?access_token=x"); err == nil {
		t.Fatal("expected query to fail")
	}
	if _, err := ParseAPIOrigin("ftp://agents.example.com"); err == nil {
		t.Fatal("expected scheme to fail")
	}
}

func TestValidatePrefsRejectsCredentialURLs(t *testing.T) {
	err := validatePrefs(Prefs{APIOrigin: "https://user:token@agents.example.com"})
	if err == nil {
		t.Fatal("expected credential URL to fail")
	}
	if err := validatePrefs(Prefs{APIOrigin: "https://agents.example.com"}); err != nil {
		t.Fatalf("valid origin: %v", err)
	}
}

func TestRequireLoopback(t *testing.T) {
	if err := requireLoopback("127.0.0.1:1738"); err != nil {
		t.Fatal(err)
	}
	if err := requireLoopback("0.0.0.0:1738"); err == nil {
		t.Fatal("expected non-loopback listen to fail")
	}
}

func TestNewServerRejectsNonLoopback(t *testing.T) {
	_, err := NewServer(Options{Listen: "0.0.0.0:1738", ConfigDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected non-loopback listen to fail")
	}
	if !strings.Contains(err.Error(), ProductTitle) {
		t.Fatalf("loopback error missing product title: %v", err)
	}
}

func TestSnapshotAndPrefsHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
		case "/ui-config.json":
			writeJSON(writer, http.StatusOK, map[string]any{
				"productTitle": "Anvil Agents Console",
				"oidc":         map[string]string{"issuer": "https://auth.example.com", "clientId": "desktop"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	configDir := t.TempDir()
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: configDir,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/local/v1/snapshot", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot HTTP %d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.ProductTitle != ProductTitle {
		t.Fatalf("title = %q", snap.ProductTitle)
	}
	if snap.API.Origin != "" {
		t.Fatalf("expected empty API origin, got %#v", snap.API)
	}
	if len(snap.Wrapper.Tools) != 3 {
		t.Fatalf("wrapper tools = %#v", snap.Wrapper.Tools)
	}
	ids := map[string]struct{}{}
	for _, tool := range snap.Wrapper.Tools {
		ids[tool.ID] = struct{}{}
	}
	for _, id := range []string{"create_agent", "anvil-api", "local-harness"} {
		if _, ok := ids[id]; !ok {
			t.Fatalf("missing wrapper tool %q in %#v", id, snap.Wrapper.Tools)
		}
	}

	body := strings.NewReader(`{"apiOrigin":"` + upstream.URL + `"}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/prefs", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prefs HTTP %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode prefs snapshot: %v", err)
	}
	if snap.Prefs.APIOrigin != upstream.URL {
		t.Fatalf("api origin = %q", snap.Prefs.APIOrigin)
	}
	if !snap.API.Reachable {
		t.Fatalf("api = %#v", snap.API)
	}

	loaded, err := loadPrefs(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIOrigin != upstream.URL {
		t.Fatalf("persisted origin = %#v", loaded)
	}
}

func TestHealthzAndSPAFallback(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<html>desktop</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		UIDir:     uiDir,
		ConfigDir: t.TempDir(),
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("healthz: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/wrapper", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "desktop") {
		t.Fatalf("spa fallback: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/auth/callback", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "desktop") {
		t.Fatalf("auth callback spa: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/callback", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "desktop") {
		t.Fatalf("callback spa: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/chat", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "desktop") {
		t.Fatalf("chat spa: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIProxyAllowlistAndTokenQuery(t *testing.T) {
	var gotAuth string
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		if request.URL.Query().Get("access_token") != "" {
			t.Fatal("upstream received access_token query")
		}
		gotAuth = request.Header.Get("Authorization")
		switch request.URL.Path {
		case "/ui-config.json":
			writeJSON(writer, http.StatusOK, map[string]any{
				"productTitle": "Anvil Agents Console",
				"oidc":         map[string]string{"issuer": "https://auth.example.com", "clientId": "desktop"},
				"desktop":      map[string]any{"stubSession": true},
			})
		case "/api/v1/namespaces/hazy-trade/agent-runs":
			writeJSON(writer, http.StatusOK, map[string]any{"items": []any{}})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.Close)

	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		APIOrigin: upstream.URL,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ui-config HTTP %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ProductTitle) {
		t.Fatalf("expected rewritten product title, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"kubernetes":true`) {
		t.Fatalf("desktop ui-config must not advertise kubernetes: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"stubSession":true`) {
		t.Fatalf("expected stubSession to survive ui-config rewrite: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/hazy-trade/agent-runs?limit=20", nil)
	req.Header.Set("Authorization", "Bearer desktop-token")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy HTTP %d %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer desktop-token" {
		t.Fatalf("Authorization not forwarded: %q", gotAuth)
	}

	before := hits
	req = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/hazy-trade/agent-runs?access_token=secret", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("token query HTTP %d %s", rec.Code, rec.Body.String())
	}
	if hits != before {
		t.Fatal("token query must not be proxied")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/secret", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unlisted API path HTTP %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://auth.example.com") {
		t.Fatalf("CSP missing issuer origin: %q", csp)
	}
}

func TestDelegateRunsCatalogBinaryWithoutTokenEnv(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "codex"), `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "codex-cli 0.42.0"
  exit 0
fi
echo "AUTH=${AUTHORIZATION-unset}"
echo "KUBE=${KUBECONFIG-unset}"
echo "---"
cat
`)
	writeExec(t, filepath.Join(dir, "grok"), `#!/bin/sh
if [ "$1" = "--version" ]; then echo "grok 1.0"; exit 0; fi
file=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--prompt-file" ]; then file="$2"; shift 2; continue; fi
  shift
done
cat "$file"
`)
	t.Setenv("AUTHORIZATION", "Bearer should-not-leak")
	t.Setenv("KUBECONFIG", "/tmp/should-not-leak")

	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		Discoverer: Discoverer{
			Path: dir,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"harness":"codex","prompt":"hello desktop","timeoutSeconds":15}`)
	req := httptest.NewRequest(http.MethodPost, "/local/v1/delegate", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delegate HTTP %d %s", rec.Code, rec.Body.String())
	}
	var result DelegateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.TimedOut {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Stdout, "should-not-leak") {
		t.Fatalf("OIDC/kube env leaked to CLI: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "hello desktop") {
		t.Fatalf("prompt missing: %q", result.Stdout)
	}

	body = strings.NewReader(`{"harness":"grok","prompt":"from-file"}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/delegate", body)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("grok delegate HTTP %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Stdout) != "from-file" {
		t.Fatalf("grok stdout = %q", result.Stdout)
	}

	body = strings.NewReader(`{"harness":"kubectl","prompt":"nope"}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/delegate", body)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("kubectl delegate HTTP %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoadPrefsMigratesLegacyConsoleURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, prefsFileName)
	if err := os.WriteFile(path, []byte(`{"consoleURL":"https://agents.example.com","kubeContext":"kind-anvil"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadPrefs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.APIOrigin != "https://agents.example.com" {
		t.Fatalf("migrated origin = %#v", prefs)
	}
}

func TestDelegateEnvStripsSecrets(t *testing.T) {
	got := delegateEnv([]string{
		"PATH=/usr/bin",
		"AUTHORIZATION=Bearer x",
		"KUBECONFIG=/tmp/kube",
		"ANVIL_ACCESS_TOKEN=nope",
		"HOME=/home/op",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "Bearer") || strings.Contains(joined, "kube") || strings.Contains(joined, "nope") {
		t.Fatalf("leaked env: %#v", got)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/home/op") {
		t.Fatalf("lost env: %#v", got)
	}
}

func writeExec(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestProxyPathAllowed(t *testing.T) {
	if !proxyPathAllowed(http.MethodGet, "/api/v1/namespaces/ns/agent-runs") {
		t.Fatal("expected runs list allowed")
	}
	if !proxyPathAllowed(http.MethodPost, "/api/v1/namespaces/ns/agent-run-profiles") {
		t.Fatal("expected profile create POST allowed")
	}
	if !proxyPathAllowed(http.MethodPost, "/api/v1/namespaces/ns/anvil-council") {
		t.Fatal("expected Anvil council POST to be proxied")
	}
	if !proxyPathAllowed(http.MethodPost, "/api/v1/namespaces/ns/anvil-council/turns") {
		t.Fatal("expected Anvil council turn POST to be proxied")
	}
	if !proxyPathAllowed(http.MethodPost, "/api/v1/namespaces/ns/chat/threads") {
		t.Fatal("expected chat thread POST allowed")
	}
	if !proxyPathAllowed(http.MethodGet, "/api/v1/namespaces/ns/chat/threads/abc/messages") {
		t.Fatal("expected chat messages GET allowed")
	}
	if proxyPathAllowed(http.MethodGet, "/api/v1/secrets") {
		t.Fatal("secrets path must be denied")
	}
	if proxyPathAllowed(http.MethodGet, "/api/v1/namespaces/ns/../secrets") {
		t.Fatal("path traversal must be denied")
	}
	if !proxyPathAllowed(http.MethodGet, "/ui-config.json") {
		t.Fatal("ui-config GET must be allowed")
	}
	if proxyPathAllowed(http.MethodPost, "/ui-config.json") {
		t.Fatal("ui-config POST must be denied")
	}
}
