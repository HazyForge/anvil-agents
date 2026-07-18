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
workdir="${ANVIL_AGENT_RUN_WORKDIR:-/workspace}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
hermes_home="${HERMES_HOME:-/opt/anvil/hermes}"
codex_home="${CODEX_HOME:-/opt/anvil/codex}"

source /opt/anvil-agent-run/lib/github-auth.sh
source /opt/anvil-agent-run/lib/repository-checkout.sh

mkdir -p "$(dirname "${status_file}")" "${hermes_home}" "${codex_home}" "${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export HERMES_HOME="${hermes_home}"
export CODEX_HOME="${codex_home}"
export PATH="/opt/hermes/bin:/opt/hermes/.venv/bin:${PATH}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

if [[ -n "${GITHUB_TOKEN:-}" && -z "${GH_TOKEN:-}" ]]; then
	export GH_TOKEN="${GITHUB_TOKEN}"
fi
if [[ -n "${GH_TOKEN:-}" && -z "${GITHUB_TOKEN:-}" ]]; then
	export GITHUB_TOKEN="${GH_TOKEN}"
fi
if [[ -n "${CODEX_AUTH_JSON:-}" && ! -f "${CODEX_HOME}/auth.json" ]]; then
	umask 077
	printf '%s' "${CODEX_AUTH_JSON}" > "${CODEX_HOME}/auth.json"
fi
if [[ -n "${CODEX_AUTH_JSON:-}" && ! -f "${HERMES_HOME}/auth.json" ]]; then
	if hermes_auth_json="$(jq -c '
		if (.providers? | type) == "object" then
			.
		elif (.tokens? | type) == "object" then
			{
				active_provider: "openai-codex",
				providers: {
					"openai-codex": {
						auth_mode: (.auth_mode // "chatgpt"),
						tokens: .tokens,
						last_refresh: (.last_refresh // null)
					}
				}
			}
		else
			empty
		end
	' <<< "${CODEX_AUTH_JSON}" 2>/dev/null)" && [[ -n "${hermes_auth_json}" ]]; then
		umask 077
		printf '%s' "${hermes_auth_json}" > "${HERMES_HOME}/auth.json"
	fi
fi

anvil_configure_github_auth

cd "${workdir}"
git config --global --add safe.directory "${workdir}" >/dev/null 2>&1 || true

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
			git clone "${repository_url}" . >/dev/null 2>&1 || { echo "ANVIL_AGENT_RUN_REPO_CLONE_FAILED repository_url_configured=true" >&2; exit 20; }
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

hermes_model_provider="${ANVIL_HERMES_MODEL_PROVIDER:-${ANVIL_AGENT_RUN_MODEL_PROVIDER:-openai-codex}}"
hermes_openai_runtime="${ANVIL_HERMES_OPENAI_RUNTIME:-}"
if [[ -z "${hermes_openai_runtime}" && "${hermes_model_provider}" == "openai-codex" ]]; then
	hermes_openai_runtime="codex_app_server"
fi

if [[ ! -f "${HERMES_HOME}/config.yaml" || -n "${ANVIL_HERMES_MODEL_PROVIDER:-}" || -n "${ANVIL_AGENT_RUN_MODEL_PROVIDER:-}" || -n "${ANVIL_HERMES_MODEL:-}" || -n "${ANVIL_HERMES_REASONING_EFFORT:-}" || -n "${ANVIL_HERMES_SERVICE_TIER:-}" ]]; then
	{
		echo "model:"
		echo "  provider: ${hermes_model_provider}"
		echo "  name: ${ANVIL_HERMES_MODEL:-gpt-5.5}"
		if [[ -n "${hermes_openai_runtime}" ]]; then
			echo "  openai_runtime: ${hermes_openai_runtime}"
		fi
		if [[ -n "${ANVIL_HERMES_REASONING_EFFORT:-}" ]]; then
			echo "  reasoning_effort: ${ANVIL_HERMES_REASONING_EFFORT}"
		fi
		if [[ -n "${ANVIL_HERMES_SERVICE_TIER:-}" ]]; then
			echo "  service_tier: ${ANVIL_HERMES_SERVICE_TIER}"
		fi
	} > "${HERMES_HOME}/config.yaml"
fi

if [[ ! -f "${HERMES_HOME}/SOUL.md" ]]; then
	{
		echo "# AgentRun Hermes Agent"
		echo
		echo "This Hermes home belongs to the configured AgentRun scope."
		echo "Keep durable memory limited to that scope and the evidence needed to operate it."
		echo
		if [[ -f "${agents_file}" ]]; then
			cat "${agents_file}"
		fi
	} > "${HERMES_HOME}/SOUL.md"
fi

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

if ! command -v hermes >/dev/null 2>&1; then
	anvil-agent-status needsHuman --stage backend-start --summary "Hermes executable is missing from the adapter image." >/dev/null || true
	exit 1
fi

if ! command -v anvil-hermes-query >/dev/null 2>&1; then
	anvil-agent-status needsHuman --stage backend-start --summary "anvil-hermes-query helper is missing from the adapter image." >/dev/null || true
	exit 1
fi

# anvil-hermes-query reads the prompt from disk and invokes Hermes in-process so
# only the prompt path appears on argv (AgentRun issue #399). Do not reintroduce
# hermes chat -q "$(cat …)" shell expansion.
echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=hermesAgent intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Hermes AgentRun harness started." >/dev/null || true
anvil-hermes-query "${combined_prompt}"
anvil-agent-status progress --stage harness-complete --summary "Hermes AgentRun harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
