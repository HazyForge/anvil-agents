#!/usr/bin/env bash
set -euo pipefail

prompt_file="${ANVIL_AGENT_RUN_PROMPT_FILE:-/var/run/anvil-agent-run/prompt.md}"
context_file="${ANVIL_AGENT_RUN_CONTEXT_FILE:-/var/run/anvil-agent-run/source.json}"
agents_file="${ANVIL_AGENT_RUN_AGENTS_FILE:-/opt/anvil-agent-run/AGENTS.md}"
immutable_prompt_dir="/opt/anvil-agent-run/static-prompts"
skill_files="${ANVIL_AGENT_RUN_SKILL_FILES:-}"
tool_setup_files="${ANVIL_AGENT_RUN_TOOL_SETUP_FILES:-}"
tools_json="${ANVIL_AGENT_RUN_TOOLS_JSON:-}"
skill_dir="${ANVIL_AGENT_RUN_SKILLS_DIR:-/opt/anvil-agent-run/skills}"
workdir="${ANVIL_AGY_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
agy_root="${ANVIL_AGY_HOME:-/opt/anvil/agy}"
agy_home="${ANVIL_AGY_USER_HOME:-${agy_root}/home}"

source /opt/anvil-agent-run/lib/github-auth.sh
anvil_configure_github_auth "$0" "$@"
source /opt/anvil-agent-run/lib/repository-checkout.sh

mkdir -p "$(dirname "${status_file}")" "${agy_home}/.agy" "${workdir}"
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

seed_agy_auth_home() {
	local agy_auth_dir="${HOME}/.agy"
	local auth_file="${agy_auth_dir}/auth.json"
	local seed_file="${HOME}/.anvil-agy-auth-seed-id"
	local logout_file="${HOME}/.anvil-agy-auth-logged-out"
	local seed_id="${ANVIL_AGY_AUTH_SEED_ID:-${AGY_AUTH_SEED_ID:-}}"
	local existing_seed=""

	mkdir -p "${agy_auth_dir}"

	if [[ -f "${logout_file}" ]]; then
		echo "ANVIL_AGY_AUTH_LOGGED_OUT home=${HOME}"
		unset AGY_AUTH_JSON AGY_AUTH_SEED_ID ANVIL_AGY_AUTH_SEED_ID || true
		return 0
	fi
	if [[ -z "${AGY_AUTH_JSON:-}" ]]; then
		return 0
	fi
	if [[ -f "${seed_file}" ]]; then
		existing_seed="$(tr -d '[:space:]' < "${seed_file}" || true)"
	fi
	if [[ -f "${auth_file}" ]]; then
		if [[ -z "${seed_id}" || -z "${existing_seed}" || "${seed_id}" == "${existing_seed}" ]]; then
			unset AGY_AUTH_JSON AGY_AUTH_SEED_ID ANVIL_AGY_AUTH_SEED_ID || true
			return 0
		fi
		echo "ANVIL_AGY_AUTH_RESEED reason=seed-id-changed home=${HOME}"
	else
		echo "ANVIL_AGY_AUTH_SEED reason=missing-auth-json home=${HOME}"
	fi
	umask 077
	local tmp_file
	tmp_file="$(mktemp "${agy_auth_dir}/.auth.json.XXXXXX")"
	printf '%s' "${AGY_AUTH_JSON}" > "${tmp_file}"
	chmod 600 "${tmp_file}"
	mv "${tmp_file}" "${auth_file}"
	if [[ -n "${seed_id}" ]]; then
		printf '%s\n' "${seed_id}" > "${seed_file}"
		chmod 600 "${seed_file}"
	else
		rm -f "${seed_file}"
	fi
	unset AGY_AUTH_JSON AGY_AUTH_SEED_ID ANVIL_AGY_AUTH_SEED_ID || true
}

seed_agy_auth_home

if [[ -n "${repository}" || -n "${repository_url}" ]]; then
	anvil_checkout_repository "${repository}" "${repository_url}" "${repository_ref}" "${workdir}"
fi

cd "${workdir}"

# If tools JSON is present, wire tools into the execution environment.
if [[ -n "${tools_json}" ]]; then
	echo "Configuring custom tools from ANVIL_AGENT_RUN_TOOLS_JSON..."
fi

combined_prompt="$(mktemp)"
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
	agy_args+=(--thinking "${agy_thinking}")
fi

if [[ -n "${ANVIL_AGY_ADDITIONAL_ARGS_JSON:-}" ]]; then
	while IFS= read -r arg; do
		agy_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_AGY_ADDITIONAL_ARGS_JSON}")
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=agy intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Agy AgentRun harness started." >/dev/null || true

agy_output="$(mktemp)"
set +e

if [[ "${agy_mode}" == "stream-json" ]]; then
	# Stream-json mode: wrap prompt as user event and pipe to stdin
	# {"event":"user","message":{"content":[{"type":"text","text":"..."}]}}
	json_input="$(jq -Rs '{event: "user", message: {content: [{type: "text", text: .}]}}' < "${combined_prompt}")"
	printf '%s\n' "${json_input}" | agy "${agy_args[@]}" --input-format stream-json --output-format stream-json 2>&1 | tee "${agy_output}"
	agy_status="${PIPESTATUS[1]}"
else
	# Print/prompt mode: prompt via stdin with -p -
	agy "${agy_args[@]}" -p - < "${combined_prompt}" 2>&1 | tee "${agy_output}"
	agy_status="${PIPESTATUS[0]}"
fi

set -e

if [[ "${agy_status}" -ne 0 ]]; then
	anvil-agent-status failure --stage harness-execution --summary "Agy harness exited with code ${agy_status}." >/dev/null || true
	exit "${agy_status}"
fi

anvil-agent-status complete --stage finished --summary "Agy AgentRun finished successfully." >/dev/null || true
rm -f "${combined_prompt}" "${agy_output}"
exit 0
