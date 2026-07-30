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
workdir="${ANVIL_GROK_BUILD_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
grok_build_home="${GROK_BUILD_HOME:-${ANVIL_GROK_BUILD_HOME:-/opt/anvil/grok-build}}"

source /opt/anvil-agent-run/lib/github-auth.sh
source /opt/anvil-agent-run/lib/repository-checkout.sh
source /opt/anvil-agent-run/lib/capabilities.sh

mkdir -p "$(dirname "${status_file}")" "${grok_build_home}/.grok" "${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export ANVIL_AGENT_RUN_STATUS_TOOL="${ANVIL_AGENT_RUN_STATUS_TOOL:-anvil-agent-status}"
export HOME="${grok_build_home}"
export GROK_HOME="${GROK_HOME:-${grok_build_home}/.grok}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

seed_grok_auth_home() {
	local grok_home="${GROK_HOME:-${grok_build_home}/.grok}"
	local auth_file="${grok_home}/auth.json"
	local seed_file="${grok_build_home}/.anvil-grok-auth-seed-id"
	local logout_file="${grok_build_home}/.anvil-grok-auth-logged-out"
	local seed_id="${ANVIL_GROK_AUTH_SEED_ID:-${GROK_AUTH_SEED_ID:-}}"
	local existing_seed=""

	mkdir -p "${grok_home}"

	if [[ -f "${logout_file}" ]]; then
		echo "ANVIL_GROK_AUTH_LOGGED_OUT home=${grok_build_home}"
		unset GROK_AUTH_JSON GROK_AUTH_SEED_ID ANVIL_GROK_AUTH_SEED_ID || true
		return 0
	fi

	if [[ -z "${GROK_AUTH_JSON:-}" ]]; then
		return 0
	fi

	if [[ -f "${seed_file}" ]]; then
		existing_seed="$(tr -d '[:space:]' < "${seed_file}" || true)"
	fi

	# Durable OAuth auth.json is authoritative. Reseed only when missing, or when
	# the operator deliberately changes the opaque seed id after reauth.
	if [[ -f "${auth_file}" ]]; then
		if [[ -z "${seed_id}" || -z "${existing_seed}" || "${seed_id}" == "${existing_seed}" ]]; then
			unset GROK_AUTH_JSON GROK_AUTH_SEED_ID ANVIL_GROK_AUTH_SEED_ID || true
			return 0
		fi
		echo "ANVIL_GROK_AUTH_RESEED reason=seed-id-changed home=${grok_build_home}"
	else
		echo "ANVIL_GROK_AUTH_SEED reason=missing-auth-json home=${grok_build_home}"
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

seed_grok_auth_home

anvil_configure_github_auth

cd "${workdir}"
git config --global --add safe.directory "${workdir}" >/dev/null 2>&1 || true
git config --global user.name "${ANVIL_AGENT_GIT_AUTHOR_NAME:-Anvil AgentRun}" >/dev/null 2>&1 || true
git config --global user.email "${ANVIL_AGENT_GIT_AUTHOR_EMAIL:-agent-run@anvil-agents.invalid}" >/dev/null 2>&1 || true

workspace_empty() {
	[[ -z "$(find . -mindepth 1 -maxdepth 1 -print -quit)" ]]
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

anvil_prepare_capabilities "${workdir}" "${ANVIL_AGENT_RUN_BACKEND:-grokBuild}"

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

grok_command_string="${ANVIL_GROK_BUILD_COMMAND:-grok}"
read -r -a grok_command <<< "${grok_command_string}"
if ! command -v "${grok_command[0]}" >/dev/null 2>&1; then
	anvil-agent-status needsHuman --stage backend-start --summary "Grok Build executable is missing from the adapter image." >/dev/null || true
	exit 1
fi

grok_args=()
if truthy "${ANVIL_GROK_BUILD_ALWAYS_APPROVE:-true}"; then
	grok_args+=(--always-approve)
fi
if [[ -n "${ANVIL_GROK_BUILD_MODEL:-}" ]]; then
	grok_args+=(-m "${ANVIL_GROK_BUILD_MODEL}")
fi
grok_build_check_default="true"
if [[ "${ANVIL_GROK_BUILD_MODEL:-}" == *composer* ]]; then
	# Composer currently enters the verifier before doing the primary task when
	# Grok Build's --check loop is enabled. Keep explicit overrides available.
	grok_build_check_default="false"
fi
if truthy "${ANVIL_GROK_BUILD_CHECK:-${grok_build_check_default}}"; then
	grok_args+=(--check)
fi
if [[ -n "${ANVIL_GROK_BUILD_REASONING_EFFORT:-}" ]]; then
	grok_args+=(--effort "${ANVIL_GROK_BUILD_REASONING_EFFORT}")
fi
if [[ -n "${ANVIL_GROK_BUILD_OUTPUT_FORMAT:-}" ]]; then
	grok_args+=(--output-format "${ANVIL_GROK_BUILD_OUTPUT_FORMAT}")
fi
if [[ -n "${ANVIL_GROK_BUILD_ADDITIONAL_ARGS_JSON:-}" ]]; then
	while IFS= read -r arg; do
		grok_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_GROK_BUILD_ADDITIONAL_ARGS_JSON}")
fi

grok_auth_mode="${ANVIL_GROK_BUILD_PROVIDER_AUTH_MODE:-${ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE:-}}"
if [[ "${grok_auth_mode}" == "apiKey" && -z "${XAI_API_KEY:-}" ]]; then
	anvil-agent-status needsHuman --stage harness-auth --summary "Grok Build apiKey mode requires XAI_API_KEY; none is mounted for this AgentRun." >/dev/null || true
	exit 1
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=grokBuild intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Grok Build AgentRun harness started." >/dev/null || true
grok_output="$(mktemp)"
set +e
"${grok_command[@]}" "${grok_args[@]}" --prompt-file "${combined_prompt}" 2>&1 | tee "${grok_output}"
grok_status="${PIPESTATUS[0]}"
set -e
if [[ "${grok_status}" -ne 0 ]]; then
	if grep -Eq 'Device code expired|open this URL in your browser|accounts\.x\.ai/oauth2/device|missing-provider-auth|No API key found|ProviderAuthError' "${grok_output}"; then
		anvil-agent-status needsHuman --stage harness-auth --summary "Grok Build model provider authentication is missing, expired, or blocked on interactive device-code login. Re-seed the AgentDataVolume OAuth home offline." >/dev/null || true
	fi
	exit "${grok_status}"
fi
anvil-agent-status progress --stage harness-complete --summary "Grok Build AgentRun harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
