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
if grep -Fq -- '--adverse-sources-json=' "${tmp_dir}/disabled.yaml"; then
  fail "structured adverse source flag rendered with empty adverseSources"
fi
if document_exists "${tmp_dir}/disabled.yaml" Deployment contract-anvil-agents-api; then
  fail "API resources rendered while api.enabled=false"
fi
if grep -Eq '^kind: CustomResourceDefinition$' "${tmp_dir}/without-crds.yaml"; then
  fail "CRDs rendered while crds.install=false"
fi
[[ "$(grep -c 'helm.sh/resource-policy: keep' "${tmp_dir}/disabled.yaml")" -eq 12 ]] || fail "all twelve CRDs must be retained on Helm uninstall"
[[ "$(grep -c 'argocd.argoproj.io/sync-options: Prune=false' "${tmp_dir}/disabled.yaml")" -eq 12 ]] || fail "all twelve CRDs must be retained during Argo ownership transfer"
for crd in agentharnessprofiles agentskillsets agenttoolsets adversesignals; do
  grep -Eq "name: ${crd}\.control\.anvil\.hazyforge\.io" "${tmp_dir}/disabled.yaml" || fail "${crd} CRD was not rendered"
done
grep -q 'harnessProfileRef:' "${tmp_dir}/disabled.yaml" || fail "composition harnessProfileRef schema is missing"
grep -q 'skillSets:' "${tmp_dir}/disabled.yaml" || fail "composition skillSets schema is missing"
grep -q 'toolSets:' "${tmp_dir}/disabled.yaml" || fail "composition toolSets schema is missing"
grep -q 'maxRunsPerDay:' "${tmp_dir}/disabled.yaml" || fail "AgentSchedule daily run budget schema is missing"
grep -q 'name: adversesignals.control.anvil.hazyforge.io' "${tmp_dir}/disabled.yaml" || fail "AdverseSignal CRD is missing"
grep -q 'AdverseSignal spec is immutable' "${tmp_dir}/disabled.yaml" || fail "AdverseSignal immutability validation is missing"
helm template "${release}" "${chart}" --show-only templates/clusterrole.yaml >"${tmp_dir}/controller-rbac.yaml"
for resource in agentharnessprofiles agentskillsets agenttoolsets; do
  grep -q "${resource}" "${tmp_dir}/controller-rbac.yaml" || fail "controller RBAC is missing ${resource}"
done
grep -q 'adversesignals' "${tmp_dir}/controller-rbac.yaml" || fail "controller RBAC is missing adversesignals"
grep -A1 'resources: \["adversesignals"\]' "${tmp_dir}/controller-rbac.yaml" | grep -q '"patch"' || fail "controller RBAC cannot patch AdverseSignal finalizers"
grep -q 'adversesignals/finalizers' "${tmp_dir}/controller-rbac.yaml" || fail "controller RBAC is missing AdverseSignal finalizer updates"
helm template "${release}" "${chart}" \
  --set-json 'adverseSources=[{"apiVersion":"apps.example.io/v1","kind":"Release","resource":"releases","situationRef":{"name":"release-health"}}]' \
  --show-only templates/clusterrole.yaml >"${tmp_dir}/controller-rbac-adverse-source.yaml"
grep -q 'apiGroups: \["apps.example.io"\]' "${tmp_dir}/controller-rbac-adverse-source.yaml" || fail "structured adverse source API group RBAC is missing"
grep -q 'resources: \["releases"\]' "${tmp_dir}/controller-rbac-adverse-source.yaml" || fail "structured adverse source resource RBAC is missing"
helm template "${release}" "${chart}" \
  --set-json 'adverseSources=[{"apiVersion":"apps.example.io/v1","kind":"Release","resource":"releases","situationRef":{"name":"release-health"}}]' \
  --show-only templates/deployment.yaml >"${tmp_dir}/controller-deployment-adverse-source.yaml"
grep -Fq -- '--adverse-sources-json=' "${tmp_dir}/controller-deployment-adverse-source.yaml" || fail "structured adverse source controller flag is missing"
grep -Fq 'apps.example.io/v1' "${tmp_dir}/controller-deployment-adverse-source.yaml" || fail "structured adverse source controller flag lost its API version"
grep -Fq 'release-health' "${tmp_dir}/controller-deployment-adverse-source.yaml" || fail "structured adverse source controller flag lost its destination"
if grep -q 'external-secrets.io' "${tmp_dir}/controller-rbac.yaml"; then
  fail "ExternalSecrets RBAC rendered while externalSecrets.enabled=false"
fi
grep -q 'resources: \["events"\]' "${tmp_dir}/controller-rbac.yaml" || fail "leader-election Event RBAC is missing"
grep -q 'resources: \["storageclasses"\]' "${tmp_dir}/controller-rbac.yaml" || fail "WaitForFirstConsumer StorageClass RBAC is missing"
if grep -q 'external-secrets.io' "${root_dir}/config/rbac/role.yaml"; then
  fail "raw default RBAC must match externalSecrets.enabled=false"
fi
helm template "${release}" "${chart}" --set externalSecrets.enabled=true \
  --show-only templates/clusterrole.yaml >"${tmp_dir}/controller-rbac-external-secrets.yaml"
grep -q 'external-secrets.io' "${tmp_dir}/controller-rbac-external-secrets.yaml" || fail "ExternalSecrets RBAC was not enabled"
helm template "${release}" "${chart}" \
  --set-string runnerImages.codex=registry.example/codex@sha256:abc \
  --set-string runnerImages.openCode=registry.example/opencode@sha256:def \
  --show-only templates/deployment.yaml >"${tmp_dir}/controller-runner-images.yaml"
grep -Fq -- '--runner-image-codex=registry.example/codex@sha256:abc' "${tmp_dir}/controller-runner-images.yaml" || fail "configured runner image was not rendered"
grep -Fq -- '--runner-image-opencode=registry.example/opencode@sha256:def' "${tmp_dir}/controller-runner-images.yaml" || fail "configured OpenCode runner image was not rendered"

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
grep -q 'resources: \["pods/log"\]' "${tmp_dir}/rbac.yaml" || fail "API RBAC cannot read pod logs"
awk '/^rules:/{capture=1} capture' "${tmp_dir}/rbac.yaml" >"${tmp_dir}/rbac-rules.yaml"
cat >"${tmp_dir}/expected-rbac-rules.yaml" <<'EOF'
rules:
  - apiGroups: ["control.anvil.hazyforge.io"]
    resources: ["agentruns"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["get"]
EOF
diff -u "${tmp_dir}/expected-rbac-rules.yaml" "${tmp_dir}/rbac-rules.yaml" >/dev/null ||
  fail "API RBAC differs from the exact read-only rule contract"

# Composition library is opt-in; read expands get/list, write adds mutations.
# Secrets and AgentRun mutation verbs must never appear.
helm template "${release}" "${chart}" "${api_args[@]}" \
  --set api.config.composition.readEnabled=true \
  --set-string 'api.config.authorization.bindings[0].permissions[2]=anvil-agents:composition:read' \
  --show-only templates/api-clusterrole.yaml >"${tmp_dir}/rbac-composition-read.yaml"
grep -q 'agentskillsets' "${tmp_dir}/rbac-composition-read.yaml" || fail "composition read RBAC missing agentskillsets"
grep -q 'agentrunprofiles' "${tmp_dir}/rbac-composition-read.yaml" || fail "composition read RBAC missing agentrunprofiles"
if grep -Eq 'verbs:.*create| - create' "${tmp_dir}/rbac-composition-read.yaml"; then
  fail "composition read RBAC must not grant create"
fi
if grep -q 'secrets' "${tmp_dir}/rbac-composition-read.yaml"; then
  fail "API RBAC must never grant secrets"
fi

helm template "${release}" "${chart}" "${api_args[@]}" \
  --set api.config.composition.readEnabled=true \
  --set api.config.composition.writeEnabled=true \
  --set-string 'api.config.authorization.bindings[0].permissions[2]=anvil-agents:composition:read' \
  --set-string 'api.config.authorization.bindings[0].permissions[3]=anvil-agents:composition:write' \
  --show-only templates/api-clusterrole.yaml >"${tmp_dir}/rbac-composition-write.yaml"
grep -q ' - create' "${tmp_dir}/rbac-composition-write.yaml" || fail "composition write RBAC missing create"
grep -q ' - delete' "${tmp_dir}/rbac-composition-write.yaml" || fail "composition write RBAC missing delete"
if grep -q 'agentruns' "${tmp_dir}/rbac-composition-write.yaml" && grep -A6 'resources: \["agentruns"\]' "${tmp_dir}/rbac-composition-write.yaml" | grep -q create; then
  fail "API RBAC must not grant AgentRun create"
fi

expect_template_failure composition-write-without-read \
  --set api.config.composition.writeEnabled=true
expect_template_failure composition-permission-without-flag \
  --set-string 'api.config.authorization.bindings[0].permissions[2]=anvil-agents:composition:read'

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
expect_template_failure shared-service-account \
  --set-string serviceAccount.name=shared-agents \
  --set-string api.serviceAccount.name=shared-agents

helm template "${release}" "${chart}" "${api_args[@]}" \
  --set-string api.oidcCA.configMapName=issuer-ca \
  --set-string api.oidcCA.restartToken=2026-07-17 \
  --set-string api.config.oidc.caFile=/etc/anvil-agents-api/oidc-ca/ca.crt \
  >"${tmp_dir}/ca.yaml"
grep -q 'control.anvil.hazyforge.io/oidc-ca-restart: "2026-07-17"' "${tmp_dir}/ca.yaml" || fail "CA restart token was not rendered"

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
