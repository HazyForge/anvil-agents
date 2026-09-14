#!/usr/bin/env bash
set -euo pipefail

prompt_file="${ANVIL_AGENT_RUN_PROMPT_FILE:-/var/run/anvil-agent-run/prompt.md}"
context_file="${ANVIL_AGENT_RUN_CONTEXT_FILE:-/var/run/anvil-agent-run/source.json}"
agents_file="${ANVIL_AGENT_RUN_AGENTS_FILE:-/opt/anvil-agent-run/AGENTS.md}"
immutable_prompt_dir="/opt/anvil-agent-run/static-prompts"
skill_files="${ANVIL_AGENT_RUN_SKILL_FILES:-}"
tool_setup_files="${ANVIL_AGENT_RUN_TOOL_SETUP_FILES:-}"
tools_json="${ANVIL_AGENT_RUN_TOOLS_JSON:-}"
workdir="${ANVIL_AGY_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
agy_root="${ANVIL_AGY_HOME:-/opt/anvil/agy}"
agy_home="${ANVIL_AGY_USER_HOME:-${agy_root}/home}"

# The bootstrap value is the opaque file produced by the pinned native CLI.
# Remove it from exported environment before setup tools or the harness start.
seed_native_agy_auth() {
	local native_seed="${ANVIL_AGY_NATIVE_OAUTH_TOKEN:-}"
	export -n native_seed
	unset ANVIL_AGY_NATIVE_OAUTH_TOKEN
	local native_dir="${agy_home}/.gemini/antigravity-cli"
	local native_file="${native_dir}/antigravity-oauth-token"
	if [[ -n "${native_seed}" && ! -e "${native_file}" && ! -L "${native_file}" ]]; then
		umask 077
		mkdir -p "${native_dir}"
		local seed_tmp
		seed_tmp="$(mktemp "${native_dir}/.native-oauth-token.XXXXXX")"
		printf '%s' "${native_seed}" > "${seed_tmp}"
		chmod 0600 "${seed_tmp}"
		# No-clobber publication also preserves a concurrent native refresh.
		if ! ln "${seed_tmp}" "${native_file}" 2>/dev/null; then
			rm -f "${seed_tmp}"
			[[ -e "${native_file}" || -L "${native_file}" ]] || return 1
		else
			rm -f "${seed_tmp}"
		fi
	fi
	unset native_seed
}
seed_native_agy_auth

runner_lib_dir="${ANVIL_AGENT_RUN_LIB_DIR:-/opt/anvil-agent-run/lib}"
source "${runner_lib_dir}/github-auth.sh"
anvil_configure_github_auth "$0" "$@"
source "${runner_lib_dir}/repository-checkout.sh"

mkdir -p "$(dirname "${status_file}")" "${agy_home}/.gemini/antigravity-cli" "${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export ANVIL_AGENT_RUN_STATUS_TOOL="${ANVIL_AGENT_RUN_STATUS_TOOL:-anvil-agent-status}"
export HOME="${agy_home}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

# Use the provider's native settings and credential mechanisms. An invented
# ~/.agy/auth.json is not consumed by Google's CLI and must not imply sign-in.
if [[ -n "${AGY_AUTH_JSON:-}" ]]; then
	echo "AGY_AUTH_JSON is unsupported; configure native AGY authentication (see runner README)." >&2
	exit 2
fi
export AGY_CLI_DISABLE_AUTO_UPDATE=true
cd "${workdir}"

workspace_empty() {
	[[ -z "$(find . -mindepth 1 -maxdepth 1 -print -quit)" ]]
}

run_tool_setup() {
	local ran_any="false"
	if [[ -n "${tool_setup_files}" ]]; then
		echo "ANVIL_AGENT_RUN_TOOL_SETUP_START"
		anvil-agent-status progress --stage tool-setup --summary "Preparing AgentRun tools." >/dev/null || true
		while IFS= read -r tool_file; do
			if [[ -z "${tool_file}" ]]; then
				continue
			fi
			if [[ ! -f "${tool_file}" ]]; then
				echo "ANVIL_AGENT_RUN_TOOL_SETUP_MISSING file=${tool_file}" >&2
				exit 1
			fi
			echo "ANVIL_AGENT_RUN_TOOL_SETUP_SOURCE file=$(basename "${tool_file}")"
			# shellcheck source=/dev/null
			. "${tool_file}"
			cd "${workdir}"
			ran_any="true"
		done <<< "${tool_setup_files}"
	fi

	if [[ -n "${tools_json}" ]]; then
		if command -v jq >/dev/null 2>&1 && command -v base64 >/dev/null 2>&1; then
			while IFS= read -r encoded_tool; do
				if [[ -z "${encoded_tool}" ]]; then
					continue
				fi
				local tool_json
				local tool_name
				local verify_count
				tool_json="$(printf '%s' "${encoded_tool}" | base64 -d)"
				tool_name="$(jq -r '.name // "unnamed"' <<< "${tool_json}")"
				verify_count="$(jq -r '(.verifyCommand // []) | length' <<< "${tool_json}")"
				if [[ "${verify_count}" == "0" ]]; then
					continue
				fi
				mapfile -t verify_command < <(jq -r '.verifyCommand[]' <<< "${tool_json}")
				echo "ANVIL_AGENT_RUN_TOOL_VERIFY_START name=${tool_name}"
				"${verify_command[@]}"
				echo "ANVIL_AGENT_RUN_TOOL_VERIFY_OK name=${tool_name}"
				ran_any="true"
			done < <(jq -r '.[] | @base64' <<< "${tools_json}")
		else
			echo "ANVIL_AGENT_RUN_TOOL_VERIFY_SKIPPED reason=missing-jq-or-base64"
		fi
	fi

	if [[ "${ran_any}" == "true" ]]; then
		echo "ANVIL_AGENT_RUN_TOOL_SETUP_COMPLETE"
		anvil-agent-status progress --stage tool-setup --summary "AgentRun tools are ready." >/dev/null || true
	fi
}

if truthy "${ANVIL_AGENT_RUN_AUTO_CLONE_REPO:-true}" && [[ ! -d .git ]]; then
	if [[ -n "${repository_url}" ]]; then
		if workspace_empty; then
			echo "ANVIL_AGENT_RUN_REPO_CLONE repository_url_configured=true"
			anvil_clone_repository_url "${repository_url}" . || { echo "ANVIL_AGENT_RUN_REPO_CLONE_FAILED repository_url_configured=true" >&2; exit 20; }
		else
			echo "ANVIL_AGENT_RUN_REPO_CLONE_SKIPPED reason=workspace-not-empty"
		fi
	elif [[ -n "${repository}" ]]; then
		if workspace_empty; then
			echo "ANVIL_AGENT_RUN_REPO_CLONE repository=${repository}"
			gh repo clone "${repository}" . >/dev/null 2>&1 || { echo "ANVIL_AGENT_RUN_REPO_CLONE_FAILED repository=${repository}" >&2; exit 20; }
		else
			echo "ANVIL_AGENT_RUN_REPO_CLONE_SKIPPED reason=workspace-not-empty repository=${repository}"
		fi
	fi
fi

if [[ -d .git && -n "${repository_ref}" ]]; then
	anvil_checkout_repository_ref "${repository_ref}" || exit $?
fi

run_tool_setup

umask 077
combined_prompt="$(mktemp)"
agy_output="$(mktemp)"
trap 'rm -f "${combined_prompt}" "${agy_output}"' EXIT
{
	if [[ -f "${agents_file}" ]]; then
		echo "# Agent Instructions and Operating Context"
		cat "${agents_file}"
		echo
	fi
	if [[ -d "${immutable_prompt_dir}" ]]; then
		echo "# Platform Operating Principles and Constraints"
		for part in "${immutable_prompt_dir}"/*.md; do
			if [[ -f "${part}" ]]; then
				cat "${part}"
				echo
			fi
		done
	fi
	if [[ -n "${skill_files}" ]]; then
		echo "# Injected Skills"
		while IFS= read -r file; do
			if [[ -f "${file}" ]]; then
				cat "${file}"
				echo
			fi
		done <<< "${skill_files}"
	fi
	if [[ -f "${context_file}" ]]; then
		echo "# Context Snapshot (source.json)"
		cat "${context_file}"
		echo
	fi
	if [[ -f "${prompt_file}" ]]; then
		echo "# Run Prompt"
		cat "${prompt_file}"
	else
		echo "The AgentRun prompt file is missing at ${prompt_file}."
	fi
	if [[ -n "${ANVIL_AGENT_RUN_PROMPT_APPEND:-}" ]]; then
		echo
		echo "# Runtime Prompt Append"
		printf '%s\n' "${ANVIL_AGENT_RUN_PROMPT_APPEND}"
	fi
} > "${combined_prompt}"
chmod 600 "${combined_prompt}"

agy_model="${ANVIL_AGY_MODEL:-}"
agy_mode="${ANVIL_AGY_MODE:-print}"
agy_thinking="${ANVIL_AGY_THINKING:-}"

agy_args=(--dangerously-skip-permissions)
if [[ -n "${agy_model}" ]]; then
	agy_args+=(--model "${agy_model}")
fi
if [[ -n "${agy_thinking}" ]]; then
	case "${agy_thinking}" in
		low|medium|high) agy_args+=(--effort "${agy_thinking}") ;;
		*) echo "ANVIL_AGY_THINKING must be low, medium, or high" >&2; exit 2 ;;
	esac
fi

case "${agy_mode}" in
	print|text|stream-json) ;;
	*) echo "ANVIL_AGY_MODE must be print, text, or stream-json" >&2; exit 2 ;;
esac

if [[ -n "${ANVIL_AGY_ADDITIONAL_ARGS_JSON:-}" ]]; then
	jq -e 'type == "array" and all(.[]; type == "string")' <<< "${ANVIL_AGY_ADDITIONAL_ARGS_JSON}" >/dev/null
	while IFS= read -r arg; do
		agy_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_AGY_ADDITIONAL_ARGS_JSON}")
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=agy intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Agy AgentRun harness started." >/dev/null || true

set +e

# Always use the documented stdin protocol. Print/text are presentation modes;
# they must not substitute the unsupported -p - sentinel for native input.
python3 "${ANVIL_AGY_TURN_DRIVER:-/opt/anvil-agent-run/lib/agy-turn.py}" "${combined_prompt}" "${agy_args[@]}" | tee "${agy_output}"
agy_status="${PIPESTATUS[0]}"

set -e

if [[ "${agy_status}" -ne 0 ]]; then
	anvil-agent-status failure --stage harness-execution --summary "Agy harness exited with code ${agy_status}." >/dev/null || true
	exit "${agy_status}"
fi

# A zero process exit without a successful turn is not completion evidence.
if ! jq -e -s '[.[] | select(.event == "result")] | length == 1 and .[0].result.status == "SUCCESS"' "${agy_output}" >/dev/null; then
	anvil-agent-status failure --stage harness-execution --summary "Agy exited without a successful result event." >/dev/null || true
	exit 1
fi
anvil-agent-status complete --stage finished --summary "Agy AgentRun finished successfully." >/dev/null || true
rm -f "${combined_prompt}" "${agy_output}"
exit 0
