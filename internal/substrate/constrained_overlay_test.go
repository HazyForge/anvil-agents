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
	if !strings.Contains(readme, "12-control-plane-sa-oidc-issuer.yaml") {
		t.Fatal("README must name the Talos CP SA OIDC patch")
	}
	if !strings.Contains(readme, "13-worker-hel1-gvisor-userns.yaml") {
		t.Fatal("README must name the hel1-1 gVisor userns patch")
	}
	if !strings.Contains(readme, "substrate.actorsEnabled") {
		t.Fatal("README must keep actorsEnabled off")
	}
	if strings.Contains(readme, ".hazyforge/clusters/anvil-primaris/namespace/") &&
		!strings.Contains(readme, "not under") {
		t.Fatal("README must keep the overlay out of Primaris namespace GitOps")
	}

	values := read("helm-values.yaml")
	if !strings.Contains(values, "issuer: https://kubernetes.default.svc.cluster.local") {
		t.Fatal("helm-values.yaml missing in-cluster kube jwt issuer")
	}
	if strings.Contains(values, "issuer: \"https://[fdae:") || strings.Contains(values, "issuer: https://[fdae:") {
		t.Fatal("helm-values.yaml must not use the SideroLink VIP as jwt issuer")
	}
	if !strings.Contains(values, "audience: api.ate-system.svc") {
		t.Fatal("helm-values.yaml missing jwt audience")
	}

	talos := read("talos/12-control-plane-sa-oidc-issuer.yaml")
	if !strings.Contains(talos, "service-account-issuer: https://kubernetes.default.svc.cluster.local") {
		t.Fatal("Talos CP patch missing in-cluster service-account-issuer")
	}
	if !strings.Contains(talos, `anonymous-auth: "true"`) {
		t.Fatal("Talos CP patch missing anonymous-auth")
	}
	if strings.Contains(talos, "50000") && !strings.Contains(talos, "Do not expose the Talos API") {
		t.Fatal("Talos CP patch must warn against exposing Talos API")
	}

	userns := read("talos/13-worker-hel1-gvisor-userns.yaml")
	if !strings.Contains(userns, `user.max_user_namespaces: "65536"`) {
		t.Fatal("hel1-1 gVisor patch missing user.max_user_namespaces override")
	}
	if !strings.Contains(userns, "rust-build") {
		t.Fatal("hel1-1 gVisor patch must target the rust-build machine set")
	}

	rbac := read("oidc-discovery-unauthenticated.yaml")
	if !strings.Contains(rbac, "name: oidc-discovery-unauthenticated") {
		t.Fatal("OIDC unauthenticated binding missing")
	}
	if !strings.Contains(rbac, "name: system:service-account-issuer-discovery") {
		t.Fatal("OIDC binding must use kube default discovery role")
	}
	if !strings.Contains(rbac, "name: system:unauthenticated") {
		t.Fatal("OIDC binding must include system:unauthenticated")
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
	storage := read("patches/pin-local-storage.yaml")
	if !strings.Contains(storage, "observability-local") {
		t.Fatal("pin-local-storage.yaml must pin observability-local (hel1-1 has no hcloud CSI topology)")
	}
	stsStorage := read("patches/pin-local-storage-sts.yaml")
	if !strings.Contains(stsStorage, "observability-local") {
		t.Fatal("pin-local-storage-sts.yaml must pin valkey PVCs to observability-local")
	}

	template := read("fleet/base/actortemplate-standing-chat.yaml")
	if !strings.Contains(template, "name: standing-chat") {
		t.Fatal("ActorTemplate must be named standing-chat")
	}
	if !strings.Contains(template, "@sha256:") {
		t.Fatal("ActorTemplate images must be digest-pinned")
	}
	if !strings.Contains(template, "ProcessBackend") {
		t.Fatal("standing-chat template must keep Desktop on ProcessBackend")
	}

	acp := read("fleet/base/actortemplate-acp-spike.yaml")
	if !strings.Contains(acp, "name: acp-spike") {
		t.Fatal("generate-on-actor ActorTemplate must be named acp-spike")
	}
	if !strings.Contains(acp, "@sha256:") {
		t.Fatal("acp-spike images must be digest-pinned")
	}
	if !strings.Contains(acp, "session/prompt") {
		t.Fatal("acp-spike must document ACP session/prompt")
	}

	pool := read("fleet/base/workerpool-warm.yaml")
	if !strings.Contains(pool, "name: warm") || !strings.Contains(pool, "anvil.hazyforge.io/worker-pool: warm") {
		t.Fatal("WorkerPool must be warm with the Anvil pool label")
	}
	if !strings.Contains(pool, "kubernetes.io/hostname: anvil-primaris-worker-hel1-1") {
		t.Fatal("WorkerPool must pin hel1-1")
	}

	gitignore := read(filepath.Join("..", "..", ".gitignore"))
	if !strings.Contains(gitignore, "/config/ate-constrained-primaris/values.secrets.yaml") {
		t.Fatal("values.secrets.yaml must be gitignored")
	}
}
