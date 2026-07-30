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
workdir="${ANVIL_OPENCODE_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
opencode_root="${ANVIL_OPENCODE_HOME:-/opt/anvil/opencode}"
opencode_home="${ANVIL_OPENCODE_USER_HOME:-${opencode_root}/home}"

source /opt/anvil-agent-run/lib/github-auth.sh
source /opt/anvil-agent-run/lib/repository-checkout.sh
source /opt/anvil-agent-run/lib/opencode-args.sh
source /opt/anvil-agent-run/lib/capabilities.sh

export HOME="${opencode_home}"
export XDG_DATA_HOME="${XDG_DATA_HOME:-${opencode_root}/data}"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-${opencode_root}/config}"
export XDG_CACHE_HOME="${XDG_CACHE_HOME:-${opencode_root}/cache}"
export XDG_STATE_HOME="${XDG_STATE_HOME:-${opencode_root}/state}"
export OPENCODE_DISABLE_AUTOUPDATE="${OPENCODE_DISABLE_AUTOUPDATE:-true}"

mkdir -p \
	"$(dirname "${status_file}")" \
	"${HOME}" \
	"${XDG_DATA_HOME}/opencode" \
	"${XDG_CONFIG_HOME}/opencode" \
	"${XDG_CACHE_HOME}/opencode" \
	"${XDG_STATE_HOME}/opencode" \
	"${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export ANVIL_AGENT_RUN_STATUS_TOOL="${ANVIL_AGENT_RUN_STATUS_TOOL:-anvil-agent-status}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

if [[ -n "${OPENCODE_AUTH_JSON:-}" ]]; then
	if ! jq -e 'type == "object"' >/dev/null 2>&1 <<< "${OPENCODE_AUTH_JSON}"; then
		echo "ANVIL_OPENCODE_AUTH_JSON_INVALID" >&2
		anvil-agent-status needsHuman --stage harness-auth --summary "OpenCode auth JSON is invalid." >/dev/null || true
		exit 1
	fi
	auth_file="${XDG_DATA_HOME}/opencode/auth.json"
	if [[ ! -f "${auth_file}" ]]; then
		umask 077
		printf '%s' "${OPENCODE_AUTH_JSON}" > "${auth_file}"
		echo "ANVIL_OPENCODE_AUTH status=seeded"
	else
		echo "ANVIL_OPENCODE_AUTH status=existing"
	fi
fi

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
			anvil_clone_repository_url "${repository_url}" . || {
				echo "ANVIL_AGENT_RUN_REPO_CLONE_FAILED repository_url_configured=true" >&2
				exit 20
			}
		else
			echo "ANVIL_AGENT_RUN_REPO_CLONE_SKIPPED reason=workspace-not-empty"
		fi
	elif [[ -n "${repository}" ]]; then
		if workspace_empty; then
			echo "ANVIL_AGENT_RUN_REPO_CLONE repository=${repository}"
			gh repo clone "${repository}" . >/dev/null 2>&1 || {
				echo "ANVIL_AGENT_RUN_REPO_CLONE_FAILED repository=${repository}" >&2
				exit 20
			}
		else
			echo "ANVIL_AGENT_RUN_REPO_CLONE_SKIPPED reason=workspace-not-empty repository=${repository}"
		fi
	fi
fi

if [[ -d .git && -n "${repository_ref}" ]]; then
	anvil_checkout_repository_ref "${repository_ref}" || exit $?
fi

anvil_prepare_capabilities "${workdir}" "${ANVIL_AGENT_RUN_BACKEND:-openCode}"

combined_prompt="$(mktemp)"
opencode_output="$(mktemp)"
trap 'rm -f "${combined_prompt}" "${opencode_output}"' EXIT
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
	else
		echo "No dedicated agent instruction file was mounted."
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
		jq . <<< "${tools_json}"
	fi
	echo
	echo "# AgentRun Context"
	if [[ -f "${context_file}" ]]; then
		cat "${context_file}"
	else
		echo "No AgentRun context file was mounted."
	fi
	echo
	echo "# AgentRun Task"
	if [[ -f "${prompt_file}" ]]; then
		cat "${prompt_file}"
	else
		echo "No AgentRun prompt file was mounted."
	fi
} > "${combined_prompt}"

format="${ANVIL_OPENCODE_FORMAT:-json}"
if [[ -z "${format}" ]]; then
	format="json"
fi
case "${format}" in
	default|json) ;;
	*)
		echo "ANVIL_OPENCODE_FORMAT_INVALID value=${format}" >&2
		exit 1
		;;
esac

opencode_args=(run --dir "${workdir}" --format "${format}")
if truthy "${ANVIL_OPENCODE_PURE:-true}"; then
	opencode_args+=(--pure)
fi
if truthy "${ANVIL_OPENCODE_AUTO:-false}"; then
	opencode_args+=(--auto)
fi
if [[ -n "${ANVIL_OPENCODE_MODEL:-}" ]]; then
	opencode_args+=(--model "${ANVIL_OPENCODE_MODEL}")
fi
if [[ -n "${ANVIL_OPENCODE_AGENT:-}" ]]; then
	opencode_args+=(--agent "${ANVIL_OPENCODE_AGENT}")
fi
if [[ -n "${ANVIL_OPENCODE_VARIANT:-}" ]]; then
	opencode_args+=(--variant "${ANVIL_OPENCODE_VARIANT}")
fi
if [[ -n "${ANVIL_OPENCODE_ADDITIONAL_ARGS_JSON:-}" ]]; then
	if ! jq -e 'type == "array" and all(.[]; type == "string")' >/dev/null 2>&1 <<< "${ANVIL_OPENCODE_ADDITIONAL_ARGS_JSON}"; then
		echo "ANVIL_OPENCODE_ADDITIONAL_ARGS_INVALID" >&2
		exit 1
	fi
	mapfile -t additional_args < <(jq -r '.[]' <<< "${ANVIL_OPENCODE_ADDITIONAL_ARGS_JSON}")
	for additional_arg in "${additional_args[@]}"; do
		if ! anvil_opencode_additional_arg_allowed "${additional_arg}"; then
			echo "ANVIL_OPENCODE_ADDITIONAL_ARG_FORBIDDEN value=${additional_arg}" >&2
			exit 1
		fi
	done
	opencode_args+=("${additional_args[@]}")
fi

anvil-agent-status progress --stage harness-start --summary "Starting OpenCode AgentRun harness." >/dev/null || true
echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} backend=openCode"

set +e
opencode "${opencode_args[@]}" < "${combined_prompt}" 2>&1 | tee "${opencode_output}"
opencode_status="${PIPESTATUS[0]}"
set -e

fatal_event="false"
if [[ "${format}" == "json" ]] && jq -eR 'fromjson? | select(.type == "error")' "${opencode_output}" >/dev/null 2>&1; then
	fatal_event="true"
fi
if [[ "${opencode_status}" != "0" || "${fatal_event}" == "true" ]]; then
	if rg -qi 'auth|credential|api[ _-]?key|unauthorized|forbidden|status.?40[13]|login|provider.+(missing|not found)|model.+not found' "${opencode_output}"; then
		anvil-agent-status needsHuman --stage harness-auth --summary "OpenCode provider authentication or model configuration is missing, expired, or invalid." >/dev/null || true
	fi
	if [[ "${opencode_status}" == "0" ]]; then
		opencode_status="1"
	fi
	exit "${opencode_status}"
fi

anvil-agent-status progress --stage harness-complete --summary "OpenCode AgentRun harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
