#!/usr/bin/env bash

# Shared capability bootstrap for all built-in AgentRun adapters. Callers must
# source this file so setupScript exports remain visible to the model process.

anvil_capability_status() {
	local level="$1"
	local stage="$2"
	local summary="$3"
	if command -v anvil-agent-status >/dev/null 2>&1; then
		anvil-agent-status progress --level "${level}" --stage "${stage}" --summary "${summary}" >/dev/null || true
	fi
}

anvil_source_setup_scripts() {
	local setup_files="${ANVIL_AGENT_RUN_TOOL_SETUP_FILES:-}"
	local workdir="$1"
	[[ -n "${setup_files}" ]] || return 0

	while IFS= read -r tool_file; do
		[[ -n "${tool_file}" ]] || continue
		if [[ ! -f "${tool_file}" ]]; then
			echo "ANVIL_AGENT_RUN_TOOL_SETUP_MISSING file=${tool_file}" >&2
			return 1
		fi
		echo "ANVIL_AGENT_RUN_TOOL_SETUP_SOURCE file=$(basename "${tool_file}")"
		# shellcheck source=/dev/null
		if ! . "${tool_file}"; then
			echo "ANVIL_AGENT_RUN_TOOL_SETUP_FAILED file=$(basename "${tool_file}")" >&2
			return 1
		fi
		cd "${workdir}"
	done <<< "${setup_files}"
}

anvil_verify_legacy_tools() {
	local tools_json="${ANVIL_AGENT_RUN_TOOLS_JSON:-}"
	[[ -n "${tools_json}" ]] || return 0
	if ! command -v jq >/dev/null 2>&1 || ! command -v base64 >/dev/null 2>&1; then
		echo "ANVIL_AGENT_RUN_TOOL_VERIFY_FAILED reason=missing-jq-or-base64" >&2
		return 1
	fi
	while IFS= read -r encoded_tool; do
		[[ -n "${encoded_tool}" ]] || continue
		local tool_json tool_name verify_count
		local -a verify_command=()
		tool_json="$(printf '%s' "${encoded_tool}" | base64 -d)"
		tool_name="$(jq -r '.name // "unnamed"' <<< "${tool_json}")"
		verify_count="$(jq -r '(.verifyCommand // []) | length' <<< "${tool_json}")"
		[[ "${verify_count}" != "0" ]] || continue
		mapfile -t verify_command < <(jq -r '.verifyCommand[]' <<< "${tool_json}")
		if [[ "${#verify_command[@]}" -eq 0 || -z "${verify_command[0]}" ]]; then
			echo "ANVIL_AGENT_RUN_TOOL_VERIFY_FAILED name=${tool_name} reason=empty-command" >&2
			return 1
		fi
		echo "ANVIL_AGENT_RUN_TOOL_VERIFY_START name=${tool_name}"
		if ! "${verify_command[@]}"; then
			echo "ANVIL_AGENT_RUN_TOOL_VERIFY_FAILED name=${tool_name}" >&2
			return 1
		fi
		echo "ANVIL_AGENT_RUN_TOOL_VERIFY_OK name=${tool_name}"
	done < <(jq -r '.[] | @base64' <<< "${tools_json}")
}

anvil_initialize_capability_paths() {
	export ANVIL_AGENT_TOOL_CACHE_ROOT="${ANVIL_AGENT_TOOL_CACHE_ROOT:-/tmp/anvil-agent-tool-cache}"
	export ANVIL_AGENT_CAPABILITIES_ROOT="${ANVIL_AGENT_CAPABILITIES_ROOT:-/tmp/anvil-agent-capabilities}"
	export ANVIL_AGENT_TOOL_INSTALL_ROOT="${ANVIL_AGENT_TOOL_INSTALL_ROOT:-${ANVIL_AGENT_CAPABILITIES_ROOT}/install}"
	export ANVIL_AGENT_TOOL_BIN_DIR="${ANVIL_AGENT_TOOL_BIN_DIR:-${ANVIL_AGENT_CAPABILITIES_ROOT}/bin}"
	case ":${PATH}:" in
		*":${ANVIL_AGENT_TOOL_BIN_DIR}:"*) ;;
		*) export PATH="${ANVIL_AGENT_TOOL_BIN_DIR}:${PATH}" ;;
	esac
	mkdir -p \
		"${ANVIL_AGENT_CAPABILITIES_ROOT}" \
		"${ANVIL_AGENT_TOOL_INSTALL_ROOT}" \
		"${ANVIL_AGENT_TOOL_BIN_DIR}"
}

anvil_copy_native_projection_file() {
	local source_file="$1"
	local destination_file="$2"
	[[ -f "${source_file}" ]] || return 0
	mkdir -p "$(dirname "${destination_file}")"
	cp -pL -- "${source_file}" "${destination_file}"
	chmod 0600 "${destination_file}"
}

anvil_link_native_projection_home() {
	local source_home="$1"
	local destination_home="$2"
	local excluded_name="$3"
	[[ -d "${source_home}" ]] || return 0
	while IFS= read -r -d '' source_entry; do
		local entry_name="${source_entry##*/}"
		[[ "${entry_name}" != "${excluded_name}" ]] || continue
		ln -s -- "${source_entry}" "${destination_home}/${entry_name}"
	done < <(find "${source_home}" -mindepth 1 -maxdepth 1 -print0)
}

anvil_prepare_native_mcp_projection() {
	local backend="$1"
	anvil_initialize_capability_paths

	local projection_id="${backend}:${ANVIL_AGENT_CAPABILITIES_ROOT}"
	if [[ "${ANVIL_AGENT_NATIVE_MCP_PROJECTION_ID:-}" == "${projection_id}" ]]; then
		return 0
	fi

	local native_root="${ANVIL_AGENT_CAPABILITIES_ROOT}/native/${backend}"
	mkdir -p "${native_root}"
	chmod 0700 "${native_root}"
	case "${backend}" in
		codex)
			local shared_codex_home="${CODEX_HOME:-/codex-home}"
			anvil_link_native_projection_home "${shared_codex_home}" "${native_root}" config.toml
			anvil_copy_native_projection_file "${shared_codex_home}/config.toml" "${native_root}/config.toml"
			export ANVIL_AGENT_SHARED_CODEX_HOME="${shared_codex_home}"
			export CODEX_HOME="${native_root}"
			;;
		grokBuild)
			local shared_grok_home="${GROK_HOME:-${HOME}/.grok}"
			anvil_link_native_projection_home "${shared_grok_home}" "${native_root}" config.toml
			anvil_copy_native_projection_file "${shared_grok_home}/config.toml" "${native_root}/config.toml"
			export ANVIL_AGENT_SHARED_GROK_HOME="${shared_grok_home}"
			export GROK_HOME="${native_root}"
			;;
		hermesAgent)
			local shared_hermes_home="${HERMES_HOME:-${HOME}/.hermes}"
			# Hermes stores memory, sessions, skills, provider credentials, and
			# other profile state beside config.yaml. Preserve that durable home
			# through links while keeping only the mutable native config run-local.
			mkdir -p \
				"${shared_hermes_home}/cron" \
				"${shared_hermes_home}/home" \
				"${shared_hermes_home}/logs" \
				"${shared_hermes_home}/memories" \
				"${shared_hermes_home}/sessions" \
				"${shared_hermes_home}/skills"
			anvil_link_native_projection_home "${shared_hermes_home}" "${native_root}" config.yaml
			anvil_copy_native_projection_file "${shared_hermes_home}/config.yaml" "${native_root}/config.yaml"
			export ANVIL_AGENT_SHARED_HERMES_HOME="${shared_hermes_home}"
			export HERMES_HOME="${native_root}"
			;;
		openClaw)
			local shared_openclaw_state="${OPENCLAW_STATE_DIR:-${HOME}/.openclaw}"
			local shared_openclaw_config="${OPENCLAW_CONFIG_PATH:-${shared_openclaw_state}/openclaw.json}"
			anvil_copy_native_projection_file "${shared_openclaw_config}" "${native_root}/openclaw.json"
			export ANVIL_AGENT_SHARED_OPENCLAW_CONFIG_PATH="${shared_openclaw_config}"
			export OPENCLAW_CONFIG_PATH="${native_root}/openclaw.json"
			;;
		openCode)
			export OPENCODE_CONFIG="${native_root}/opencode.json"
			;;
		piAgent)
			# ConfigureNativeMCP rejects Pi until the image carries an audited
			# MCP client extension. Keep even its sentinel run-local.
			;;
		*)
			return 1
			;;
	esac
	export ANVIL_AGENT_NATIVE_MCP_PROJECTION_ID="${projection_id}"
}

anvil_native_mcp_config_path() {
	local backend="$1"
	case "${backend}" in
		codex)
			printf '%s/config.toml\n' "${CODEX_HOME:-/codex-home}"
			;;
		grokBuild)
			printf '%s/config.toml\n' "${GROK_HOME:-${HOME}/.grok}"
			;;
		hermesAgent)
			printf '%s/config.yaml\n' "${HERMES_HOME:-${HOME}/.hermes}"
			;;
		openClaw)
			printf '%s\n' "${OPENCLAW_CONFIG_PATH:-${ANVIL_AGENT_CAPABILITIES_ROOT}/native/openClaw/openclaw.json}"
			;;
		openCode)
			printf '%s\n' "${OPENCODE_CONFIG:-${ANVIL_AGENT_CAPABILITIES_ROOT}/native/openCode/opencode.json}"
			;;
		piAgent)
			# ConfigureNativeMCP deliberately rejects Pi until the image carries
			# an audited MCP client extension.
			printf '%s/native/piAgent/pi-mcp.unsupported\n' "${ANVIL_AGENT_CAPABILITIES_ROOT}"
			;;
		*)
			return 1
			;;
	esac
}

anvil_prepare_capabilities() {
	local workdir="$1"
	local backend="${2:-${ANVIL_AGENT_RUN_BACKEND:-}}"
	local tool_manifest="${ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE:-}"
	local mcp_manifest="${ANVIL_AGENT_RUN_MCP_MANIFEST_FILE:-}"
	local capability_command="${ANVIL_AGENT_CAPABILITIES_COMMAND:-anvil-agent-capabilities}"

	anvil_initialize_capability_paths
	if ! anvil_prepare_native_mcp_projection "${backend}"; then
		echo "ANVIL_AGENT_RUN_MCP_BACKEND_UNSUPPORTED backend=${backend}" >&2
		return 1
	fi

	echo "ANVIL_AGENT_RUN_CAPABILITY_PREFLIGHT_START"
	anvil_capability_status info capability-preflight "Preparing AgentRun capabilities."

	if [[ -n "${tool_manifest}" ]]; then
		if [[ ! -s "${tool_manifest}" ]]; then
			echo "ANVIL_AGENT_RUN_TOOL_MANIFEST_MISSING file=${tool_manifest}" >&2
			anvil_capability_status error tool-acquisition "AgentRun tool manifest is unavailable."
			return 1
		fi
		if ! "${capability_command}" tools install \
				--manifest "${tool_manifest}" \
				--cache-root "${ANVIL_AGENT_TOOL_CACHE_ROOT}" \
				--install-root "${ANVIL_AGENT_TOOL_INSTALL_ROOT}" \
				--bin-dir "${ANVIL_AGENT_TOOL_BIN_DIR}"; then
			anvil_capability_status error tool-acquisition "AgentRun tool acquisition failed."
			return 1
		fi
	fi

	if ! anvil_source_setup_scripts "${workdir}"; then
		anvil_capability_status error tool-setup "AgentRun custom tool setup failed."
		return 1
	fi

	if [[ -n "${tool_manifest}" ]]; then
		if ! "${capability_command}" tools verify \
			--manifest "${tool_manifest}" \
			--bin-dir "${ANVIL_AGENT_TOOL_BIN_DIR}"; then
			anvil_capability_status error tool-verify "AgentRun tool verification failed."
			return 1
		fi
	fi
	if [[ -z "${tool_manifest}" ]] && ! anvil_verify_legacy_tools; then
		anvil_capability_status error tool-verify "AgentRun compatibility tool verification failed."
		return 1
	fi

	local effective_mcp_manifest="${mcp_manifest}"
	if [[ -n "${mcp_manifest}" ]]; then
		if [[ ! -s "${mcp_manifest}" ]]; then
			echo "ANVIL_AGENT_RUN_MCP_MANIFEST_MISSING file=${mcp_manifest}" >&2
			anvil_capability_status error mcp-preflight "AgentRun MCP manifest is unavailable."
			return 1
		fi
		if ! "${capability_command}" mcp preflight --manifest "${mcp_manifest}" --backend "${backend}"; then
			anvil_capability_status error mcp-preflight "AgentRun MCP preflight failed."
			return 1
		fi
	else
		effective_mcp_manifest="${ANVIL_AGENT_CAPABILITIES_ROOT}/empty-mcp.json"
		printf '[]\n' > "${effective_mcp_manifest}"
	fi
	local native_config
	if ! native_config="$(anvil_native_mcp_config_path "${backend}")"; then
		echo "ANVIL_AGENT_RUN_MCP_BACKEND_UNSUPPORTED backend=${backend}" >&2
		return 1
	fi
	if ! "${capability_command}" mcp configure \
		--manifest "${effective_mcp_manifest}" \
		--backend "${backend}" \
		--config "${native_config}"; then
		anvil_capability_status error mcp-configure "AgentRun MCP native configuration failed."
		return 1
	fi
	echo "ANVIL_AGENT_RUN_CAPABILITY_PREFLIGHT_COMPLETE"
	anvil_capability_status info capability-preflight "AgentRun capabilities are ready."
}
