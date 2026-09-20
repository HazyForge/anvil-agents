#!/usr/bin/env bash
# Render the constrained Primaris ATE overlay. Does not apply.
#
# DO NOT helm upgrade --install stock ATE 0.0.8. This script templates
# oci://ghcr.io/kagent-dev/substrate/helm/substrate 0.0.8 (or ATE_CHART_DIR),
# overrides jwt issuer/audience and rotated RustFS keys, then JSON6902-pins
# every DaemonSet/Deployment/StatefulSet/Job onto
# anvil-primaris-worker-hel1-1 so hostPorts 8085/9090 are not cluster-wide.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
overlay="${root_dir}/config/ate-constrained-primaris"
chart_ref="${ATE_CHART:-oci://ghcr.io/kagent-dev/substrate/helm/substrate}"
chart_version="${ATE_CHART_VERSION:-0.0.8}"
chart_dir="${ATE_CHART_DIR:-}"
secrets=""
include_fleet=1
include_crds=0
crds_ref="${ATE_CRDS_CHART:-oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds}"
crds_dir="${ATE_CRDS_CHART_DIR:-}"

usage() {
  cat <<'EOF'
Usage: hack/render-ate-constrained.sh --secrets <values.secrets.yaml> [options]

Renders ATE 0.0.8 + one-node pin + fleet CRs to stdout. Does not apply.

  --secrets FILE     Rotated rustfs accessKey/secretKey (required). Gitignored.
  --skip-fleet       Omit ActorTemplate/WorkerPool (helm+pin only).
  --include-crds     Prepend substrate-crds 0.0.8 (still not applied).
  -h, --help         Show this help.

Env:
  ATE_CHART / ATE_CHART_VERSION   default oci://ghcr.io/kagent-dev/substrate/helm/substrate 0.0.8
  ATE_CHART_DIR                   local unpacked chart (tests; skips OCI pull)
  ATE_CRDS_CHART / ATE_CRDS_CHART_DIR
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --secrets)
      secrets="${2:-}"
      shift 2
      ;;
    --skip-fleet)
      include_fleet=0
      shift
      ;;
    --include-crds)
      include_crds=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'unknown flag: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${secrets}" || ! -f "${secrets}" ]]; then
  printf 'render-ate-constrained: --secrets file is required (copy values.secrets.yaml.example and rotate keys)\n' >&2
  exit 2
fi
if grep -E 'accessKey:[[:space:]]*rustfsadmin([[:space:]]|$)|secretKey:[[:space:]]*rustfsadmin([[:space:]]|$)' "${secrets}" >/dev/null 2>&1; then
  printf 'render-ate-constrained: secrets file still uses chart default rustfsadmin; rotate keys\n' >&2
  exit 1
fi
if grep -E 'SET_AT_APPLY_DO_NOT_COMMIT|ROTATE_BEFORE_RENDER' "${secrets}" >/dev/null 2>&1; then
  printf 'render-ate-constrained: secrets file still has placeholders; rotate keys\n' >&2
  exit 1
fi

if ! command -v helm >/dev/null 2>&1; then
  printf 'render-ate-constrained: helm is required\n' >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

helm_chart="${chart_dir}"
if [[ -z "${helm_chart}" ]]; then
  helm_chart="${chart_ref}"
fi

helm_args=(template substrate "${helm_chart}" --namespace ate-system --version "${chart_version}" --values "${overlay}/helm-values.yaml" --values "${secrets}")
if [[ -n "${chart_dir}" ]]; then
  helm_args=(template substrate "${chart_dir}" --namespace ate-system --values "${overlay}/helm-values.yaml" --values "${secrets}")
fi

helm "${helm_args[@]}" >"${tmp_dir}/helm.yaml"

kustomize_cmd=()
if command -v kustomize >/dev/null 2>&1; then
  kustomize_cmd=(kustomize build)
elif command -v kubectl >/dev/null 2>&1; then
  kustomize_cmd=(kubectl kustomize)
else
  printf 'render-ate-constrained: kustomize or kubectl is required to pin nodeSelector\n' >&2
  exit 1
fi

cp "${overlay}/patches/pin-one-node.yaml" "${tmp_dir}/pin-one-node.yaml"
cat >"${tmp_dir}/kustomization.yaml" <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - helm.yaml
patches:
  - target:
      kind: DaemonSet
    path: pin-one-node.yaml
  - target:
      kind: Deployment
    path: pin-one-node.yaml
  - target:
      kind: StatefulSet
    path: pin-one-node.yaml
  - target:
      kind: Job
    path: pin-one-node.yaml
EOF

if [[ "${include_crds}" -eq 1 ]]; then
  crds_chart="${crds_dir}"
  crds_args=(template substrate-crds)
  if [[ -n "${crds_chart}" ]]; then
    crds_args+=("${crds_chart}" --namespace ate-system)
  else
    crds_args+=("${crds_ref}" --namespace ate-system --version "${chart_version}")
  fi
  helm "${crds_args[@]}"
  printf '\n---\n'
fi

"${kustomize_cmd[@]}" "${tmp_dir}"

if [[ "${include_fleet}" -eq 1 ]]; then
  printf '\n---\n'
  "${kustomize_cmd[@]}" "${overlay}/fleet"
fi
