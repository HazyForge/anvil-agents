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
	--output "${tmp_dir}/tagged" \
	--image-prefix registry.example.com/team >/dev/null
helm show values "${tmp_dir}/tagged/anvil-agents-0.2.3.tgz" > "${tmp_dir}/tagged-values.yaml"
for image in \
	anvil-agents \
	anvil-agent-run-codex \
	anvil-agent-run-opencode \
	anvil-agent-run-grok-build \
	anvil-agent-run-hermes \
	anvil-agent-run-openclaw \
	anvil-agent-run-pi; do
	rg -q "registry\.example\.com/team/${image}(:v0\.2\.3)?" "${tmp_dir}/tagged-values.yaml" ||
		fail "development package did not reference version-tagged ${image}"
done

digest() {
	printf '%064d' "$1"
}
lock="${tmp_dir}/images.lock.tsv"
{
	printf 'schema\tanvil-agents-image-lock/v1\n'
	printf 'source-revision\t%s\n' 0123456789abcdef0123456789abcdef01234567
	printf 'platform\tlinux/amd64\n'
	printf 'controller\tregistry.example.com/team/anvil-agents@sha256:%s\n' "$(digest 1)"
	printf 'codex\tregistry.example.com/team/anvil-agent-run-codex@sha256:%s\n' "$(digest 2)"
	printf 'opencode\tregistry.example.com/team/anvil-agent-run-opencode@sha256:%s\n' "$(digest 3)"
	printf 'grok-build\tregistry.example.com/team/anvil-agent-run-grok-build@sha256:%s\n' "$(digest 4)"
	printf 'hermes\tregistry.example.com/team/anvil-agent-run-hermes@sha256:%s\n' "$(digest 5)"
	printf 'openclaw\tregistry.example.com/team/anvil-agent-run-openclaw@sha256:%s\n' "$(digest 6)"
	printf 'pi\tregistry.example.com/team/anvil-agent-run-pi@sha256:%s\n' "$(digest 7)"
} > "${lock}"

"${repo_root}/hack/package-chart.sh" \
	--version v0.2.3 \
	--output "${tmp_dir}" \
	--image-prefix registry.example.com/team \
	--image-lock "${lock}" \
	--source-revision 0123456789abcdef0123456789abcdef01234567 >/dev/null

chart="${tmp_dir}/anvil-agents-0.2.3.tgz"
[[ -f "${chart}" ]] || fail "did not create ${chart}"

helm show chart "${chart}" >"${tmp_dir}/chart.yaml"
rg -q '^version: 0\.2\.3$' "${tmp_dir}/chart.yaml" || fail "chart version was not updated"
rg -q '^appVersion: v0\.2\.3$' "${tmp_dir}/chart.yaml" || fail "appVersion was not updated"

helm show values "${chart}" >"${tmp_dir}/values.yaml"
for row in \
	"anvil-agents $(digest 1)" \
	"anvil-agent-run-codex $(digest 2)" \
	"anvil-agent-run-opencode $(digest 3)" \
	"anvil-agent-run-grok-build $(digest 4)" \
	"anvil-agent-run-hermes $(digest 5)" \
	"anvil-agent-run-openclaw $(digest 6)" \
	"anvil-agent-run-pi $(digest 7)"; do
	read -r image expected_digest <<<"${row}"
	rg -q "registry\.example\.com/team/${image}@sha256:${expected_digest}" "${tmp_dir}/values.yaml" ||
		fail "packaged values did not digest-pin ${image}"
done

rg -q '^      reference: ""$' "${tmp_dir}/values.yaml" ||
	fail "image lock replaced the standalone PostgreSQL image reference"
helm template test "${chart}" \
	--namespace anvil-agents-system \
	--show-only templates/archive-standalone-statefulset.yaml \
	--set archive.mode=standalone \
	--set archive.standalone.auth.generate=true \
	>"${tmp_dir}/standalone.yaml"
rg -q 'image: "postgres:17-alpine"' "${tmp_dir}/standalone.yaml" ||
	fail "digest-locked chart did not preserve the standalone PostgreSQL image"
if rg -q "image: \"registry\.example\.com/team/anvil-agents@sha256:$(digest 1)\"" "${tmp_dir}/standalone.yaml"; then
	fail "digest-locked chart used the controller image for standalone PostgreSQL"
fi

bad_lock="${tmp_dir}/bad.lock.tsv"
sed 's#registry.example.com/team/anvil-agent-run-pi#registry.example.com/other/anvil-agent-run-pi#' "${lock}" > "${bad_lock}"
if "${repo_root}/hack/package-chart.sh" --version v0.2.3 --output "${tmp_dir}/bad" \
	--image-prefix registry.example.com/team --image-lock "${bad_lock}" \
	--source-revision 0123456789abcdef0123456789abcdef01234567 >"${tmp_dir}/bad.out" 2>&1; then
	fail "accepted an image lock from a different repository prefix"
fi

missing_schema="${tmp_dir}/missing-schema.lock.tsv"
sed '/^schema\t/d' "${lock}" > "${missing_schema}"
if "${repo_root}/hack/package-chart.sh" --version v0.2.3 --output "${tmp_dir}/missing-schema" \
	--image-prefix registry.example.com/team --image-lock "${missing_schema}" \
	--source-revision 0123456789abcdef0123456789abcdef01234567 >/dev/null 2>&1; then
	fail "accepted an image lock without its schema"
fi

missing_platform="${tmp_dir}/missing-platform.lock.tsv"
sed '/^platform\t/d' "${lock}" > "${missing_platform}"
if "${repo_root}/hack/package-chart.sh" --version v0.2.3 --output "${tmp_dir}/missing-platform" \
	--image-prefix registry.example.com/team --image-lock "${missing_platform}" \
	--source-revision 0123456789abcdef0123456789abcdef01234567 >/dev/null 2>&1; then
	fail "accepted an image lock without its platform"
fi

if "${repo_root}/hack/package-chart.sh" --version v0.2.3 --output "${tmp_dir}/wrong-revision" \
	--image-prefix registry.example.com/team --image-lock "${lock}" \
	--source-revision fedcba9876543210fedcba9876543210fedcba98 >/dev/null 2>&1; then
	fail "accepted an image lock for a different source revision"
fi

echo "package-chart contract tests passed"
