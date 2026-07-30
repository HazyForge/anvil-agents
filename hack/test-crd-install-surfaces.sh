#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fail() {
	printf 'CRD install-surface contract failed: %s\n' "$*" >&2
	exit 1
}

find "${root_dir}/config/crd/bases" -maxdepth 1 -type f -name '*.yaml' \
	-printf '%f\n' | sort >"${tmp_dir}/base-files"
[[ -s "${tmp_dir}/base-files" ]] || fail "no generated CRD base files found"
sed -n 's|^[[:space:]]*- bases/||p' "${root_dir}/config/crd/kustomization.yaml" \
	| sort >"${tmp_dir}/kustomization-files"

if ! diff -u "${tmp_dir}/base-files" "${tmp_dir}/kustomization-files"; then
	fail "config/crd/kustomization.yaml must reference every generated CRD base exactly once"
fi

awk '/^  name: [a-z0-9.-]+\.control\.anvil\.hazyforge\.io$/ { print $2 }' \
	"${root_dir}"/config/crd/bases/*.yaml | sort >"${tmp_dir}/base-names"
awk '/^  name: [a-z0-9.-]+\.control\.anvil\.hazyforge\.io$/ { print $2 }' \
	"${root_dir}/charts/anvil-agents/templates/crds.yaml" | sort >"${tmp_dir}/chart-names"

if ! diff -u "${tmp_dir}/base-names" "${tmp_dir}/chart-names"; then
	fail "generated CRD bases and the Helm CRD template must contain the same CRD names"
fi

[[ -s "${tmp_dir}/base-names" ]] || fail "no CRDs found"
