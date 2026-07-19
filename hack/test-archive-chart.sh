#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root_dir}/charts/anvil-agents"
release="archive-contract"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fail() {
  printf 'archive chart contract failed: %s\n' "$*" >&2
  exit 1
}

expect_template_failure() {
  local name="$1"
  shift
  if helm template "${release}" "${chart}" "$@" >"${tmp_dir}/${name}.out" 2>"${tmp_dir}/${name}.err"; then
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
        if (in_metadata && lines[i] ~ /^  name: /) {
          name=substr(lines[i], 9)
          gsub(/^"|"$/, "", name)
          in_metadata=0
        }
      }
      if (kind == expected_kind && name == expected_name) { found=1 }
    }
    END { exit(found ? 0 : 1) }
  ' "${file}"
}

helm template "${release}" "${chart}" >"${tmp_dir}/disabled.yaml"
if grep -q 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/disabled.yaml"; then
  fail "disabled mode rendered the archive database environment variable"
fi
if grep -Fq -- '--terminal-retention=' "${tmp_dir}/disabled.yaml"; then
  fail "disabled mode rendered terminal retention"
fi
for kind in StatefulSet Cluster; do
  if grep -Eq "^kind: ${kind}$" "${tmp_dir}/disabled.yaml"; then
    fail "disabled mode rendered ${kind}"
  fi
done

helm template "${release}" "${chart}" \
  --set archive.mode=external \
  --set-string archive.external.databaseURLSecret.name=managed-archive \
  --set-string archive.external.databaseURLSecret.key=uri \
  --set-string archive.restartToken=rotation-2 \
  >"${tmp_dir}/external.yaml"
helm lint "${chart}" \
  --set archive.mode=external \
  --set-string archive.external.databaseURLSecret.name=managed-archive \
  --set-string archive.external.databaseURLSecret.key=uri \
  >/dev/null
grep -q 'control.anvil.hazyforge.io/archive-restart: "rotation-2"' "${tmp_dir}/external.yaml" || fail "archive restart token was not rendered"
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/external.yaml" | grep -Eq 'name: "?managed-archive"?' || fail "external Secret name was not rendered"
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/external.yaml" | grep -Eq 'key: "?uri"?' || fail "external Secret key was not rendered"
if grep -Eq '^kind: (StatefulSet|Cluster)$' "${tmp_dir}/external.yaml"; then
  fail "external mode rendered a database workload"
fi

helm template "${release}" "${chart}" \
  --set-string archive.databaseURLSecret.name=legacy-archive \
  --set-string archive.databaseURLSecret.key=url \
  >"${tmp_dir}/legacy.yaml"
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/legacy.yaml" | grep -Eq 'name: "?legacy-archive"?' || fail "legacy Secret alias no longer enables external archive mode"

standalone_name="${release}-anvil-agents-archive-postgres"
helm template "${release}" "${chart}" \
  --set archive.mode=standalone \
  --set-string archive.standalone.auth.existingSecret=standalone-archive \
  >"${tmp_dir}/standalone-existing.yaml"
helm lint "${chart}" \
  --set archive.mode=standalone \
  --set-string archive.standalone.auth.existingSecret=standalone-archive \
  >/dev/null
document_exists "${tmp_dir}/standalone-existing.yaml" Service "${standalone_name}" || fail "standalone Service was not rendered"
document_exists "${tmp_dir}/standalone-existing.yaml" StatefulSet "${standalone_name}" || fail "standalone StatefulSet was not rendered"
document_exists "${tmp_dir}/standalone-existing.yaml" PersistentVolumeClaim "${standalone_name}" || fail "standalone PVC was not rendered"
if document_exists "${tmp_dir}/standalone-existing.yaml" Secret "${standalone_name}"; then
  fail "chart generated a Secret despite standalone.auth.existingSecret"
fi
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/standalone-existing.yaml" | grep -Eq 'name: "?standalone-archive"?' || fail "controller does not consume the standalone existing Secret"
grep -A5 'name: POSTGRES_PASSWORD' "${tmp_dir}/standalone-existing.yaml" | grep -Eq 'name: "?standalone-archive"?' || fail "PostgreSQL does not consume the standalone existing Secret"

helm template "${release}" "${chart}" \
  --set archive.mode=standalone \
  --set archive.standalone.auth.generate=true \
  >"${tmp_dir}/standalone-generated.yaml"
document_exists "${tmp_dir}/standalone-generated.yaml" Secret "${standalone_name}" || fail "standalone generated Secret was not rendered"
grep -A12 "name: ${standalone_name}" "${tmp_dir}/standalone-generated.yaml" | grep -q 'helm.sh/resource-policy: keep' || fail "standalone credential Secret is not retained"
grep -A12 "name: ${standalone_name}" "${tmp_dir}/standalone-generated.yaml" | grep -q 'argocd.argoproj.io/sync-options: Prune=false' || fail "standalone credential Secret is not protected from Argo pruning"
[[ "$(grep -c 'argocd.argoproj.io/sync-options: Prune=false,Delete=false' "${tmp_dir}/standalone-generated.yaml")" -eq 2 ]] || fail "standalone Secret and PVC must each have one Argo retention annotation"
helm template "${release}" "${chart}" \
  --set archive.mode=standalone \
  --set-string archive.standalone.auth.existingSecret=standalone-archive \
  --set-string archive.standalone.image.reference=registry.example/postgres@sha256:abc \
  --show-only templates/archive-standalone-statefulset.yaml \
  >"${tmp_dir}/standalone-image-reference.yaml"
grep -q 'image: "registry.example/postgres@sha256:abc"' "${tmp_dir}/standalone-image-reference.yaml" || fail "standalone immutable image reference was not rendered"

cnpg_name="${release}-anvil-agents-archive-cnpg"
helm template "${release}" "${chart}" \
  --api-versions postgresql.cnpg.io/v1/Cluster \
  --set archive.mode=cloudnativepg \
  >"${tmp_dir}/cloudnativepg.yaml"
document_exists "${tmp_dir}/cloudnativepg.yaml" Cluster "${cnpg_name}" || fail "CloudNativePG Cluster was not rendered"
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/cloudnativepg.yaml" | grep -Eq "name: \"?${cnpg_name}-app\"?" || fail "controller does not consume the CNPG application Secret"
grep -A5 'ANVIL_AGENTS_ARCHIVE_DATABASE_URL' "${tmp_dir}/cloudnativepg.yaml" | grep -Eq 'key: "?uri"?' || fail "controller does not consume the CNPG URI key"
grep -E -A12 "name: \"?${cnpg_name}\"?" "${tmp_dir}/cloudnativepg.yaml" | grep -q 'helm.sh/resource-policy: keep' || fail "CNPG Cluster is not retained"
if grep -Eq '^kind: (Secret|StatefulSet)$' "${tmp_dir}/cloudnativepg.yaml"; then
  fail "CloudNativePG mode rendered standalone credentials or workload"
fi

expect_template_failure invalid-mode --set archive.mode=mysql
expect_template_failure retention-without-archive --set-string archive.terminalRetention=24h
expect_template_failure missing-external-secret --set archive.mode=external
expect_template_failure missing-standalone-auth --set archive.mode=standalone
expect_template_failure invalid-retention \
  --set archive.mode=external \
  --set-string archive.external.databaseURLSecret.name=managed-archive \
  --set-string archive.terminalRetention=tomorrow
expect_template_failure invalid-external-secret-name \
  --set archive.mode=external \
  --set-string archive.external.databaseURLSecret.name=a..b
expect_template_failure cnpg-crd-missing --set archive.mode=cloudnativepg
expect_template_failure legacy-mode-conflict \
  --set archive.mode=standalone \
  --set-string archive.databaseURLSecret.name=legacy-archive
expect_template_failure external-secret-conflict \
  --set archive.mode=external \
  --set-string archive.databaseURLSecret.name=legacy-archive \
  --set-string archive.external.databaseURLSecret.name=managed-archive
expect_template_failure disabled-external-identity \
  --set archive.mode=disabled \
  --set-string archive.external.databaseURLSecret.name=managed-archive
expect_template_failure disabled-legacy-identity \
  --set archive.mode=disabled \
  --set-string archive.databaseURLSecret.name=legacy-archive
expect_template_failure standalone-auth-conflict \
  --set archive.mode=standalone \
  --set archive.standalone.auth.generate=true \
  --set-string archive.standalone.auth.existingSecret=standalone-archive
expect_template_failure standalone-key-conflict \
  --set archive.mode=standalone \
  --set-string archive.standalone.auth.existingSecret=standalone-archive \
  --set-string archive.standalone.auth.passwordKey=url \
  --set-string archive.standalone.auth.databaseURLKey=url

helm template "${release}" "${chart}" \
  --set archive.mode=external \
  --set-string archive.external.databaseURLSecret.name=null \
  --show-only templates/deployment.yaml \
  >"${tmp_dir}/quoted-external-name.yaml"
grep -q 'name: "null"' "${tmp_dir}/quoted-external-name.yaml" || fail "external Secret name is not YAML-safe"
helm template "${release}" "${chart}" \
  --api-versions postgresql.cnpg.io/v1/Cluster \
  --set archive.mode=cloudnativepg \
  --set-string archive.cloudnativePG.clusterName=true \
  --show-only templates/archive-cloudnativepg.yaml \
  >"${tmp_dir}/quoted-cnpg-name.yaml"
grep -q 'name: "true"' "${tmp_dir}/quoted-cnpg-name.yaml" || fail "CNPG Cluster name is not YAML-safe"

long_fullname="anvil-agents-release-with-a-name-long-enough-to-test-truncation-123456789"
helm template "${release}" "${chart}" \
  --set archive.mode=standalone \
  --set-string archive.standalone.auth.existingSecret=standalone-archive \
  --set-string fullnameOverride="${long_fullname}" \
  >"${tmp_dir}/standalone-long-name.yaml"
long_archive_name="$(awk '
  BEGIN { RS="---" }
  /^\n?# Source: anvil-agents\/templates\/archive-standalone-statefulset.yaml/ {
    for (i=1; i<=NF; i++) if ($i == "name:") { print $(i+1); exit }
  }
' "${tmp_dir}/standalone-long-name.yaml")"
[[ -n "${long_archive_name}" ]] || fail "standalone long archive name was not found"
[[ "${#long_archive_name}" -le 59 ]] || fail "standalone archive name leaves no room for StatefulSet pod ordinal"
mapfile -t long_service_names < <(awk '
  BEGIN { RS="---" }
  {
    kind=""; name=""; in_metadata=0
    count=split($0, lines, "\n")
    for (i=1; i<=count; i++) {
      if (lines[i] ~ /^kind: /) { kind=substr(lines[i], 7) }
      if (lines[i] == "metadata:") { in_metadata=1; continue }
      if (in_metadata && lines[i] ~ /^  name: /) {
        name=substr(lines[i], 9)
        gsub(/^"|"$/, "", name)
        in_metadata=0
      }
    }
    if (kind == "Service") { print name }
  }
' "${tmp_dir}/standalone-long-name.yaml")
[[ "${#long_service_names[@]}" -eq 2 ]] || fail "expected controller and standalone Services for long fullname"
[[ "${long_service_names[0]}" != "${long_service_names[1]}" ]] || fail "standalone archive Service collides with controller Service"
printf '%s\n' "${long_service_names[@]}" | grep -Fxq "${long_archive_name}" || fail "standalone StatefulSet and Service names do not match"

printf 'AgentRun archive chart contract passed\n'
