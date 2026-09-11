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

func TestLoadKubeContextsFromFile(t *testing.T) {
	kubeconfig := writeKubeconfig(t)
	apiConfig, path, err := loadKubeAPIConfig(kubeconfig)
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	if path != kubeconfig {
		t.Fatalf("path = %q", path)
	}
	contexts := listContexts(apiConfig)
	if len(contexts) != 2 {
		t.Fatalf("contexts = %#v", contexts)
	}
	if apiConfig.CurrentContext != "kind-anvil" {
		t.Fatalf("current = %q", apiConfig.CurrentContext)
	}
	if contextNamespace(apiConfig, "prod") != "hazy-trade" {
		t.Fatalf("prod namespace = %q", contextNamespace(apiConfig, "prod"))
	}
}

func TestValidatePrefsRejectsCredentialURLs(t *testing.T) {
	err := validatePrefs(Prefs{ConsoleURL: "https://user:token@agents.example.com"})
	if err == nil {
		t.Fatal("expected credential URL to fail")
	}
	if err := validatePrefs(Prefs{ConsoleURL: "https://agents.example.com"}); err != nil {
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
	configDir := t.TempDir()
	kubeconfig := writeKubeconfig(t)
	server, err := NewServer(Options{
		Listen:     "127.0.0.1:0",
		ConfigDir:  configDir,
		Kubeconfig: kubeconfig,
		Discoverer: Discoverer{
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
		ClusterProbe: func(context.Context, string, string) OperatorStatus {
			return OperatorStatus{Reachable: true, APIGroupPresent: true, Message: "present"}
		},
		ConsoleProbe: func(_ context.Context, origin string) ConsoleStatus {
			return ConsoleStatus{URL: origin, Reachable: origin != "", Message: "ok"}
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
	if snap.Cluster.CurrentContext != "kind-anvil" {
		t.Fatalf("current context = %q", snap.Cluster.CurrentContext)
	}
	if !snap.Cluster.Operator.APIGroupPresent {
		t.Fatalf("operator = %#v", snap.Cluster.Operator)
	}
	if snap.Chat.StandingChatPath != "/chat" {
		t.Fatalf("chat path = %q", snap.Chat.StandingChatPath)
	}

	body := strings.NewReader(`{"kubeContext":"prod","consoleURL":"https://agents.anvil.hazyforge.io"}`)
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
	if snap.Cluster.SelectedContext != "prod" {
		t.Fatalf("selected = %q", snap.Cluster.SelectedContext)
	}
	if snap.Cluster.Namespace != "hazy-trade" {
		t.Fatalf("namespace = %q", snap.Cluster.Namespace)
	}
	if snap.Cluster.Console.URL != "https://agents.anvil.hazyforge.io" {
		t.Fatalf("console = %#v", snap.Cluster.Console)
	}

	loaded, err := loadPrefs(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Context != "prod" {
		t.Fatalf("persisted context = %#v", loaded)
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
		ClusterProbe: func(context.Context, string, string) OperatorStatus { return OperatorStatus{} },
		ConsoleProbe: func(context.Context, string) ConsoleStatus { return ConsoleStatus{} },
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
	req = httptest.NewRequest(http.MethodGet, "/cluster", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "desktop") {
		t.Fatalf("spa fallback: %d %s", rec.Code, rec.Body.String())
	}
}

func writeExec(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	raw := `apiVersion: v1
kind: Config
current-context: kind-anvil
contexts:
- name: kind-anvil
  context:
    cluster: kind
    user: kind
    namespace: default
- name: prod
  context:
    cluster: primaris
    user: austin
    namespace: hazy-trade
clusters:
- name: kind
  cluster:
    server: https://127.0.0.1:6443
- name: primaris
  cluster:
    server: https://127.0.0.1:6443
users:
- name: kind
  user:
    token: unused
- name: austin
  user:
    token: unused
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
