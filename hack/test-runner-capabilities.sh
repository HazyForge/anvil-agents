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

projection_shared="${test_root}/projection-shared"
mkdir -p \
	"${projection_shared}/codex" \
	"${projection_shared}/grok" \
	"${projection_shared}/hermes/memories" \
	"${projection_shared}/hermes/sessions" \
	"${projection_shared}/openclaw" \
	"${projection_shared}/opencode-data/opencode"
printf '%s\n' '{"tokens":"codex-auth"}' > "${projection_shared}/codex/auth.json"
printf '%s\n' 'model = "codex-shared"' > "${projection_shared}/codex/config.toml"
printf '%s\n' '{"tokens":"grok-auth"}' > "${projection_shared}/grok/auth.json"
printf '%s\n' 'model = "grok-shared"' > "${projection_shared}/grok/config.toml"
printf '%s\n' '{"tokens":"hermes-auth"}' > "${projection_shared}/hermes/auth.json"
printf '%s\n' 'HERMES_PROVIDER_TOKEN=from-durable-home' > "${projection_shared}/hermes/.env"
printf '%s\n' 'model:' '  default: hermes-shared' > "${projection_shared}/hermes/config.yaml"
printf '%s\n' 'durable-memory' > "${projection_shared}/hermes/memories/MEMORY.md"
printf '%s\n' '{"mcpServers":{}}' > "${projection_shared}/openclaw/openclaw.json"
printf '%s\n' '{"provider":"opencode-auth"}' > "${projection_shared}/opencode-data/opencode/auth.json"
chmod 0600 \
	"${projection_shared}/codex/auth.json" \
	"${projection_shared}/grok/auth.json" \
	"${projection_shared}/hermes/auth.json" \
	"${projection_shared}/hermes/.env" \
	"${projection_shared}/openclaw/openclaw.json" \
	"${projection_shared}/opencode-data/opencode/auth.json"

run_native_projection_worker() (
	local backend="$1"
	local run_name="$2"
	local result_file="$3"
	local run_root="${test_root}/projection-runs/${backend}/${run_name}"

	unset \
		ANVIL_AGENT_NATIVE_MCP_PROJECTION_ID \
		ANVIL_AGENT_SHARED_CODEX_HOME \
		ANVIL_AGENT_SHARED_GROK_HOME \
		ANVIL_AGENT_SHARED_HERMES_HOME \
		ANVIL_AGENT_SHARED_OPENCLAW_CONFIG_PATH \
		GROK_AUTH_PATH \
		OPENCODE_CONFIG \
		OPENCODE_CONFIG_CONTENT \
		OPENCODE_CONFIG_DIR \
		OPENCODE_DISABLE_PROJECT_CONFIG \
		OPENCLAW_CONFIG_PATH
	unset XDG_CONFIG_HOME
	export ANVIL_AGENT_CAPABILITIES_ROOT="${run_root}"
	export ANVIL_AGENT_TOOL_CACHE_ROOT="${run_root}/cache"
	export ANVIL_AGENT_TOOL_INSTALL_ROOT="${run_root}/install"
	export ANVIL_AGENT_TOOL_BIN_DIR="${run_root}/bin"
	case "${backend}" in
		codex)
			export CODEX_HOME="${projection_shared}/codex"
			;;
		grokBuild)
			export GROK_HOME="${projection_shared}/grok"
			;;
		hermesAgent)
			export HERMES_HOME="${projection_shared}/hermes"
			;;
		openClaw)
			export OPENCLAW_STATE_DIR="${projection_shared}/openclaw"
			;;
		openCode)
			export XDG_DATA_HOME="${projection_shared}/opencode-data"
			export OPENCODE_CONFIG_CONTENT='{"mcp":{"ambient":{"type":"local","command":["false"]}}}'
			export OPENCODE_CONFIG_DIR="${projection_shared}/ambient-opencode-config"
			;;
	esac

	anvil_prepare_native_mcp_projection "${backend}"
	local native_config
	native_config="$(anvil_native_mcp_config_path "${backend}")"
	# Entrypoints project before backend-specific config mutation and the shared
	# preflight calls the helper again. The second call must be a no-op.
	anvil_prepare_native_mcp_projection "${backend}"
	[[ "$(anvil_native_mcp_config_path "${backend}")" == "${native_config}" ]]
	if [[ ! -f "${native_config}" ]]; then
		install -m 0600 /dev/null "${native_config}"
	fi
	printf '%s\n' "projection-run=${run_name}" >> "${native_config}"
	[[ "$(stat -c '%a' "${run_root}/native/${backend}")" == "700" ]]
	[[ "$(stat -c '%a' "${native_config}")" == "600" ]]

	case "${backend}" in
		codex)
			[[ -L "${CODEX_HOME}/auth.json" ]]
			[[ "$(cat "${CODEX_HOME}/auth.json")" == '{"tokens":"codex-auth"}' ]]
			;;
		grokBuild)
			[[ -L "${GROK_HOME}/auth.json" ]]
			[[ "$(cat "${GROK_HOME}/auth.json")" == '{"tokens":"grok-auth"}' ]]
			[[ "${GROK_AUTH_PATH}" == "${projection_shared}/grok/auth.json" ]]
			;;
		hermesAgent)
			[[ -L "${HERMES_HOME}/auth.json" && -L "${HERMES_HOME}/.env" ]]
			[[ -L "${HERMES_HOME}/auth.lock" && -L "${HERMES_HOME}/state.db" ]]
			[[ "$(readlink "${HERMES_HOME}/auth.lock")" == "${projection_shared}/hermes/auth.lock" ]]
			[[ -L "${HERMES_HOME}/memories" && -L "${HERMES_HOME}/sessions" ]]
			[[ "$(cat "${HERMES_HOME}/memories/MEMORY.md")" == durable-memory ]]
			;;
		openClaw)
			[[ "${OPENCLAW_STATE_DIR}" == "${projection_shared}/openclaw" ]]
			[[ "${OPENCLAW_CONFIG_PATH}" == "${native_config}" ]]
			;;
		openCode)
			[[ "$(cat "${XDG_DATA_HOME}/opencode/auth.json")" == '{"provider":"opencode-auth"}' ]]
			[[ -z "${OPENCODE_CONFIG_CONTENT:-}" && -z "${OPENCODE_CONFIG_DIR:-}" ]]
			[[ "${XDG_CONFIG_HOME}" == "${run_root}/native/openCode/xdg-config" ]]
			[[ "${OPENCODE_DISABLE_PROJECT_CONFIG}" == 1 ]]
			;;
	esac
	printf '%s\n' "${native_config}" > "${result_file}"
)

shared_digest_before="$(find "${projection_shared}" -type f -print0 | sort -z | xargs -0 sha256sum)"
for backend in codex grokBuild hermesAgent openClaw openCode; do
	result_a="${test_root}/${backend}-projection-a"
	result_b="${test_root}/${backend}-projection-b"
	run_native_projection_worker "${backend}" run-a "${result_a}" &
	pid_a=$!
	run_native_projection_worker "${backend}" run-b "${result_b}" &
	pid_b=$!
	wait "${pid_a}"
	wait "${pid_b}"
	config_a="$(<"${result_a}")"
	config_b="$(<"${result_b}")"
	[[ "${config_a}" != "${config_b}" ]] || {
		echo "concurrent ${backend} projections shared a native config" >&2
		exit 1
	}
	rg -q '^projection-run=run-a$' "${config_a}" || {
		echo "${backend} run-a projection was not independently writable" >&2
		exit 1
	}
	if rg -q '^projection-run=run-b$' "${config_a}"; then
		echo "${backend} run-b mutated run-a projection" >&2
		exit 1
	fi
	rg -q '^projection-run=run-b$' "${config_b}" || {
		echo "${backend} run-b projection was not independently writable" >&2
		exit 1
	}
done
shared_digest_after="$(find "${projection_shared}" -type f -print0 | sort -z | xargs -0 sha256sum)"
[[ "${shared_digest_before}" == "${shared_digest_after}" ]] || {
	echo "native MCP projection mutated a shared backend home" >&2
	exit 1
}

# Hermes creates its state lazily. A fresh durable home must receive new
# top-level identity, credential-lock, and SQLite files rather than losing them
# with the per-run config projection.
(
	fresh_shared="${test_root}/fresh-hermes"
	fresh_run="${test_root}/fresh-hermes-run"
	mkdir -p "${fresh_shared}"
	export HERMES_HOME="${fresh_shared}"
	export ANVIL_AGENT_CAPABILITIES_ROOT="${fresh_run}"
	unset ANVIL_AGENT_NATIVE_MCP_PROJECTION_ID
	anvil_prepare_native_mcp_projection hermesAgent
	[[ -L "${HERMES_HOME}/state.db" && -L "${HERMES_HOME}/state.db-wal" ]]
	[[ -L "${HERMES_HOME}/auth.lock" && -L "${HERMES_HOME}/SOUL.md" ]]
	printf '%s\n' durable-state > "${HERMES_HOME}/state.db"
	printf '%s\n' lock-state > "${HERMES_HOME}/auth.lock"
	printf '%s\n' '# durable identity' > "${HERMES_HOME}/SOUL.md"
	[[ "$(cat "${fresh_shared}/state.db")" == durable-state ]]
	[[ "$(cat "${fresh_shared}/auth.lock")" == lock-state ]]
	[[ "$(cat "${fresh_shared}/SOUL.md")" == '# durable identity' ]]
)

# Grok's supported auth-path override keeps both atomic replacement and its
# sibling auth.json.lock in the durable home while config.toml stays per-run.
(
	shared_grok="${test_root}/fresh-grok"
	run_grok="${test_root}/fresh-grok-run"
	mkdir -p "${shared_grok}"
	export GROK_HOME="${shared_grok}"
	export GROK_AUTH_PATH="${shared_grok}/custom-auth.json"
	export ANVIL_AGENT_CAPABILITIES_ROOT="${run_grok}"
	unset ANVIL_AGENT_NATIVE_MCP_PROJECTION_ID
	anvil_prepare_native_mcp_projection grokBuild
	[[ "${GROK_AUTH_PATH}" == "${shared_grok}/custom-auth.json" ]]
	printf '%s\n' refreshed > "${GROK_AUTH_PATH}"
	printf '%s\n' locked > "$(dirname "${GROK_AUTH_PATH}")/auth.json.lock"
	[[ "$(cat "${shared_grok}/custom-auth.json")" == refreshed ]]
	[[ "$(cat "${shared_grok}/auth.json.lock")" == locked ]]
)

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
export ANVIL_AGENT_CAPABILITIES_ROOT="${test_root}/capabilities"
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

for runner in codex grok-build hermes openclaw; do
	entrypoint="${repo_root}/docker/agent-run-${runner}/entrypoint.sh"
	projection_line="$(rg -n '^anvil_prepare_native_mcp_projection ' "${entrypoint}" | cut -d: -f1)"
	preflight_line="$(rg -n '^anvil_prepare_capabilities ' "${entrypoint}" | cut -d: -f1)"
	if [[ -z "${projection_line}" || -z "${preflight_line}" || "${projection_line}" -ge "${preflight_line}" ]]; then
		echo "native MCP projection is not prepared before preflight: ${entrypoint}" >&2
		exit 1
	fi
done

codex_projection_line="$(rg -n '^anvil_prepare_native_mcp_projection ' "${repo_root}/docker/agent-run-codex/entrypoint.sh" | cut -d: -f1)"
codex_configure_line="$(rg -n '^configure_codex_home$' "${repo_root}/docker/agent-run-codex/entrypoint.sh" | cut -d: -f1)"
[[ "${codex_projection_line}" -lt "${codex_configure_line}" ]] || {
	echo "Codex native projection occurs after shared config mutation" >&2
	exit 1
}

hermes_projection_line="$(rg -n '^anvil_prepare_native_mcp_projection ' "${repo_root}/docker/agent-run-hermes/entrypoint.sh" | cut -d: -f1)"
hermes_configure_line="$(rg -n '^if \[\[ ! -f "\$\{HERMES_HOME\}/config\.yaml"' "${repo_root}/docker/agent-run-hermes/entrypoint.sh" | cut -d: -f1)"
[[ "${hermes_projection_line}" -lt "${hermes_configure_line}" ]] || {
	echo "Hermes native projection occurs after shared config mutation" >&2
	exit 1
}

openclaw_entrypoint="${repo_root}/docker/agent-run-openclaw/entrypoint.sh"
openclaw_projection_line="$(rg -n '^anvil_prepare_native_mcp_projection ' "${openclaw_entrypoint}" | cut -d: -f1)"
openclaw_onboarding_line="$(rg -n '^if \[\[ ! -f "\$\{OPENCLAW_CONFIG_PATH\}"' "${openclaw_entrypoint}" | head -n1 | cut -d: -f1)"
[[ "${openclaw_projection_line}" -lt "${openclaw_onboarding_line}" ]] || {
	echo "OpenClaw native projection occurs after onboarding" >&2
	exit 1
}

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
