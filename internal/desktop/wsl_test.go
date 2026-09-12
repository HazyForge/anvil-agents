package desktop

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDecodeAndParseWSLDistroList(t *testing.T) {
	utf8List := "Ubuntu-24.04\r\ndebian\n"
	got := parseWSLDistroList(utf8List)
	if len(got) != 2 || got[0] != "Ubuntu-24.04" || got[1] != "debian" {
		t.Fatalf("utf8 list = %#v", got)
	}

	u16 := utf16.Encode([]rune("* Ubuntu-24.04\n  debian\n"))
	raw := make([]byte, 0, 2+len(u16)*2)
	raw = append(raw, 0xFF, 0xFE)
	for _, r := range u16 {
		raw = append(raw, byte(r), byte(r>>8))
	}
	got = parseWSLDistroList(decodeWSLOutput(raw))
	if len(got) != 2 || got[0] != "Ubuntu-24.04" {
		t.Fatalf("utf16 list = %#v", got)
	}
	if got := parseWSLDistroList("evil distro; rm -rf /"); len(got) != 0 {
		t.Fatalf("expected unsafe distro names to be dropped, got %#v", got)
	}
	if got := parseWSLDistroList("Ubuntu;rm"); len(got) != 0 {
		t.Fatalf("semicolon names must be dropped, got %#v", got)
	}
	verbose := "  NAME              STATE           VERSION\n* Ubuntu-24.04      Running         2\n  debian            Stopped         2\n"
	got = parseWSLDistroList(verbose)
	if len(got) != 2 || got[0] != "Ubuntu-24.04" || got[1] != "debian" {
		t.Fatalf("verbose list = %#v", got)
	}
}

func TestWSLArgvRejectsUnsafeDistro(t *testing.T) {
	if _, err := wslArgv("Ubuntu-24.04", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wslArgv("bad distro", []string{"/bin/true"}); err == nil {
		t.Fatal("expected invalid distro")
	}
	if _, err := wslArgv("Ubuntu;rm", []string{"/bin/true"}); err == nil {
		t.Fatal("expected invalid distro")
	}
}

func TestLinuxEnvArgvDropsTokens(t *testing.T) {
	got := linuxEnvArgv([]string{
		"ANVIL_DESKTOP_BIN=codex",
		"AUTHORIZATION=Bearer x",
		"ACCESS_TOKEN=nope",
		"KUBECONFIG=/tmp/kube",
	}, []string{"/bin/true"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "Bearer") || strings.Contains(joined, "nope") || strings.Contains(joined, "KUBE") {
		t.Fatalf("leaked env into WSL argv: %#v", got)
	}
	if got[0] != "/usr/bin/env" || !strings.Contains(joined, "ANVIL_DESKTOP_BIN=codex") {
		t.Fatalf("missing catalog env: %#v", got)
	}
}

func TestDesktopOIDCClientID(t *testing.T) {
	if got := desktopOIDCClientID("", map[string]any{}); got != "" {
		t.Fatalf("empty should keep API client id, got %q", got)
	}
	const consoleID = "383499920822362966"
	both := map[string]any{
		"oidc":    map[string]any{"clientId": consoleID},
		"desktop": map[string]any{"oidcClientId": "from-gitops"},
	}
	if got := desktopOIDCClientID("", both); got != "from-gitops" {
		t.Fatalf("Native must win over Console User-Agent, got %q", got)
	}
	if got := desktopOIDCClientID("cli-override", both); got != "cli-override" {
		t.Fatalf("cli = %q", got)
	}
	consoleOnly := map[string]any{"oidc": map[string]any{"clientId": consoleID}}
	if got := desktopOIDCClientID("", consoleOnly); got != "" {
		t.Fatalf("missing Native must not invent a client id, got %q", got)
	}
	if DefaultOIDCClientID != "anvil-agents-desktop" {
		t.Fatalf("Kind/desktop PKCE pattern = %q", DefaultOIDCClientID)
	}
}

func TestResolveHarnessTarget(t *testing.T) {
	if got := resolveHarnessTarget(Prefs{}, WSLStatus{}); got != HarnessTargetNative {
		t.Fatalf("empty = %q", got)
	}
	if got := resolveHarnessTarget(Prefs{}, WSLStatus{Available: true}); got != HarnessTargetWSL {
		t.Fatalf("auto wsl = %q", got)
	}
	if got := resolveHarnessTarget(Prefs{HarnessTarget: HarnessTargetNative}, WSLStatus{Available: true}); got != HarnessTargetNative {
		t.Fatalf("explicit native = %q", got)
	}
}

func fakeWSLDiscoverer(t *testing.T, binDir string) Discoverer {
	t.Helper()
	script := filepath.Join("testdata", "fake-wsl.sh")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANVIL_WSL_DISTROS", "Ubuntu-24.04")
	t.Setenv("ANVIL_WSL_DEFAULT", "Ubuntu-24.04")
	t.Setenv("ANVIL_WSL_PATH", binDir)
	return Discoverer{
		Target:  HarnessTargetWSL,
		Distro:  "Ubuntu-24.04",
		WSLPath: binDir,
		LookPath: func(string) (string, error) {
			return "", os.ErrNotExist
		},
		WSL: WSLRunner{
			LookExe: func() (string, error) { return abs, nil },
		},
	}
}

func TestDiscoverAndDelegateViaFakeWSL(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "codex"), `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then echo "codex-cli 0.42.0"; exit 0; fi
echo "AUTH=${AUTHORIZATION-unset}"
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
	record := filepath.Join(dir, "wsl-argv.log")
	t.Setenv("ANVIL_WSL_RECORD", record)
	t.Setenv("AUTHORIZATION", "Bearer should-not-leak")
	t.Setenv("KUBECONFIG", "/tmp/should-not-leak")

	discoverer := fakeWSLDiscoverer(t, dir)
	found := discoverer.Discover(context.Background())
	present := map[string]Discovered{}
	for _, item := range found {
		if item.Present {
			present[item.ID] = item
		}
	}
	codex, ok := present["codex"]
	if !ok || codex.Source != HarnessTargetWSL || !strings.Contains(codex.Path, "codex") {
		t.Fatalf("codex via WSL = %#v", found)
	}
	if !strings.Contains(codex.Version, "0.42.0") {
		t.Fatalf("codex version = %q", codex.Version)
	}

	server, err := NewServer(Options{
		Listen:        "127.0.0.1:0",
		ConfigDir:     t.TempDir(),
		HarnessTarget: HarnessTargetWSL,
		WSLDistro:     "Ubuntu-24.04",
		Discoverer:    discoverer,
	})
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"harness":"codex","prompt":"hello wsl","timeoutSeconds":15}`)
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
	if result.Target != HarnessTargetWSL || result.Distro != "Ubuntu-24.04" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Stdout, "should-not-leak") {
		t.Fatalf("OIDC/kube env leaked to WSL CLI: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "hello wsl") {
		t.Fatalf("prompt missing: %q", result.Stdout)
	}

	body = strings.NewReader(`{"harness":"grok","prompt":"from-wsl-file"}`)
	req = httptest.NewRequest(http.MethodPost, "/local/v1/delegate", body)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("grok WSL delegate HTTP %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Stdout) != "from-wsl-file" {
		t.Fatalf("grok WSL stdout = %q stderr=%q", result.Stdout, result.Stderr)
	}

	raw, _ := os.ReadFile(record)
	logged := string(raw)
	if strings.Contains(logged, "should-not-leak") {
		t.Fatalf("token appeared in recorded wsl argv: %s", raw)
	}
	if !strings.Contains(logged, "--exec") || !strings.Contains(logged, "-d Ubuntu-24.04") {
		t.Fatalf("Windows-hosted adapter must invoke wsl.exe --exec -d <distro>, got %s", raw)
	}
}

func TestPrefsHarnessTargetValidation(t *testing.T) {
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/local/v1/prefs", strings.NewReader(`{"harnessTarget":"powershell"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target HTTP %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/local/v1/prefs", strings.NewReader(`{"harnessTarget":"wsl","wslDistro":"Ubuntu-24.04"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid wsl prefs HTTP %d %s", rec.Code, rec.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Prefs.HarnessTarget != HarnessTargetWSL || snap.Prefs.WSLDistro != "Ubuntu-24.04" {
		t.Fatalf("prefs = %#v", snap.Prefs)
	}
}

func TestRewriteUIConfigUsesDesktopClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ui-config.json" {
			http.NotFound(writer, request)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"oidc":    map[string]string{"issuer": "https://auth.example.com", "clientId": "anvil-agents-console"},
			"desktop": map[string]any{"oidcClientId": "anvil-agents-desktop"},
		})
	}))
	t.Cleanup(upstream.Close)
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		APIOrigin: upstream.URL,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
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
	if !strings.Contains(rec.Body.String(), `"clientId":"`+DefaultOIDCClientID+`"`) {
		t.Fatalf("expected desktop.oidcClientId rewrite, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"clientId":"anvil-agents-console"`) {
		t.Fatalf("Console client must not remain once Native exists: %s", rec.Body.String())
	}
}

func TestDecodeMaybeGzip(t *testing.T) {
	raw := []byte(`{"ok":true}`)
	got, uncompressed, err := decodeMaybeGzip(raw, "")
	if err != nil || uncompressed || string(got) != string(raw) {
		t.Fatalf("identity: got %q uncompressed=%v err=%v", got, uncompressed, err)
	}
	compressed := gzipJSON(t, map[string]bool{"ok": true})
	got, uncompressed, err = decodeMaybeGzip(compressed, "gzip")
	if err != nil || !uncompressed || string(got) != `{"ok":true}` {
		t.Fatalf("gzip: got %q uncompressed=%v err=%v", got, uncompressed, err)
	}
	if _, _, err := decodeMaybeGzip(compressed, "br"); err == nil {
		t.Fatal("expected unsupported encoding error")
	}
}

func gzipJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRewriteUIConfigGzipPrefersNativeOverConsole(t *testing.T) {
	const consoleID = "383499920822362966"
	const nativeID = "native-from-gitops"
	compressed := gzipJSON(t, map[string]any{
		"oidc":    map[string]string{"issuer": "https://auth.example.com", "clientId": consoleID},
		"desktop": map[string]any{"oidcClientId": nativeID},
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ui-config.json" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(compressed)
	}))
	t.Cleanup(upstream.Close)
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		APIOrigin: upstream.URL,
		Transport: &http.Transport{DisableCompression: true},
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ui-config HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected uncompressed rewrite, got encoding %q body %s", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"clientId":"`+nativeID+`"`) {
		t.Fatalf("expected Native clientId, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"clientId":"`+consoleID+`"`) {
		t.Fatalf("Console User-Agent client must not win once Native exists: %s", rec.Body.String())
	}
}

func TestRewriteUIConfigKeepsConsoleWhenNativeMissing(t *testing.T) {
	const consoleID = "383499920822362966"
	compressed := gzipJSON(t, map[string]any{
		"oidc": map[string]string{"issuer": "https://auth.example.com", "clientId": consoleID},
	})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ui-config.json" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(compressed)
	}))
	t.Cleanup(upstream.Close)
	server, err := NewServer(Options{
		Listen:    "127.0.0.1:0",
		ConfigDir: t.TempDir(),
		APIOrigin: upstream.URL,
		Transport: &http.Transport{DisableCompression: true},
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
			WSL:      WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }},
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
	if !strings.Contains(rec.Body.String(), `"clientId":"`+consoleID+`"`) {
		t.Fatalf("Console fallback should remain when Native is absent: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"clientId":"`+DefaultOIDCClientID+`"`) {
		t.Fatalf("must not substitute Kind pattern for missing Native: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"oidcClientId"`) {
		t.Fatalf("must not invent desktop.oidcClientId: %s", rec.Body.String())
	}
}
