#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

export FAKE_DOCKER_LOG="${tmp_dir}/docker.log"
mkdir -p "${tmp_dir}/bin"
cat > "${tmp_dir}/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FAKE_DOCKER_LOG}"
EOF
chmod 0755 "${tmp_dir}/bin/docker"
export PATH="${tmp_dir}/bin:${PATH}"

fail() {
	echo "build-images test failed: $*" >&2
	exit 1
}

if "${repo_root}/hack/build-images.sh" --tag --push >"${tmp_dir}/missing-value" 2>&1; then
	fail "accepted --tag without a value"
fi
rg -q -- '--tag requires a value' "${tmp_dir}/missing-value" || fail "did not explain the missing --tag value"

: > "${FAKE_DOCKER_LOG}"
"${repo_root}/hack/build-images.sh" \
	--component controller \
	--prefix registry.example.com/team \
	--tag first \
	--tag second \
	--platform linux/amd64 >/dev/null
local_command="$(tail -n 1 "${FAKE_DOCKER_LOG}")"
[[ "${local_command}" == build\ * ]] || fail "local mode did not use docker build"
[[ "${local_command}" == *"--file ${repo_root}/Dockerfile"* ]] || fail "controller Dockerfile mapping is wrong"
[[ "${local_command}" == *"--platform linux/amd64"* ]] || fail "platform was not forwarded"
[[ "${local_command}" == *"--tag registry.example.com/team/anvil-agents:first"* ]] || fail "first tag is missing"
[[ "${local_command}" == *"--tag registry.example.com/team/anvil-agents:second"* ]] || fail "second tag is missing"
[[ "${local_command}" == *"--label org.opencontainers.image.source=https://github.com/HazyForge/anvil-agents"* ]] || fail "Git SSH origin was not normalized for the OCI source label"

: > "${FAKE_DOCKER_LOG}"
"${repo_root}/hack/build-images.sh" --check >/dev/null
[[ "$(grep -c '^build --check ' "${FAKE_DOCKER_LOG}")" == "7" ]] || fail "all-image check did not inspect seven Dockerfiles"
for dockerfile in \
	Dockerfile \
	docker/agent-run-codex/Dockerfile \
	docker/agent-run-opencode/Dockerfile \
	docker/agent-run-grok-build/Dockerfile \
	docker/agent-run-hermes/Dockerfile \
	docker/agent-run-openclaw/Dockerfile \
	docker/agent-run-pi/Dockerfile; do
	rg -q --fixed-strings -- "--file ${repo_root}/${dockerfile}" "${FAKE_DOCKER_LOG}" || fail "missing check for ${dockerfile}"
done
rg -q 'OPENCODE_LICENSE_SHA256=625f0f619133f89bbbb2abe37369613dfa1885eba1e50d02170deb62bb42cb6b' \
	"${repo_root}/docker/agent-run-opencode/Dockerfile" || fail "OpenCode license checksum pin is missing"
rg -q '/usr/share/doc/opencode/LICENSE' \
	"${repo_root}/docker/agent-run-opencode/Dockerfile" || fail "OpenCode image does not install its upstream license"
rg -q '^\| OpenCode \| `1\.18\.3`' \
	"${repo_root}/THIRD_PARTY_NOTICES.md" || fail "OpenCode third-party notice is missing"

if "${repo_root}/hack/build-images.sh" --component controller --push >"${tmp_dir}/push-error" 2>&1; then
	fail "push without a repository prefix succeeded"
fi
rg -q --fixed-strings -- "--push requires --prefix" "${tmp_dir}/push-error" || fail "push prefix error is unclear"

: > "${FAKE_DOCKER_LOG}"
"${repo_root}/hack/build-images.sh" \
	--component pi \
	--prefix registry.example.com/team \
	--tag release \
	--cache-from local-cache \
	--cache-to remote-cache \
	--allow-dirty \
	--push >/dev/null
rg -q '^buildx version$' "${FAKE_DOCKER_LOG}" || fail "push did not verify buildx"
push_command="$(tail -n 1 "${FAKE_DOCKER_LOG}")"
[[ "${push_command}" == buildx\ build\ --push\ * ]] || fail "push mode did not use docker buildx build --push"
[[ "${push_command}" == *"--cache-from local-cache"* ]] || fail "cache source was not forwarded"
[[ "${push_command}" == *"--cache-to remote-cache"* ]] || fail "cache destination was not forwarded"
[[ "${push_command}" == *"--tag registry.example.com/team/anvil-agent-run-pi:release"* ]] || fail "Pi image mapping is wrong"

: > "${FAKE_DOCKER_LOG}"
ANVIL_AGENTS_IMAGE_SOURCE=https://code.example.com/fork/anvil-agents \
	"${repo_root}/hack/build-images.sh" --component controller --tag source-test >/dev/null
source_command="$(tail -n 1 "${FAKE_DOCKER_LOG}")"
[[ "${source_command}" == *"--label org.opencontainers.image.source=https://code.example.com/fork/anvil-agents"* ]] || fail "source label override is missing"

echo "build-images contract tests passed"
