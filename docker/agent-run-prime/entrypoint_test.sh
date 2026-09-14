#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
mkdir -p "${test_dir}/bin" "${test_dir}/work" "${test_dir}/home"
cat > "${test_dir}/bin/prime-agent" <<'PRIME'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "${TEST_DIR}/argv"
cat > "${TEST_DIR}/input"
test "${PRIME_TOOL_READY:-}" = yes
test "${PRIME_AGENT_CODING_AGENT_DIR}" = "${ANVIL_PRIME_HOME}/agent"
if [[ "${EXPECT_AUTH:-}" == yes ]]; then
  test -n "${DEEPSEEK_API_KEY:-}"
  test -z "${apiKey+x}"
fi
if [[ "${EXPECT_NATIVE:-}" == yes ]]; then test "${DEEPSEEK_API_KEY:-}" = fixture-native; fi
if [[ "${EXPECT_NO_AUTH:-}" == yes ]]; then test -z "${DEEPSEEK_API_KEY+x}"; fi
if [[ "${PRIME_TEST_SLEEP:-}" == yes ]]; then sleep 30; fi
if [[ "${PRIME_TEST_OUTPUT+x}" ]]; then printf '%s\n' "${PRIME_TEST_OUTPUT}";
else printf '%s\n' '{"type":"agent_end","messages":[{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"PRIME_READY"}]}]}'; fi
exit "${PRIME_TEST_EXIT:-0}"
PRIME
cat > "${test_dir}/bin/anvil-agent-status" <<'STATUS'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${TEST_DIR}/status"
STATUS
chmod +x "${test_dir}/bin/prime-agent" "${test_dir}/bin/anvil-agent-status"
printf '%s\n' 'Prompt "quotes" $(literal shell) & backticks `literal`.' 'Second line.' > "${test_dir}/prompt"
printf '%s\n' 'export PRIME_TOOL_READY=yes' > "${test_dir}/setup"
printf '%s\n' 'Dedicated agent instruction' > "${test_dir}/agents"
printf '%s\n' '{"intent":"proposeChange","scope":"fixture"}' > "${test_dir}/context"
run_adapter() {
 env -i PATH="${test_dir}/bin:${PATH}" TEST_DIR="${test_dir}" \
  ANVIL_PRIME_HOME="${test_dir}/home" ANVIL_PRIME_WORKDIR="${test_dir}/work" \
  ANVIL_AGENT_RUN_LIB_DIR="${repo_root}/docker/agent-run-common" \
  ANVIL_AGENT_RUN_STATUS_FILE="${test_dir}/runner-status" \
  ANVIL_AGENT_RUN_PROMPT_FILE="${test_dir}/prompt" ANVIL_AGENT_RUN_CONTEXT_FILE="${test_dir}/context" \
  ANVIL_AGENT_RUN_AGENTS_FILE="${test_dir}/agents" ANVIL_AGENT_RUN_TOOL_SETUP_FILES="${test_dir}/setup" \
  ANVIL_AGENT_RUN_TOOLS_JSON='[{"name":"setup-check","verifyCommand":["bash","-c","test \"$PRIME_TOOL_READY\" = yes"]}]' \
  "$@" bash "${repo_root}/docker/agent-run-prime/entrypoint.sh" > "${test_dir}/out" 2>&1
}
run_adapter ANVIL_PRIME_PROVIDER=deepseek ANVIL_PRIME_MODEL=deepseek-v4-flash ANVIL_PRIME_THINKING=high ANVIL_PRIME_NO_SESSION=true ANVIL_PRIME_ADDITIONAL_ARGS_JSON='["--no-extensions"]'
for arg in --print --mode json --cwd --provider deepseek --model deepseek-v4-flash --thinking high --no-session --no-extensions; do rg -Fxq -- "$arg" "${test_dir}/argv"; done
if rg -q -- 'literal shell|--no-tools|--api-key' "${test_dir}/argv"; then echo 'prompt or unintended flags in argv' >&2; exit 1; fi
python3 - "${test_dir}" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]); content=(p/'input').read_text()
assert (p/'prompt').read_text() in content
assert (p/'agents').read_text() in content
assert (p/'context').read_text() in content
PY
rg -q '^ANVIL_AGENT_RUN_COMPLETE' "${test_dir}/out"
run_adapter apiKey=fixture-generic ANVIL_AGENT_RUN_MODEL_PROVIDER=deepseek ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE=apiKey EXPECT_AUTH=yes
run_adapter apiKey=fixture-generic DEEPSEEK_API_KEY=fixture-native ANVIL_PRIME_MODEL_PROVIDER=deepseek ANVIL_PRIME_PROVIDER_AUTH_MODE=apiKey EXPECT_AUTH=yes EXPECT_NATIVE=yes
run_adapter apiKey=fixture-generic ANVIL_AGENT_RUN_MODEL_PROVIDER=other ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE=apiKey EXPECT_NO_AUTH=yes
run_adapter ANVIL_PRIME_MODE=text PRIME_TEST_OUTPUT='Native text reply'
for value in '{}' '[1]' '["--api-key","fixture"]' '["--mode","rpc"]' '["--no-tools"]' '["--model=other"]' '["--extension=/tmp/plugin.js"]' '["$(touch /tmp/never)"]'; do
 if run_adapter ANVIL_PRIME_ADDITIONAL_ARGS_JSON="$value"; then echo 'invalid extra args accepted' >&2; exit 1; fi
done
if run_adapter ANVIL_PRIME_THINKING=invalid; then echo 'invalid thinking accepted' >&2; exit 1; fi
if run_adapter ANVIL_PRIME_MODE=rpc; then echo 'unsupported RPC accepted' >&2; exit 1; fi
if run_adapter ANVIL_PRIME_TURN_TIMEOUT_SECONDS=0; then echo 'unbounded timeout accepted' >&2; exit 1; fi
if run_adapter PRIME_TEST_OUTPUT='{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"private"}]}'; then echo 'native error accepted' >&2; exit 1; fi
if run_adapter PRIME_TEST_OUTPUT='{"type":"agent_start"}'; then echo 'missing terminal accepted' >&2; exit 1; fi
if run_adapter PRIME_TEST_EXIT=17; then echo 'nonzero accepted' >&2; exit 1; else test "$?" -eq 17; fi
if run_adapter PRIME_TEST_SLEEP=yes ANVIL_PRIME_TURN_TIMEOUT_SECONDS=1; then echo 'timeout accepted' >&2; exit 1; else test "$?" -eq 124; fi
git init -q "${test_dir}/source"
git -C "${test_dir}/source" -c user.name=Test -c user.email=test@example.invalid commit -q --allow-empty -m initial
revision="$(git -C "${test_dir}/source" rev-parse HEAD)"
run_adapter ANVIL_AGENT_RUN_REPOSITORY_URL="file://${test_dir}/source" ANVIL_AGENT_RUN_REPOSITORY_REF="${revision}"
test "$(git -C "${test_dir}/work" rev-parse HEAD)" = "${revision}"
printf '%s\n' 'Prime entrypoint contract tests passed'
