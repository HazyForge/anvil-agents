#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "${test_root}"' EXIT

# shellcheck source=../docker/agent-run-common/capabilities.sh
source "${repo_root}/docker/agent-run-common/capabilities.sh"

workdir="${test_root}/work"
mkdir -p "${workdir}"
setup_file="${test_root}/setup.sh"
tool_manifest="${test_root}/tools.json"
mcp_manifest="${test_root}/mcp.json"
printf '%s\n' 'export CAPABILITY_SETUP_EXPORT=preserved' > "${setup_file}"
printf '%s\n' '[]' > "${tool_manifest}"
printf '%s\n' '[]' > "${mcp_manifest}"

capability_invocations="${test_root}/invocations"
fake_capabilities() {
	printf '%s\n' "$*" >> "${capability_invocations}"
	if [[ "${FAIL_CAPABILITY_PHASE:-}" == "$1 $2" ]]; then
		return 19
	fi
}

export ANVIL_AGENT_CAPABILITIES_COMMAND=fake_capabilities
export ANVIL_AGENT_RUN_TOOL_SETUP_FILES="${setup_file}"
export ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE="${tool_manifest}"
export ANVIL_AGENT_RUN_MCP_MANIFEST_FILE="${mcp_manifest}"
export ANVIL_AGENT_TOOL_CACHE_ROOT="${test_root}/cache"
export ANVIL_AGENT_TOOL_INSTALL_ROOT="${test_root}/install"
export ANVIL_AGENT_TOOL_BIN_DIR="${test_root}/bin"
anvil_prepare_capabilities "${workdir}" codex

[[ "${CAPABILITY_SETUP_EXPORT:-}" == preserved ]] || {
	echo "sourced setupScript export did not survive capability bootstrap" >&2
	exit 1
}
[[ ":${PATH}:" == *":${test_root}/bin:"* ]] || {
	echo "per-run tool bin directory was not added to PATH" >&2
	exit 1
}
for invocation in "tools install" "tools verify" "mcp preflight" "mcp configure"; do
	rg -q "^${invocation} " "${capability_invocations}" || {
		echo "missing capability invocation: ${invocation}" >&2
		exit 1
	}
done

for phase in "tools install" "tools verify" "mcp preflight" "mcp configure"; do
	if FAIL_CAPABILITY_PHASE="${phase}" anvil_prepare_capabilities "${workdir}" codex >/dev/null 2>&1; then
		echo "capability bootstrap ignored ${phase} failure" >&2
		exit 1
	fi
done

# A canonical manifest owns verification. The legacy JSON compatibility path
# must not execute the same command a second time.
export ANVIL_AGENT_RUN_TOOLS_JSON='[{"name":"duplicate","verifyCommand":["false"]}]'
anvil_prepare_capabilities "${workdir}" codex >/dev/null
unset ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE
if anvil_prepare_capabilities "${workdir}" codex >/dev/null 2>&1; then
	echo "legacy tool verification did not run when the canonical manifest was absent" >&2
	exit 1
fi
export ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE="${tool_manifest}"
unset ANVIL_AGENT_RUN_TOOLS_JSON

for entrypoint in "${repo_root}"/docker/agent-run-*/entrypoint.sh; do
	bash -n "${entrypoint}"
	rg -q '^source /opt/anvil-agent-run/lib/capabilities\.sh$' "${entrypoint}" || {
		echo "runner does not source shared capabilities: ${entrypoint}" >&2
		exit 1
	}
	rg -q '^anvil_prepare_capabilities ' "${entrypoint}" || {
		echo "runner does not invoke capability preflight: ${entrypoint}" >&2
		exit 1
	}
	if rg -q '^run_tool_setup\(\)' "${entrypoint}"; then
		echo "runner still embeds duplicated tool setup: ${entrypoint}" >&2
		exit 1
	fi
done

declare -A backend_start_patterns=(
	[codex]='^codex "\$\{codex_args\[@\]\}"'
	[grok-build]='^"\$\{grok_command\[@\]\}"'
	[hermes]='^anvil-hermes-query "\$\{combined_prompt\}"'
	[openclaw]='^openclaw "\$\{openclaw_args\[@\]\}"'
	[opencode]='^opencode "\$\{opencode_args\[@\]\}"'
	[pi]='^pi "\$\{pi_args\[@\]\}"'
)
for runner in codex grok-build hermes openclaw opencode pi; do
	entrypoint="${repo_root}/docker/agent-run-${runner}/entrypoint.sh"
	preflight_line="$(rg -n '^anvil_prepare_capabilities ' "${entrypoint}" | cut -d: -f1)"
	backend_line="$(rg -n "${backend_start_patterns[${runner}]}" "${entrypoint}" | tail -n1 | cut -d: -f1)"
	if [[ -z "${preflight_line}" || -z "${backend_line}" || "${preflight_line}" -ge "${backend_line}" ]]; then
		echo "capability preflight is not before backend start: ${entrypoint}" >&2
		exit 1
	fi
done

for dockerfile in "${repo_root}"/docker/agent-run-*/Dockerfile; do
	rg -q 'agent-run-common/capabilities\.sh' "${dockerfile}" || {
		echo "runner image omits shared capability shell: ${dockerfile}" >&2
		exit 1
	}
	rg -q '/out/anvil-agent-capabilities' "${dockerfile}" || {
		echo "runner image omits capability binary: ${dockerfile}" >&2
		exit 1
	}
done

echo "Runner capability bootstrap contract passed"
