#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fail() {
	echo "package-chart test failed: $*" >&2
	exit 1
}

if "${repo_root}/hack/package-chart.sh" --version >"${tmp_dir}/missing-value" 2>&1; then
	fail "accepted --version without a value"
fi
rg -q -- '--version requires a value' "${tmp_dir}/missing-value" ||
	fail "did not explain the missing --version value"

"${repo_root}/hack/package-chart.sh" \
	--version v0.2.3 \
	--output "${tmp_dir}" \
	--image-prefix registry.example.com/team >/dev/null

chart="${tmp_dir}/anvil-agents-0.2.3.tgz"
[[ -f "${chart}" ]] || fail "did not create ${chart}"

helm show chart "${chart}" >"${tmp_dir}/chart.yaml"
rg -q '^version: 0\.2\.3$' "${tmp_dir}/chart.yaml" || fail "chart version was not updated"
rg -q '^appVersion: v0\.2\.3$' "${tmp_dir}/chart.yaml" || fail "appVersion was not updated"

helm show values "${chart}" >"${tmp_dir}/values.yaml"
for image in \
	anvil-agents \
	anvil-agent-run-codex \
	anvil-agent-run-hermes \
	anvil-agent-run-openclaw \
	anvil-agent-run-grok-build \
	anvil-agent-run-pi; do
	rg -q "registry\.example\.com/team/${image}(:v0\.2\.3)?" "${tmp_dir}/values.yaml" ||
		fail "packaged values did not reference ${image}"
done
rg -q '^  tag: v0\.2\.3$' "${tmp_dir}/values.yaml" || fail "controller tag was not updated"

echo "package-chart contract tests passed"
