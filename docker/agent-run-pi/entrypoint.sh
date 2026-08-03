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
workdir="${ANVIL_PI_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
pi_root="${ANVIL_PI_HOME:-/opt/anvil/pi}"
pi_agent_dir="${PI_CODING_AGENT_DIR:-${pi_root}/agent}"
pi_session_dir="${PI_CODING_AGENT_SESSION_DIR:-${pi_root}/sessions}"
pi_home="${ANVIL_PI_USER_HOME:-${pi_root}/home}"
pi_xai_extension="${ANVIL_PI_XAI_EXTENSION:-/usr/local/lib/node_modules/pi-xai-oauth/extensions/xai-oauth.ts}"

source /opt/anvil-agent-run/lib/github-auth.sh
anvil_configure_github_auth "$0" "$@"
source /opt/anvil-agent-run/lib/repository-checkout.sh

mkdir -p "$(dirname "${status_file}")" "${pi_agent_dir}" "${pi_session_dir}" "${pi_home}/.grok" "${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export ANVIL_AGENT_RUN_STATUS_TOOL="${ANVIL_AGENT_RUN_STATUS_TOOL:-anvil-agent-status}"
export PI_CODING_AGENT_DIR="${pi_agent_dir}"
export PI_CODING_AGENT_SESSION_DIR="${pi_session_dir}"
export HOME="${pi_home}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

seed_pi_grok_auth_home() {
	local grok_home="${HOME}/.grok"
	local auth_file="${grok_home}/auth.json"
	local seed_file="${HOME}/.anvil-grok-auth-seed-id"
	local logout_file="${HOME}/.anvil-grok-auth-logged-out"
	local seed_id="${ANVIL_GROK_AUTH_SEED_ID:-${GROK_AUTH_SEED_ID:-}}"
	local existing_seed=""

	mkdir -p "${grok_home}"

	if [[ -f "${logout_file}" ]]; then
		echo "ANVIL_GROK_AUTH_LOGGED_OUT home=${HOME}"
		unset GROK_AUTH_JSON GROK_AUTH_SEED_ID ANVIL_GROK_AUTH_SEED_ID || true
		return 0
	fi
	if [[ -z "${GROK_AUTH_JSON:-}" ]]; then
		return 0
	fi
	if [[ -f "${seed_file}" ]]; then
		existing_seed="$(tr -d '[:space:]' < "${seed_file}" || true)"
	fi
	if [[ -f "${auth_file}" ]]; then
		if [[ -z "${seed_id}" || -z "${existing_seed}" || "${seed_id}" == "${existing_seed}" ]]; then
			unset GROK_AUTH_JSON GROK_AUTH_SEED_ID ANVIL_GROK_AUTH_SEED_ID || true
			return 0
		fi
		echo "ANVIL_GROK_AUTH_RESEED reason=seed-id-changed home=${HOME}"
	else
		echo "ANVIL_GROK_AUTH_SEED reason=missing-auth-json home=${HOME}"
	fi
	umask 077
	local tmp_file
	tmp_file="$(mktemp "${grok_home}/.auth.json.XXXXXX")"
	printf '%s' "${GROK_AUTH_JSON}" > "${tmp_file}"
	chmod 600 "${tmp_file}"
	mv "${tmp_file}" "${auth_file}"
	if [[ -n "${seed_id}" ]]; then
		printf '%s\n' "${seed_id}" > "${seed_file}"
		chmod 600 "${seed_file}" >/dev/null 2>&1 || true
	fi
	unset GROK_AUTH_JSON GROK_AUTH_SEED_ID ANVIL_GROK_AUTH_SEED_ID || true
}

seed_pi_grok_auth_home

cd "${workdir}"
git config --global --add safe.directory "${workdir}" >/dev/null 2>&1 || true
git config --global user.name "${ANVIL_AGENT_GIT_AUTHOR_NAME:-Anvil AgentRun}" >/dev/null 2>&1 || true
git config --global user.email "${ANVIL_AGENT_GIT_AUTHOR_EMAIL:-agent-run@anvil-agents.invalid}" >/dev/null 2>&1 || true

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

combined_prompt="$(mktemp)"
{
	echo "# Immutable AgentRun Prompt Layers"
	if [[ -d "${immutable_prompt_dir}" ]]; then
		while IFS= read -r -d '' prompt_part; do
			echo
			echo "## $(basename "${prompt_part}")"
			cat "${prompt_part}"
		done < <(find "${immutable_prompt_dir}" -maxdepth 1 -type f -name '*.md' -print0 | sort -z)
	fi
	echo
	echo "# Dedicated Agent Instructions"
	if [[ -f "${agents_file}" ]]; then
		cat "${agents_file}"
	fi
	if [[ -n "${skill_dir}" && -d "${skill_dir}" ]]; then
		echo
		echo "# Bundled Agent Skills"
		while IFS= read -r -d '' prompt_part; do
			echo
			echo "## $(basename "${prompt_part}")"
			cat "${prompt_part}"
		done < <(find "${skill_dir}" -maxdepth 1 -type f -name '*.md' -print0 | sort -z)
	fi
	if [[ -n "${skill_files}" ]]; then
		echo
		echo "# Injected AgentRun Skills"
		while IFS= read -r prompt_part; do
			[[ -z "${prompt_part}" ]] && continue
			echo
			echo "## $(basename "${prompt_part}")"
			if [[ -f "${prompt_part}" ]]; then
				cat "${prompt_part}"
			else
				echo "Configured skill file was not found at ${prompt_part}."
			fi
		done <<< "${skill_files}"
	fi
	if [[ -n "${tools_json}" ]]; then
		echo
		echo "# AgentRun Tools"
		if command -v jq >/dev/null 2>&1; then
			jq . <<< "${tools_json}"
		else
			printf '%s\n' "${tools_json}"
		fi
	fi
	echo
	echo "# AgentRun Context"
	if [[ -f "${context_file}" ]]; then
		cat "${context_file}"
	else
		echo "{}"
	fi
	echo
	echo "# AgentRun Prompt"
	if [[ -f "${prompt_file}" ]]; then
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

if ! command -v pi >/dev/null 2>&1; then
	anvil-agent-status needsHuman --stage backend-start --summary "Pi executable is missing from the adapter image." >/dev/null || true
	exit 1
fi

pi_provider="${ANVIL_PI_PROVIDER:-xai-auth}"
pi_model="${ANVIL_PI_MODEL:-grok-4.5}"
pi_mode="${ANVIL_PI_MODE:-text}"
pi_thinking="${ANVIL_PI_THINKING:-}"
session_name="${ANVIL_PI_SESSION_NAME:-AgentRun ${ANVIL_AGENT_RUN_NAMESPACE:-default}/${ANVIL_AGENT_RUN:-manual}}"

pi_args=(--approve --provider "${pi_provider}" --model "${pi_model}" --mode "${pi_mode}")
if [[ -n "${pi_thinking}" ]]; then
	pi_args+=(--thinking "${pi_thinking}")
fi
if [[ -f "${pi_xai_extension}" ]]; then
	pi_args+=(--extension "${pi_xai_extension}")
elif [[ "${pi_provider}" == "xai-auth" ]]; then
	anvil-agent-status needsHuman --stage backend-start --summary "Pi xAI OAuth extension is missing from the adapter image." >/dev/null || true
	exit 1
fi
if truthy "${ANVIL_PI_NO_SESSION:-false}"; then
	pi_args+=(--no-session)
else
	pi_args+=(--session-dir "${pi_session_dir}" --name "${session_name}")
fi
if [[ "${ANVIL_PI_PROVIDER_AUTH_MODE:-}" == "apiKey" && -n "${XAI_API_KEY:-}" ]]; then
	pi_args+=(--api-key "${XAI_API_KEY}")
fi
if [[ -n "${ANVIL_PI_ADDITIONAL_ARGS_JSON:-}" ]]; then
	while IFS= read -r arg; do
		pi_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_PI_ADDITIONAL_ARGS_JSON}")
fi

pi_auth_mode="${ANVIL_PI_PROVIDER_AUTH_MODE:-${ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE:-}}"
pi_auth_json="${pi_agent_dir}/auth.json"
if [[ "${pi_provider}" == "xai-auth" ]]; then
	if [[ "${pi_auth_mode}" == "apiKey" ]]; then
		if [[ -z "${XAI_API_KEY:-}" ]]; then
			anvil-agent-status needsHuman --stage harness-auth --summary "Pi xAI apiKey mode requires XAI_API_KEY; none is mounted for this AgentRun." >/dev/null || true
			exit 1
		fi
	elif [[ ! -s "${pi_auth_json}" ]]; then
		anvil-agent-status needsHuman --stage harness-auth --summary "Pi xai-auth OAuth credentials are missing from the durable home (expected ${pi_auth_json}). Bootstrap anvil-pi-xai-home offline; GROK_AUTH_JSON alone is not enough." >/dev/null || true
		exit 1
	fi
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=piAgent intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Pi AgentRun harness started." >/dev/null || true
# Use Pi @file message syntax so process listings show only the path, not the
# merged immutable/context/prompt payload (AgentRun issue #399).
pi_output="$(mktemp)"
set +e
pi "${pi_args[@]}" -p @"${combined_prompt}" 2>&1 | tee "${pi_output}"
pi_status="${PIPESTATUS[0]}"
set -e
if [[ "${pi_status}" -ne 0 ]]; then
	if grep -Eq 'No API key found for "xai-auth"|No API key found for .xai-auth.|missing-provider-auth|ProviderAuthError' "${pi_output}"; then
		anvil-agent-status needsHuman --stage harness-auth --summary "Pi model provider authentication is missing or invalid for xai-auth." >/dev/null || true
	fi
	exit "${pi_status}"
fi
anvil-agent-status progress --stage harness-complete --summary "Pi AgentRun harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
