#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
mkdir -p "${test_dir}/bin" "${test_dir}/work" "${test_dir}/home"
cat > "${test_dir}/bin/agy" <<'AGY'
#!/usr/bin/env bash
set -euo pipefail
test -z "${ANVIL_AGY_NATIVE_OAUTH_TOKEN+x}" || exit 19
printf '%s\n' "$@" > "${TEST_DIR}/argv"
IFS= read -r prompt
printf '%s\n' "${prompt}" > "${TEST_DIR}/input"
printf '%s\n' "${AGY_TEST_OUTPUT:-{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"OK\"}}}"
exit "${AGY_TEST_EXIT:-0}"
AGY
cat > "${test_dir}/bin/anvil-agent-status" <<'STATUS'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${TEST_DIR}/status"
STATUS
chmod +x "${test_dir}/bin/agy" "${test_dir}/bin/anvil-agent-status"
printf '%s\n' 'A multiline prompt with "quotes" and $(literal shell) text.' 'Second line.' > "${test_dir}/prompt"
printf '%s\n' 'test -z "${ANVIL_AGY_NATIVE_OAUTH_TOKEN+x}"; export AGY_TOOL_READY=yes' > "${test_dir}/setup"
run_adapter() {
 env -i PATH="${test_dir}/bin:${PATH}" TEST_DIR="${test_dir}" \
  ANVIL_AGY_USER_HOME="${test_dir}/home" ANVIL_AGY_WORKDIR="${test_dir}/work" \
  ANVIL_AGENT_RUN_LIB_DIR="${repo_root}/docker/agent-run-common" \
  ANVIL_AGY_TURN_DRIVER="${repo_root}/docker/agent-run-agy/turn.py" \
  ANVIL_AGENT_RUN_STATUS_FILE="${test_dir}/runner-status" \
  ANVIL_AGENT_RUN_PROMPT_FILE="${test_dir}/prompt" \
  ANVIL_AGENT_RUN_TOOL_SETUP_FILES="${test_dir}/setup" \
  ANVIL_AGENT_RUN_TOOLS_JSON='[{"name":"setup-check","verifyCommand":["bash","-c","test \"$AGY_TOOL_READY\" = yes"]}]' \
  "$@" bash "${repo_root}/docker/agent-run-agy/entrypoint.sh" > "${test_dir}/out" 2>&1
}
run_adapter ANVIL_AGY_NATIVE_OAUTH_TOKEN=fixture-native-token ANVIL_AGY_MODEL=example-model ANVIL_AGY_THINKING=high
jq -e --rawfile prompt "${test_dir}/prompt" '.event == "user" and (.message.content | contains($prompt))' "${test_dir}/input" >/dev/null
rg -Fxq -- --effort "${test_dir}/argv"
rg -Fxq -- --input-format "${test_dir}/argv"
if rg -q -- '^-p$|^--thinking$|literal shell' "${test_dir}/argv"; then echo 'prompt or unsupported flags reached argv' >&2; exit 1; fi
rg -q '^complete ' "${test_dir}/status"
native_file="${test_dir}/home/.gemini/antigravity-cli/antigravity-oauth-token"
test "$(cat "${native_file}")" = fixture-native-token
test "$(stat -c %a "${native_file}")" = 600
printf '%s' refreshed-native-token > "${native_file}"
run_adapter ANVIL_AGY_NATIVE_OAUTH_TOKEN=stale-bootstrap-token
test "$(cat "${native_file}")" = refreshed-native-token
for mode in print text stream-json; do run_adapter ANVIL_AGY_MODE="${mode}"; done
if run_adapter ANVIL_AGY_THINKING=invalid; then echo 'invalid effort accepted' >&2; exit 1; fi
if run_adapter ANVIL_AGY_MODE=invalid; then echo 'invalid mode accepted' >&2; exit 1; fi
if run_adapter AGY_AUTH_JSON=unsupported; then echo 'invented auth accepted' >&2; exit 1; fi
if run_adapter AGY_TEST_OUTPUT='{"event":"result","result":{"status":"ERROR"}}'; then echo 'failed result accepted' >&2; exit 1; fi
if run_adapter AGY_TEST_OUTPUT='{"event":"init"}'; then echo 'missing result accepted' >&2; exit 1; fi
if run_adapter AGY_TEST_EXIT=17; then echo 'nonzero exit accepted' >&2; exit 1; else test "$?" -eq 17; fi
# Exercise repository/ref checkout with the shared implementation and a local repo.
git init -q "${test_dir}/source"
git -C "${test_dir}/source" -c user.name=Test -c user.email=test@example.invalid commit -q --allow-empty -m initial
revision="$(git -C "${test_dir}/source" rev-parse HEAD)"
run_adapter ANVIL_AGENT_RUN_REPOSITORY_URL="file://${test_dir}/source" ANVIL_AGENT_RUN_REPOSITORY_REF="${revision}"
test "$(git -C "${test_dir}/work" rev-parse HEAD)" = "${revision}"
printf '%s\n' 'AGY entrypoint contract tests passed'
