#!/usr/bin/env bash
# Static (always) + optional helm-render contract for the constrained ATE overlay.
# Never applies to a cluster. Never helm-installs stock 0.0.8.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
overlay="${root_dir}/config/ate-constrained-primaris"
worker="anvil-primaris-worker-hel1-1"

fail() {
  printf 'ate constrained overlay contract failed: %s\n' "$*" >&2
  exit 1
}

[[ -f "${overlay}/README.md" ]] || fail "README.md missing"
grep -q 'Do not' "${overlay}/README.md" || grep -qi 'do not' "${overlay}/README.md" || fail "README must forbid stock-install"
grep -Fq 'helm upgrade --install' "${overlay}/README.md" || fail "README must mention helm upgrade --install in the do-not-stock-install warning"
grep -Fq "${worker}" "${overlay}/README.md" || fail "README must pin ${worker}"
grep -Fq 'substrate.actorsEnabled' "${overlay}/README.md" || fail "README must keep actorsEnabled off"
grep -Fq '12-control-plane-sa-oidc-issuer.yaml' "${overlay}/README.md" || fail "README must name the Talos CP SA OIDC patch"
grep -Fq '13-worker-hel1-gvisor-userns.yaml' "${overlay}/README.md" || fail "README must name the hel1-1 gVisor userns patch"
grep -Fq 'oidc-discovery-unauthenticated' "${overlay}/README.md" || fail "README must name the unauthenticated OIDC binding"

values="${overlay}/helm-values.yaml"
grep -Fq 'issuer: https://kubernetes.default.svc.cluster.local' "${values}" || fail "helm-values.yaml missing in-cluster kube SA issuer"
if grep -E '^[[:space:]]*issuer:[[:space:]]*"?https://\[fdae:' "${values}" >/dev/null 2>&1; then
  fail "helm-values.yaml must not use the SideroLink VIP as jwt issuer"
fi
grep -Fq 'audience: api.ate-system.svc' "${values}" || fail "helm-values.yaml missing jwt audience"
talos_patch="${overlay}/talos/12-control-plane-sa-oidc-issuer.yaml"
[[ -f "${talos_patch}" ]] || fail "Talos CP SA OIDC patch missing"
grep -Fq 'service-account-issuer: https://kubernetes.default.svc.cluster.local' "${talos_patch}" || fail "Talos patch missing in-cluster service-account-issuer"
grep -Fq 'anonymous-auth: "true"' "${talos_patch}" || fail "Talos patch missing anonymous-auth for OIDC GET"
userns_patch="${overlay}/talos/13-worker-hel1-gvisor-userns.yaml"
[[ -f "${userns_patch}" ]] || fail "Talos hel1-1 gVisor userns patch missing"
grep -Fq 'user.max_user_namespaces: "65536"' "${userns_patch}" || fail "userns patch missing Talos KSPP override"
if grep -Eq 'control-plane|metal|vultr' "${userns_patch}"; then
  grep -Fq 'Do not add this sysctl to control-plane, metal, or other worker sets' "${userns_patch}" || fail "userns patch must stay rust-build/hel1-1 only"
fi
rbac="${overlay}/oidc-discovery-unauthenticated.yaml"
grep -Fq 'name: oidc-discovery-unauthenticated' "${rbac}" || fail "OIDC unauthenticated binding missing"
grep -Fq 'name: system:service-account-issuer-discovery' "${rbac}" || fail "OIDC binding must use kube default discovery role"
grep -Fq 'name: system:unauthenticated' "${rbac}" || fail "OIDC binding must include system:unauthenticated"
grep -Fq 'accessKey: SET_AT_APPLY_DO_NOT_COMMIT' "${values}" || fail "helm-values.yaml must override rustfs accessKey placeholder"
grep -Fq 'secretKey: SET_AT_APPLY_DO_NOT_COMMIT' "${values}" || fail "helm-values.yaml must override rustfs secretKey placeholder"
if grep -E 'accessKey:[[:space:]]*rustfsadmin([[:space:]]|$)|secretKey:[[:space:]]*rustfsadmin([[:space:]]|$)' "${values}" >/dev/null 2>&1; then
  fail "helm-values.yaml must not commit chart default rustfsadmin as a value"
fi
if grep -E 'accessKey:[[:space:]]*rustfsadmin([[:space:]]|$)|secretKey:[[:space:]]*rustfsadmin([[:space:]]|$)' "${overlay}/values.secrets.yaml.example" >/dev/null 2>&1; then
  fail "values.secrets.yaml.example must not use rustfsadmin"
fi
if git ls-files --error-unmatch "${overlay}/values.secrets.yaml" >/dev/null 2>&1; then
  fail "values.secrets.yaml must stay gitignored and uncommitted"
fi
grep -Fq '/config/ate-constrained-primaris/values.secrets.yaml' "${root_dir}/.gitignore" || fail "values.secrets.yaml must be gitignored"

grep -Fq "kubernetes.io/hostname: ${worker}" "${overlay}/patches/pin-one-node.yaml" || fail "pin-one-node.yaml missing hostname ${worker}"
grep -Fq 'observability-local' "${overlay}/patches/pin-local-storage.yaml" || fail "pin-local-storage.yaml must pin observability-local"
grep -Fq 'observability-local' "${overlay}/patches/pin-local-storage-sts.yaml" || fail "pin-local-storage-sts.yaml must pin observability-local"
grep -Fq 'createNamespace: true' "${values}" || fail "helm-values.yaml must create ate-system (helm template omits Namespace otherwise)"
grep -Fq 'pod-security.kubernetes.io/enforce=privileged' "${overlay}/README.md" || fail "README must document PSA privileged for ateom workers"
grep -Fq 'hostPort' "${overlay}/README.md" || fail "README must call out hostPorts"

template="${overlay}/fleet/base/actortemplate-standing-chat.yaml"
grep -Eq '^[[:space:]]*name: standing-chat[[:space:]]*$' "${template}" || fail "ActorTemplate name must be standing-chat"
grep -Fq 'registry.k8s.io/pause:3.10.2@sha256:' "${template}" || fail "pauseImage must be digest-pinned"
grep -Fq 'ghcr.io/hazyforge/anvil-agent-run-grok-build@sha256:' "${template}" || fail "standing-chat agent image must be digest-pinned"
grep -Fq 'NOT an ACP' "${template}" || fail "ActorTemplate must warn that the grok image is not ACP"

pool="${overlay}/fleet/base/workerpool-warm.yaml"
grep -Eq '^[[:space:]]*name: warm[[:space:]]*$' "${pool}" || fail "WorkerPool name must be warm"
grep -Fq "anvil.hazyforge.io/worker-pool: warm" "${pool}" || fail "WorkerPool missing pool label"
grep -Fq "kubernetes.io/hostname: ${worker}" "${pool}" || fail "WorkerPool missing nodeSelector"
grep -Fq 'ateom-gvisor:v0.0.8' "${pool}" || fail "WorkerPool ateomImage must track ATE 0.0.8"

grep -Fq 'namespace: anvilhub' "${overlay}/fleet/anvilhub/kustomization.yaml" || fail "anvilhub fleet overlay missing"
grep -Fq 'namespace: hazy-trade' "${overlay}/fleet/hazy-trade/kustomization.yaml" || fail "hazy-trade fleet overlay missing"

if grep -Fq '.hazyforge/clusters/anvil-primaris/namespace' "${overlay}/kustomization.yaml" && \
   ! grep -Fq 'Do not move this under' "${overlay}/kustomization.yaml"; then
  fail "overlay must not live under Primaris namespace GitOps"
fi

if [[ -d "${root_dir}/.hazyforge/clusters/anvil-primaris/namespace" ]] && \
   find "${root_dir}/.hazyforge/clusters/anvil-primaris/namespace" -type d -name 'ate-constrained-primaris' | grep -q .; then
  fail "constrained overlay must not sit under Primaris namespace GitOps"
fi

if [[ "${ATE_OVERLAY_RENDER:-}" != "1" ]]; then
  printf 'ATE constrained overlay static contract passed\n'
  exit 0
fi

command -v helm >/dev/null 2>&1 || fail "ATE_OVERLAY_RENDER=1 requires helm"
command -v kubectl >/dev/null 2>&1 || command -v kustomize >/dev/null 2>&1 || fail "ATE_OVERLAY_RENDER=1 requires kustomize or kubectl"

chart_dir="${ATE_CHART_DIR:-}"
if [[ -z "${chart_dir}" && -d /tmp/ate-helm-0.0.8/substrate ]]; then
  chart_dir=/tmp/ate-helm-0.0.8/substrate
fi
if [[ -z "${chart_dir}" ]]; then
  fail "ATE_OVERLAY_RENDER=1 needs ATE_CHART_DIR or /tmp/ate-helm-0.0.8/substrate (do not pull stock-install)"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
cat >"${tmp_dir}/secrets.yaml" <<'EOF'
rustfs:
  accessKey: overlay-test-access
  secretKey: overlay-test-secret
EOF

ATE_CHART_DIR="${chart_dir}" "${root_dir}/hack/render-ate-constrained.sh" \
  --secrets "${tmp_dir}/secrets.yaml" >"${tmp_dir}/render.yaml"

if grep -E 'accessKey:[[:space:]]*rustfsadmin([[:space:]]|$)|secretKey:[[:space:]]*rustfsadmin([[:space:]]|$)' "${tmp_dir}/render.yaml" >/dev/null 2>&1; then
  fail "render still contains chart default rustfsadmin values"
fi
grep -Fq 'overlay-test-access' "${tmp_dir}/render.yaml" || fail "render missing rotated access key"

python3 - "${tmp_dir}/render.yaml" "${worker}" <<'PY' || fail "render workloads missing one-node pin or cluster-wide hostPorts"
import sys, re
path, worker = sys.argv[1], sys.argv[2]
text = open(path).read()
docs = re.split(r"\n---\n", "\n" + text)
kinds = {"DaemonSet", "Deployment", "StatefulSet", "Job"}
checked = 0
hostport_unpinned = 0
for doc in docs:
    kind = None
    name = None
    in_meta = False
    for line in doc.splitlines():
        if line.startswith("kind: "):
            kind = line[6:].strip()
        if line == "metadata:":
            in_meta = True
            continue
        if in_meta and line.startswith("  name: "):
            name = line[8:].strip()
            in_meta = False
    if kind not in kinds:
        continue
    checked += 1
    if f"kubernetes.io/hostname: {worker}" not in doc:
        print(f"{kind}/{name} missing nodeSelector {worker}", file=sys.stderr)
        sys.exit(1)
    if "hostPort:" in doc and f"kubernetes.io/hostname: {worker}" not in doc:
        hostport_unpinned += 1
if checked < 6:
    print(f"expected at least 6 pinned workloads, got {checked}", file=sys.stderr)
    sys.exit(1)
if hostport_unpinned:
    print("hostPort without one-node pin", file=sys.stderr)
    sys.exit(1)
print(f"pinned {checked} workloads onto {worker}")
PY

grep -Fq 'kind: ActorTemplate' "${tmp_dir}/render.yaml" || fail "fleet ActorTemplate missing from render"
grep -Fq 'kind: WorkerPool' "${tmp_dir}/render.yaml" || fail "fleet WorkerPool missing from render"
grep -Fq 'name: standing-chat' "${tmp_dir}/render.yaml" || fail "standing-chat template missing from render"
grep -Fq 'namespace: anvilhub' "${tmp_dir}/render.yaml" || fail "anvilhub fleet missing from render"
grep -Fq 'namespace: hazy-trade' "${tmp_dir}/render.yaml" || fail "hazy-trade fleet missing from render"
grep -Fq 'name: oidc-discovery-unauthenticated' "${tmp_dir}/render.yaml" || fail "render missing OIDC unauthenticated binding"
grep -Fq 'https://kubernetes.default.svc.cluster.local' "${tmp_dir}/render.yaml" || fail "render missing in-cluster jwt issuer"

printf 'ATE constrained overlay static+render contract passed\n'
