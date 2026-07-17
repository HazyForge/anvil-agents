#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root_dir}/charts/anvil-agents"
release="contract"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

api_args=(
  --set api.enabled=true
  --set-string api.config.oidc.issuer=https://issuer.example
  --set-string 'api.config.oidc.audiences[0]=anvil-agents-api'
  --set-string 'api.config.authorization.bindings[0].roles[0]=viewer'
  --set-string 'api.config.authorization.bindings[0].permissions[0]=anvil-agents:runs:read'
  --set-string 'api.config.authorization.bindings[0].permissions[1]=anvil-agents:runs:stream'
  --set-string 'api.config.authorization.bindings[0].namespaces[0]=agents'
)

fail() {
  printf 'chart contract failed: %s\n' "$*" >&2
  exit 1
}

expect_template_failure() {
  local name="$1"
  shift
  if helm template "${release}" "${chart}" "${api_args[@]}" "$@" >"${tmp_dir}/${name}.out" 2>"${tmp_dir}/${name}.err"; then
    fail "${name} unexpectedly rendered"
  fi
}

document_exists() {
  local file="$1" kind="$2" name="$3"
  awk -v expected_kind="${kind}" -v expected_name="${name}" '
    BEGIN { RS="---" }
    {
      kind=""; name=""; in_metadata=0
      count=split($0, lines, "\n")
      for (i=1; i<=count; i++) {
        if (lines[i] ~ /^kind: /) { kind=substr(lines[i], 7) }
        if (lines[i] == "metadata:") { in_metadata=1; continue }
        if (in_metadata && lines[i] ~ /^  name: /) { name=substr(lines[i], 9); in_metadata=0 }
      }
      if (kind == expected_kind && name == expected_name) { found=1 }
    }
    END { exit(found ? 0 : 1) }
  ' "${file}"
}

helm lint "${chart}" >/dev/null
helm template "${release}" "${chart}" >"${tmp_dir}/disabled.yaml"
helm template "${release}" "${chart}" --set crds.install=false >"${tmp_dir}/without-crds.yaml"
if document_exists "${tmp_dir}/disabled.yaml" Deployment contract-anvil-agents-api; then
  fail "API resources rendered while api.enabled=false"
fi
if rg -q '^kind: CustomResourceDefinition$' "${tmp_dir}/without-crds.yaml"; then
  fail "CRDs rendered while crds.install=false"
fi
[[ "$(rg -c 'helm.sh/resource-policy: keep' "${tmp_dir}/disabled.yaml")" -eq 7 ]] || fail "all seven CRDs must be retained on Helm uninstall"
helm template "${release}" "${chart}" --show-only templates/clusterrole.yaml >"${tmp_dir}/controller-rbac.yaml"
if rg -q 'external-secrets.io' "${tmp_dir}/controller-rbac.yaml"; then
  fail "ExternalSecrets RBAC rendered while externalSecrets.enabled=false"
fi
helm template "${release}" "${chart}" --set externalSecrets.enabled=true \
  --show-only templates/clusterrole.yaml >"${tmp_dir}/controller-rbac-external-secrets.yaml"
rg -q 'external-secrets.io' "${tmp_dir}/controller-rbac-external-secrets.yaml" || fail "ExternalSecrets RBAC was not enabled"
helm template "${release}" "${chart}" --set-string runnerImages.codex=registry.example/codex@sha256:abc \
  --show-only templates/deployment.yaml >"${tmp_dir}/controller-runner-images.yaml"
rg -q --fixed-strings -- '--runner-image-codex=registry.example/codex@sha256:abc' "${tmp_dir}/controller-runner-images.yaml" || fail "configured runner image was not rendered"

helm template "${release}" "${chart}" "${api_args[@]}" >"${tmp_dir}/enabled.yaml"
helm template "${release}" "${chart}" \
  --values "${root_dir}/examples/live-api/zitadel-values.yaml" >"${tmp_dir}/zitadel.yaml"
for resource in \
  'Deployment contract-anvil-agents-api' \
  'ServiceAccount contract-anvil-agents-api' \
  'ClusterRole contract-anvil-agents-api' \
  'ClusterRoleBinding contract-anvil-agents-api' \
  'Service contract-anvil-agents-api'; do
  read -r kind name <<<"${resource}"
  document_exists "${tmp_dir}/enabled.yaml" "${kind}" "${name}" || fail "missing ${kind}/${name}"
done

helm template "${release}" "${chart}" "${api_args[@]}" \
  --show-only templates/api-clusterrole.yaml >"${tmp_dir}/rbac.yaml"
rg -q 'resources: \["pods/log"\]' "${tmp_dir}/rbac.yaml" || fail "API RBAC cannot read pod logs"
if rg -q '(^|[[:space:]])(secrets|watch|create|update|patch|delete)([[:space:]]|$)' "${tmp_dir}/rbac.yaml"; then
  fail "API RBAC contains a secret, watch, or mutation grant"
fi

expect_template_failure service-port-mismatch --set api.service.port=9090
expect_template_failure route-wildcard \
  --set api.httpRoute.enabled=true \
  --set-string 'api.httpRoute.parentRefs[0].name=public' \
  --set-string 'api.httpRoute.parentRefs[0].sectionName=https' \
  --set-string 'api.httpRoute.hostnames[0]=*.example.com'
expect_template_failure route-listener-unspecified \
  --set api.httpRoute.enabled=true \
  --set-string 'api.httpRoute.parentRefs[0].name=public' \
  --set-string 'api.httpRoute.hostnames[0]=agents.example.com'
expect_template_failure private-ca-incomplete \
  --set-string api.oidcCA.configMapName=issuer-ca

helm template "${release}" "${chart}" "${api_args[@]}" \
  --set-string api.oidcCA.configMapName=issuer-ca \
  --set-string api.oidcCA.restartToken=2026-07-17 \
  --set-string api.config.oidc.caFile=/etc/anvil-agents-api/oidc-ca/ca.crt \
  >"${tmp_dir}/ca.yaml"
rg -q 'control.anvil.hazyforge.io/oidc-ca-restart: "2026-07-17"' "${tmp_dir}/ca.yaml" || fail "CA restart token was not rendered"

long_name="anvil-agents-release-with-a-name-long-enough-to-test-truncation-123456789"
helm template "${release}" "${chart}" "${api_args[@]}" \
  --set-string fullnameOverride="${long_name}" >"${tmp_dir}/long-name.yaml"
mapfile -t deployments < <(awk '
  BEGIN { RS="---" }
  {
    kind=""; name=""; in_metadata=0
    count=split($0, lines, "\n")
    for (i=1; i<=count; i++) {
      if (lines[i] ~ /^kind: /) { kind=substr(lines[i], 7) }
      if (lines[i] == "metadata:") { in_metadata=1; continue }
      if (in_metadata && lines[i] ~ /^  name: /) { name=substr(lines[i], 9); in_metadata=0 }
    }
    if (kind == "Deployment") { print name }
  }
' "${tmp_dir}/long-name.yaml")
[[ "${#deployments[@]}" -eq 2 ]] || fail "expected two Deployments for long fullname"
[[ "${deployments[0]}" != "${deployments[1]}" ]] || fail "controller and API Deployment names collided"
for deployment in "${deployments[@]}"; do
  [[ "${#deployment}" -le 63 ]] || fail "Deployment name exceeds 63 characters: ${deployment}"
done

printf 'AgentRun API chart contract passed\n'
