#!/usr/bin/env bash
set -euo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
mkdir -p "${tmp_dir}/dist" "${tmp_dir}/bin"
export FAKE_LOG="${tmp_dir}/commands.log"

fail() {
	echo "local-release test failed: $*" >&2
	exit 1
}

cat > "${tmp_dir}/dist/images-v9.8.7.lock.tsv" <<'EOF'
schema	anvil-agents-image-lock/v1
source-revision	0123456789abcdef0123456789abcdef01234567
platform	linux/amd64
controller	ghcr.io/hazyforge/anvil-agents@sha256:1111111111111111111111111111111111111111111111111111111111111111
codex	ghcr.io/hazyforge/anvil-agent-run-codex@sha256:2222222222222222222222222222222222222222222222222222222222222222
opencode	ghcr.io/hazyforge/anvil-agent-run-opencode@sha256:3333333333333333333333333333333333333333333333333333333333333333
grok-build	ghcr.io/hazyforge/anvil-agent-run-grok-build@sha256:4444444444444444444444444444444444444444444444444444444444444444
hermes	ghcr.io/hazyforge/anvil-agent-run-hermes@sha256:5555555555555555555555555555555555555555555555555555555555555555
openclaw	ghcr.io/hazyforge/anvil-agent-run-openclaw@sha256:6666666666666666666666666666666666666666666666666666666666666666
pi	ghcr.io/hazyforge/anvil-agent-run-pi@sha256:7777777777777777777777777777777777777777777777777777777777777777
EOF
: > "${tmp_dir}/dist/anvil-agents-9.8.7.tgz"

cat > "${tmp_dir}/deploy.yaml" <<'EOF'
helmChartPath: charts/anvil-agents
image:
  reference: ghcr.io/hazyforge/anvil-agents@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  pullPolicy: IfNotPresent
runnerImages:
  codex: ghcr.io/hazyforge/anvil-agent-run-codex@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  openCode: ghcr.io/hazyforge/anvil-agent-run-opencode@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  hermesAgent: ghcr.io/hazyforge/anvil-agent-run-hermes@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  openClaw: ghcr.io/hazyforge/anvil-agent-run-openclaw@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  grokBuild: ghcr.io/hazyforge/anvil-agent-run-grok-build@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  piAgent: ghcr.io/hazyforge/anvil-agent-run-pi@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
crds:
  install: true
EOF

"${source_root}/hack/pin-deploy-values-from-lock.sh" \
	--image-lock "${tmp_dir}/dist/images-v9.8.7.lock.tsv" \
	--values "${tmp_dir}/deploy.yaml" >/dev/null
for digest in 1111111111111111111111111111111111111111111111111111111111111111 \
	2222222222222222222222222222222222222222222222222222222222222222 \
	3333333333333333333333333333333333333333333333333333333333333333 \
	4444444444444444444444444444444444444444444444444444444444444444 \
	5555555555555555555555555555555555555555555555555555555555555555 \
	6666666666666666666666666666666666666666666666666666666666666666 \
	7777777777777777777777777777777777777777777777777777777777777777; do
	rg -q "${digest}" "${tmp_dir}/deploy.yaml" || fail "deploy values missing digest ${digest}"
done

tag_repo="${tmp_dir}/tag-repo"
mkdir -p "${tag_repo}/hack"
cp "${source_root}/hack/ensure-release-tag.sh" "${tag_repo}/hack/ensure-release-tag.sh"
git -C "${tag_repo}" init -b master >/dev/null
git -C "${tag_repo}" config user.name "Release Test"
git -C "${tag_repo}" config user.email "release-test@example.invalid"
printf '%s\n' test > "${tag_repo}/README.md"
git -C "${tag_repo}" add README.md hack/ensure-release-tag.sh
git -C "${tag_repo}" commit -m initial >/dev/null
tag_head="$(git -C "${tag_repo}" rev-parse HEAD)"
"${tag_repo}/hack/ensure-release-tag.sh" --version v9.8.7 >/dev/null
[[ "$(git -C "${tag_repo}" rev-parse v9.8.7^{commit})" == "${tag_head}" ]] ||
	fail "release tag was not created at HEAD"
"${tag_repo}/hack/ensure-release-tag.sh" --version v9.8.7 >/dev/null
printf '%s\n' dirty > "${tag_repo}/README.md"
if "${tag_repo}/hack/ensure-release-tag.sh" --version v9.8.8 >"${tmp_dir}/dirty-tag.out" 2>&1; then
	fail "release tag accepted a dirty worktree"
fi
rg -q 'dirty worktree' "${tmp_dir}/dirty-tag.out" || fail "dirty-tag error is unclear"
git -C "${tag_repo}" checkout -- README.md

cat > "${tmp_dir}/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >> "${FAKE_LOG}"
if [[ "$1 $2" == "release view" ]]; then
	[[ "${FAKE_RELEASE_EXISTS:-false}" == "true" ]]
	exit $?
fi
EOF
cat > "${tmp_dir}/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
	*"remote get-url origin"*) printf '%s\n' git@github.com:HazyForge/anvil-agents.git ;;
	*"rev-parse -q --verify refs/tags/v9.8.7^{commit}"*) printf '%s\n' 0123456789abcdef0123456789abcdef01234567 ;;
	*) printf 'unexpected git %s\n' "$*" >&2; exit 1 ;;
esac
EOF
chmod 0755 "${tmp_dir}/bin/gh" "${tmp_dir}/bin/git"
export PATH="${tmp_dir}/bin:${PATH}"

"${source_root}/hack/create-github-release.sh" \
	--version v9.8.7 \
	--output "${tmp_dir}/dist" \
	--chart-registry oci://ghcr.io/hazyforge/charts >/dev/null
rg -q '^gh release create v9.8.7 ' "${FAKE_LOG}" || fail "release create was not called"
rg -q -- '--target 0123456789abcdef0123456789abcdef01234567' "${FAKE_LOG}" ||
	fail "release target was not the lock revision"
rg -q -- '--verify-tag' "${FAKE_LOG}" || fail "remote tag verification was not required"
rg -q "${tmp_dir}/dist/anvil-agents-9.8.7.tgz" "${FAKE_LOG}" || fail "chart asset was not uploaded"
rg -q "${tmp_dir}/dist/images-v9.8.7.lock.tsv" "${FAKE_LOG}" || fail "lock asset was not uploaded"

if FAKE_RELEASE_EXISTS=true "${source_root}/hack/create-github-release.sh" \
	--version v9.8.7 --output "${tmp_dir}/dist" >"${tmp_dir}/exists.out" 2>&1; then
	fail "existing release was updated without explicit permission"
fi
rg -q 'already exists' "${tmp_dir}/exists.out" || fail "existing-release error is unclear"

: > "${FAKE_LOG}"
FAKE_RELEASE_EXISTS=true "${source_root}/hack/create-github-release.sh" \
	--version v9.8.7 \
	--output "${tmp_dir}/dist" \
	--repo HazyForge/anvil-agents \
	--update-existing >/dev/null
rg -q '^gh release edit v9.8.7 ' "${FAKE_LOG}" || fail "existing release was not edited"
rg -q '^gh release upload v9.8.7 .* --clobber' "${FAKE_LOG}" || fail "existing release assets were not clobbered"

echo "local-release contract tests passed"
