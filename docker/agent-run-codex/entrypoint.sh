#!/usr/bin/env bash
set -euo pipefail

prompt_file="${ANVIL_AGENT_RUN_PROMPT_FILE:-/var/run/anvil-agent-run/prompt.md}"
context_file="${ANVIL_AGENT_RUN_CONTEXT_FILE:-/var/run/anvil-agent-run/source.json}"
agents_file="${ANVIL_AGENT_RUN_AGENTS_FILE:-/opt/anvil-agent-run/AGENTS.md}"
immutable_prompt_dir="/opt/anvil-agent-run/static-prompts"
extra_prompt_dir="${ANVIL_AGENT_RUN_EXTRA_PROMPT_DIR:-}"
skill_files="${ANVIL_AGENT_RUN_SKILL_FILES:-}"
tool_setup_files="${ANVIL_AGENT_RUN_TOOL_SETUP_FILES:-}"
tools_json="${ANVIL_AGENT_RUN_TOOLS_JSON:-}"
skill_dir="${ANVIL_AGENT_RUN_SKILLS_DIR:-/opt/anvil-agent-run/skills}"
goal_prompt_file="${ANVIL_CODEX_GOAL_PROMPT_FILE:-/opt/anvil-agent-run/goal-mode.md}"
goal_file="${ANVIL_CODEX_GOAL_FILE:-}"
prompt_append_file="${ANVIL_AGENT_RUN_PROMPT_APPEND_FILE:-}"
workdir="${ANVIL_CODEX_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
github_host="${ANVIL_GITHUB_HOST:-github.com}"

mkdir -p "$(dirname "${status_file}")"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export ANVIL_AGENT_RUN_STATUS_TOOL="${ANVIL_AGENT_RUN_STATUS_TOOL:-anvil-agent-status}"

if [[ -n "${GITHUB_TOKEN:-}" && -z "${GH_TOKEN:-}" ]]; then
	export GH_TOKEN="${GITHUB_TOKEN}"
fi
if [[ -n "${GH_TOKEN:-}" && -z "${GITHUB_TOKEN:-}" ]]; then
	export GITHUB_TOKEN="${GH_TOKEN}"
fi
if [[ "${github_host}" != "github.com" ]]; then
	export GH_HOST="${github_host}"
fi

if [[ -n "${CODEX_AUTH_JSON:-}" ]]; then
	mkdir -p "${CODEX_HOME:-/codex-home}"
	if [[ ! -f "${CODEX_HOME:-/codex-home}/auth.json" ]]; then
		umask 077
		printf '%s' "${CODEX_AUTH_JSON}" > "${CODEX_HOME:-/codex-home}/auth.json"
	fi
fi

mkdir -p "${workdir}"
cd "${workdir}"
git config --global --add safe.directory "${workdir}" >/dev/null 2>&1 || true

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

github_preflight() {
	local repo="${ANVIL_AGENT_RUN_GITHUB_REPOSITORY:-${repository}}"
	local permission=""
	local default_branch=""
	local status="missing-token"

	export ANVIL_AGENT_RUN_GITHUB_CAN_PUSH="false"

	git config --global user.name "${ANVIL_AGENT_GIT_AUTHOR_NAME:-Anvil AgentRun}" >/dev/null 2>&1 || true
	git config --global user.email "${ANVIL_AGENT_GIT_AUTHOR_EMAIL:-agent-run@anvil-agents.invalid}" >/dev/null 2>&1 || true
	git config --global init.defaultBranch "${ANVIL_AGENT_GIT_DEFAULT_BRANCH:-main}" >/dev/null 2>&1 || true

	if [[ -z "${GH_TOKEN:-}" ]]; then
		echo "ANVIL_AGENT_RUN_GITHUB_AUTH status=missing-token host=${github_host} repository=${repo:-unset}"
		if truthy "${ANVIL_GITHUB_AUTH_REQUIRED:-false}"; then
			echo "ANVIL_AGENT_RUN_GITHUB_AUTH_REQUIRED_MISSING host=${github_host}" >&2
			exit 1
		fi
		return 0
	fi

	if gh auth status --hostname "${github_host}" >/dev/null 2>&1; then
		status="authenticated"
	else
		status="token-present"
	fi
	if gh auth setup-git --hostname "${github_host}" --force >/dev/null 2>&1; then
		status="${status}+git-helper"
	else
		echo "ANVIL_AGENT_RUN_GITHUB_AUTH_SETUP_FAILED host=${github_host}"
	fi

	if [[ -n "${repo}" ]]; then
		permission="$(gh repo view "${repo}" --json viewerPermission --jq '.viewerPermission // "unknown"' 2>/dev/null || true)"
		default_branch="$(gh repo view "${repo}" --json defaultBranchRef --jq '.defaultBranchRef.name // "unknown"' 2>/dev/null || true)"
		case "${permission}" in
			ADMIN|MAINTAIN|WRITE)
				export ANVIL_AGENT_RUN_GITHUB_CAN_PUSH="true"
				;;
		esac
		if [[ -n "${default_branch}" ]]; then
			export ANVIL_AGENT_RUN_GITHUB_DEFAULT_BRANCH="${default_branch}"
		fi
	fi

	echo "ANVIL_AGENT_RUN_GITHUB_AUTH status=${status} host=${github_host} repository=${repo:-unset} permission=${permission:-unknown} defaultBranch=${default_branch:-unknown} canPush=${ANVIL_AGENT_RUN_GITHUB_CAN_PUSH}"
	if truthy "${ANVIL_GITHUB_AUTH_REQUIRED:-false}" && [[ "${ANVIL_AGENT_RUN_GITHUB_CAN_PUSH}" != "true" ]]; then
		echo "ANVIL_AGENT_RUN_GITHUB_AUTH_REQUIRED_INSUFFICIENT host=${github_host} repository=${repo:-unset} permission=${permission:-unknown}" >&2
		exit 1
	fi
}

workspace_empty() {
	[[ -z "$(find . -mindepth 1 -maxdepth 1 -print -quit)" ]]
}

codex_config_set_string() {
	local key="$1"
	local value="$2"
	local codex_home="${CODEX_HOME:-/codex-home}"
	local config_file="${codex_home}/config.toml"
	local tmp_file

	mkdir -p "${codex_home}"
	if [[ ! -f "${config_file}" ]]; then
		umask 077
		printf '%s = "%s"\n' "${key}" "${value}" > "${config_file}"
		return
	fi

	tmp_file="$(mktemp)"
	awk -v key="${key}" -v value="${value}" '
		BEGIN {
			written = 0
			in_top_level = 1
			replacement = key " = \"" value "\""
		}
		in_top_level && $0 ~ "^" key "[[:space:]]*=" {
			if (!written) {
				print replacement
				written = 1
			}
			next
		}
		in_top_level && $0 ~ "^[[:space:]]*\\[" {
			if (!written) {
				print replacement
				written = 1
			}
			in_top_level = 0
		}
		{ print }
		END {
			if (!written) {
				print replacement
			}
		}
	' "${config_file}" > "${tmp_file}"
	cat "${tmp_file}" > "${config_file}"
	rm -f "${tmp_file}"
	chmod 600 "${config_file}" >/dev/null 2>&1 || true
}

configure_codex_home() {
	if [[ -z "${ANVIL_CODEX_VERBOSITY:-}" ]]; then
		return
	fi
	case "${ANVIL_CODEX_VERBOSITY}" in
		low|medium|high) ;;
		*)
			echo "ANVIL_CODEX_VERBOSITY_INVALID value=${ANVIL_CODEX_VERBOSITY}" >&2
			exit 1
			;;
	esac
	codex_config_set_string "model_verbosity" "${ANVIL_CODEX_VERBOSITY}"
}

run_tool_setup() {
	local ran_any="false"
	if [[ -n "${tool_setup_files}" ]]; then
		echo "ANVIL_AGENT_RUN_TOOL_SETUP_START"
		if command -v anvil-agent-status >/dev/null 2>&1; then
			anvil-agent-status progress --stage tool-setup --summary "Preparing AgentRun tools." >/dev/null || true
		fi
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
		if command -v anvil-agent-status >/dev/null 2>&1; then
			anvil-agent-status progress --stage tool-setup --summary "AgentRun tools are ready." >/dev/null || true
		fi
	fi
}

github_preflight
configure_codex_home

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
	git fetch --all --prune >/dev/null 2>&1 || { echo "ANVIL_AGENT_RUN_REPO_FETCH_FAILED" >&2; exit 21; }
	git checkout "${repository_ref}" >/dev/null 2>&1 || git checkout -B agentrun-work "origin/${repository_ref}" >/dev/null 2>&1 || { echo "ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED ref=${repository_ref}" >&2; exit 22; }
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
	else
		echo "No immutable prompt directory was mounted at ${immutable_prompt_dir}."
	fi
	echo
	echo "# Dedicated Agent Instructions"
	if [[ -f "${agents_file}" ]]; then
		cat "${agents_file}"
	else
		echo "No dedicated agent instruction file was mounted."
	fi
	if [[ -n "${extra_prompt_dir}" && -d "${extra_prompt_dir}" ]]; then
		echo
		echo "# Extra Prompt Directory"
		while IFS= read -r -d '' prompt_part; do
			echo
			echo "## $(basename "${prompt_part}")"
			cat "${prompt_part}"
		done < <(find "${extra_prompt_dir}" -maxdepth 1 -type f -name '*.md' -print0 | sort -z)
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
			if [[ -z "${prompt_part}" ]]; then
				continue
			fi
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
	if truthy "${ANVIL_CODEX_GOAL_MODE:-}" || [[ -n "${ANVIL_CODEX_GOAL:-}" || -n "${goal_file}" ]]; then
		echo
		echo "# Codex Goal Mode"
		if [[ -f "${goal_prompt_file}" ]]; then
			cat "${goal_prompt_file}"
		else
			echo "Goal mode is enabled, but no goal prompt file was mounted at ${goal_prompt_file}."
		fi
		echo
		echo "## Goal"
		if [[ -n "${goal_file}" && -f "${goal_file}" ]]; then
			cat "${goal_file}"
		elif [[ -n "${ANVIL_CODEX_GOAL:-}" ]]; then
			printf '%s\n' "${ANVIL_CODEX_GOAL}"
		else
			echo "Use the AgentRun prompt and context as the goal."
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
	if [[ -n "${prompt_append_file}" && -f "${prompt_append_file}" ]]; then
		echo
		echo "# Runtime Prompt Append File"
		cat "${prompt_append_file}"
	fi
	if [[ -n "${ANVIL_AGENT_RUN_PROMPT_APPEND:-}" ]]; then
		echo
		echo "# Runtime Prompt Append"
		printf '%s\n' "${ANVIL_AGENT_RUN_PROMPT_APPEND}"
	fi
} > "${combined_prompt}"
chmod 600 "${combined_prompt}"

codex_args=(
	exec
	--json
	--sandbox
	"${ANVIL_CODEX_SANDBOX:-read-only}"
	--skip-git-repo-check
	-c
	"approval_policy=\"${ANVIL_CODEX_APPROVAL_POLICY:-never}\""
)

if [[ -n "${ANVIL_CODEX_MODEL:-}" ]]; then
	codex_args+=(-m "${ANVIL_CODEX_MODEL}")
fi

if [[ -n "${ANVIL_CODEX_REASONING_EFFORT:-}" ]]; then
	codex_args+=(-c "model_reasoning_effort=\"${ANVIL_CODEX_REASONING_EFFORT}\"")
fi

if [[ -n "${ANVIL_CODEX_VERBOSITY:-}" ]]; then
	codex_args+=(-c "model_verbosity=\"${ANVIL_CODEX_VERBOSITY}\"")
fi

if [[ -n "${ANVIL_CODEX_SERVICE_TIER:-}" ]]; then
	codex_args+=(-c "service_tier=\"${ANVIL_CODEX_SERVICE_TIER}\"")
fi

if [[ -n "${ANVIL_CODEX_ADDITIONAL_ARGS_JSON:-}" ]]; then
	while IFS= read -r arg; do
		codex_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_CODEX_ADDITIONAL_ARGS_JSON}")
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=${ANVIL_AGENT_RUN_BACKEND:-codex} intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
if command -v anvil-agent-status >/dev/null 2>&1; then
	anvil-agent-status progress --stage harness-start --summary "AgentRun harness started." >/dev/null || true
fi
codex "${codex_args[@]}" - < "${combined_prompt}"
if command -v anvil-agent-status >/dev/null 2>&1; then
	anvil-agent-status progress --stage harness-complete --summary "AgentRun harness completed." >/dev/null || true
fi
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
