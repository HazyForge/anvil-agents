#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
mkdir -p "${test_dir}/bin" "${test_dir}/work" "${test_dir}/user-home" "${test_dir}/hermes home" "${test_dir}/codex" "${test_dir}/tmp"
# Resolve only the image's fixed library paths to the same checked-in libraries.
# The actual entrypoint and its tool setup/harness execution run unchanged.
cat > "${test_dir}/test-env" <<'ENV'
source() {
 case "$1" in
  /opt/anvil-agent-run/lib/github-auth.sh) builtin source "${TEST_REPO}/docker/agent-run-common/github-auth.sh" ;;
  /opt/anvil-agent-run/lib/repository-checkout.sh) builtin source "${TEST_REPO}/docker/agent-run-common/repository-checkout.sh" ;;
  *) builtin source "$@" ;;
 esac
}
ENV
cat > "${test_dir}/bin/anvil-hermes-query" <<'HARNESS'
#!/usr/bin/env bash
set -euo pipefail
test "${GOCACHE}" = "${EXPECTED_BUILD}"
test "${GOMODCACHE}" = "${EXPECTED_MODULES}"
test "${CACHE_SETUP_RAN:-}" = yes
printf '%s\n%s\n' "${GOCACHE}" "${GOMODCACHE}" > "${TEST_DIR}/harness-cache"
HARNESS
for tool in hermes anvil-agent-status; do
 printf '#!/usr/bin/env bash\nexit 0\n' > "${test_dir}/bin/${tool}"
done
chmod +x "${test_dir}/bin/"*
cat > "${test_dir}/setup" <<'SETUP'
# A separate child observes exported values before any harness invocation.
bash -c 'test "$GOCACHE" = "$EXPECTED_BUILD" && test "$GOMODCACHE" = "$EXPECTED_MODULES"'
if [[ "${EXPECT_DEFAULT:-}" = yes ]]; then
 test -d "${GOCACHE}"
 test -d "${GOMODCACHE}"
 if [[ "${EXPECT_REUSE:-}" = yes ]]; then
  test -f "${GOCACHE}/previous-turn"
  test -f "${GOMODCACHE}/previous-turn"
 fi
 touch "${GOCACHE}/previous-turn" "${GOMODCACHE}/previous-turn"
fi
export CACHE_SETUP_RAN=yes
SETUP
run_entrypoint() {
 env -i PATH="${test_dir}/bin:${PATH}" HOME="${test_dir}/user-home" TMPDIR="${test_dir}/tmp" \
  TEST_DIR="${test_dir}" TEST_REPO="${repo_dir}" BASH_ENV="${test_dir}/test-env" \
  HERMES_HOME="${test_dir}/hermes home" CODEX_HOME="${test_dir}/codex" \
  ANVIL_AGENT_RUN_WORKDIR="${test_dir}/work" ANVIL_AGENT_RUN_AUTO_CLONE_REPO=false \
  ANVIL_AGENT_RUN_STATUS_FILE="${test_dir}/status" ANVIL_AGENT_RUN_TOOL_SETUP_FILES="${test_dir}/setup" \
  ANVIL_AGENT_RUN_AGENTS_FILE="${test_dir}/absent" ANVIL_AGENT_RUN_SKILLS_DIR="${test_dir}/absent" \
  "$@" bash "${repo_dir}/docker/agent-run-hermes/entrypoint.sh" > "${test_dir}/output" 2>&1 || { cat "${test_dir}/output" >&2; return 1; }
 test -f "${test_dir}/harness-cache"
}
default_build="${test_dir}/hermes home/cache/go-build"
default_modules="${test_dir}/hermes home/cache/go-mod"
run_entrypoint EXPECT_DEFAULT=yes EXPECTED_BUILD="${default_build}" EXPECTED_MODULES="${default_modules}"
run_entrypoint GOCACHE= GOMODCACHE= EXPECT_DEFAULT=yes EXPECT_REUSE=yes EXPECTED_BUILD="${default_build}" EXPECTED_MODULES="${default_modules}"
run_entrypoint GOCACHE="${test_dir}/custom build" GOMODCACHE="${test_dir}/custom modules" EXPECTED_BUILD="${test_dir}/custom build" EXPECTED_MODULES="${test_dir}/custom modules"
run_entrypoint GOCACHE=off GOMODCACHE="${test_dir}/explicit modules" EXPECTED_BUILD=off EXPECTED_MODULES="${test_dir}/explicit modules"
test ! -d "${test_dir}/work/off"
printf 'Hermes entrypoint persistent Go cache contract passed\n'
