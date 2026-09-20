package substrate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func constrainedOverlayDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "config", "ate-constrained-primaris")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("constrained overlay dir: %v", err)
	}
	return dir
}

func TestConstrainedOverlayForbidsStockInstallAndDefaultRustFSKeys(t *testing.T) {
	t.Parallel()
	root := constrainedOverlayDir(t)
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(raw)
	}
	readme := read("README.md")
	if !strings.Contains(readme, "Do not") && !strings.Contains(readme, "DO NOT") {
		t.Fatal("README must forbid stock-install")
	}
	if !strings.Contains(readme, "anvil-primaris-worker-hel1-1") {
		t.Fatal("README must name the pinned worker")
	}
	if strings.Contains(readme, ".hazyforge/clusters/anvil-primaris/namespace/") &&
		!strings.Contains(readme, "not under") {
		t.Fatal("README must keep the overlay out of Primaris namespace GitOps")
	}

	values := read("helm-values.yaml")
	if !strings.Contains(values, "https://[fdae:41e4:649b:9303::1]:10000") {
		t.Fatal("helm-values.yaml missing Talos jwt issuer")
	}
	if !strings.Contains(values, "audience: api.ate-system.svc") {
		t.Fatal("helm-values.yaml missing jwt audience")
	}
	if strings.Contains(values, "accessKey: rustfsadmin") || strings.Contains(values, "secretKey: rustfsadmin") {
		t.Fatal("helm-values.yaml must not commit chart default rustfsadmin as a value")
	}
	if !strings.Contains(values, "SET_AT_APPLY_DO_NOT_COMMIT") {
		t.Fatal("helm-values.yaml must placeholder-rotate rustfs keys")
	}

	pin := read("patches/pin-one-node.yaml")
	if !strings.Contains(pin, "kubernetes.io/hostname: anvil-primaris-worker-hel1-1") {
		t.Fatal("pin-one-node.yaml missing hostname selector")
	}

	template := read("fleet/base/actortemplate-standing-chat.yaml")
	if !strings.Contains(template, "name: standing-chat") {
		t.Fatal("ActorTemplate must be named standing-chat")
	}
	if !strings.Contains(template, "@sha256:") {
		t.Fatal("ActorTemplate images must be digest-pinned")
	}

	pool := read("fleet/base/workerpool-warm.yaml")
	if !strings.Contains(pool, "name: warm") || !strings.Contains(pool, "anvil.hazyforge.io/worker-pool: warm") {
		t.Fatal("WorkerPool must be warm with the Anvil pool label")
	}
	if !strings.Contains(pool, "kubernetes.io/hostname: anvil-primaris-worker-hel1-1") {
		t.Fatal("WorkerPool must pin hel1-1")
	}

	if _, err := os.Stat(filepath.Join(root, "values.secrets.yaml")); err == nil {
		t.Fatal("values.secrets.yaml must not be committed")
	}
}
