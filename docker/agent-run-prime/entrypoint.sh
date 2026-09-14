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
workdir="${ANVIL_PRIME_WORKDIR:-${ANVIL_AGENT_RUN_WORKDIR:-/workspace}}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
repository="${ANVIL_AGENT_RUN_REPOSITORY:-}"
repository_url="${ANVIL_AGENT_RUN_REPOSITORY_URL:-}"
repository_ref="${ANVIL_AGENT_RUN_REPOSITORY_REF:-}"
prime_root="${ANVIL_PRIME_HOME:-/opt/anvil/prime}"
prime_home="${ANVIL_PRIME_USER_HOME:-${prime_root}/home}"

# Adapt the existing provider-neutral Secret key only for its declared provider.
# Keep an explicitly injected native key authoritative; never put keys in argv.
if [[ "${ANVIL_AGENT_RUN_MODEL_PROVIDER:-${ANVIL_PRIME_MODEL_PROVIDER:-}}" == deepseek && "${ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE:-${ANVIL_PRIME_PROVIDER_AUTH_MODE:-}}" == apiKey ]]; then
	if [[ -z "${DEEPSEEK_API_KEY:-}" && -n "${apiKey:-}" ]]; then
		export DEEPSEEK_API_KEY="${apiKey}"
	fi
	unset apiKey
fi

runner_lib_dir="${ANVIL_AGENT_RUN_LIB_DIR:-/opt/anvil-agent-run/lib}"
source "${runner_lib_dir}/github-auth.sh"
anvil_configure_github_auth "$0" "$@"
source "${runner_lib_dir}/repository-checkout.sh"

export HOME="${prime_home}"
export PRIME_AGENT_CODING_AGENT_DIR="${PRIME_AGENT_CODING_AGENT_DIR:-${prime_root}/agent}"
export PRIME_AGENT_SESSION_DIR="${PRIME_AGENT_SESSION_DIR:-${prime_root}/sessions}"

mkdir -p "$(dirname "${status_file}")" "${HOME}" "${PRIME_AGENT_CODING_AGENT_DIR}" "${PRIME_AGENT_SESSION_DIR}" "${workdir}"
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

run_tool_setup

combined_prompt="$(mktemp)"
prime_output="$(mktemp)"
trap 'rm -f "${combined_prompt}" "${prime_output}"' EXIT
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
	if [[ -n "${ANVIL_AGENT_RUN_PROMPT_APPEND:-}" ]]; then
		echo
		echo "# Runtime Prompt Append"
		printf '%s\n' "${ANVIL_AGENT_RUN_PROMPT_APPEND}"
	fi
} > "${combined_prompt}"
chmod 600 "${combined_prompt}"

prime_mode="${ANVIL_PRIME_MODE:-json}"
prime_thinking="${ANVIL_PRIME_THINKING:-}"
case "${prime_mode}" in json|text) ;; *) echo 'ANVIL_PRIME_MODE_INVALID' >&2; exit 2 ;; esac
case "${prime_thinking}" in ''|off|minimal|low|medium|high|xhigh|max) ;; *) echo 'ANVIL_PRIME_THINKING_INVALID' >&2; exit 2 ;; esac
turn_timeout="${ANVIL_PRIME_TURN_TIMEOUT_SECONDS:-280}"
if [[ ! "${turn_timeout}" =~ ^[1-9][0-9]{0,4}$ ]] || (( turn_timeout > 86400 )); then
	echo 'ANVIL_PRIME_TIMEOUT_INVALID' >&2; exit 2
fi

prime_args=(--print --mode "${prime_mode}" --cwd "${workdir}")
if [[ -n "${ANVIL_PRIME_PROVIDER:-}" ]]; then prime_args+=(--provider "${ANVIL_PRIME_PROVIDER}"); fi
if [[ -n "${ANVIL_PRIME_MODEL:-}" ]]; then prime_args+=(--model "${ANVIL_PRIME_MODEL}"); fi
if [[ -n "${prime_thinking}" ]]; then prime_args+=(--thinking "${prime_thinking}"); fi
if truthy "${ANVIL_PRIME_NO_SESSION:-false}"; then prime_args+=(--no-session); fi
# Keep prompt, credentials, model, tools and execution mode in their dedicated
# fields. Additional args cannot replace stdin, enter RPC, or load executable code.
if [[ -n "${ANVIL_PRIME_ADDITIONAL_ARGS_JSON:-}" ]]; then
	if ! jq -e 'type == "array" and length <= 16 and all(.[]; type == "string" and (length <= 128))' >/dev/null 2>&1 <<< "${ANVIL_PRIME_ADDITIONAL_ARGS_JSON}"; then
		echo 'ANVIL_PRIME_ADDITIONAL_ARGS_INVALID' >&2; exit 2
	fi
	mapfile -t extra_args < <(jq -r '.[]' <<< "${ANVIL_PRIME_ADDITIONAL_ARGS_JSON}")
	for arg in "${extra_args[@]}"; do
		case "${arg}" in
			--verbose|--no-extensions|--no-context-files|--no-prompt-templates) prime_args+=("${arg}") ;;
			*) echo 'ANVIL_PRIME_ADDITIONAL_ARG_FORBIDDEN' >&2; exit 2 ;;
		esac
	done
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=primeAgent intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "Prime Agent harness started." >/dev/null || true
# Native print mode reads piped stdin to EOF before starting its turn. The
# complete prompt never appears in process argv. Tools remain natively enabled.
set +e
timeout --signal=TERM --kill-after=10s "${turn_timeout}s" prime-agent "${prime_args[@]}" < "${combined_prompt}" 2>&1 | tee "${prime_output}"
prime_status="${PIPESTATUS[0]}"
set -e
if [[ "${prime_status}" -ne 0 ]]; then exit "${prime_status}"; fi
if [[ "${prime_mode}" == json ]]; then
	# A clean process exit alone is not a completed model turn. Native errors
	# and aborted/empty turns must never become an Anvil success marker.
	if ! jq -eRs 'split("\n") | map(fromjson? | select(.type == "agent_end")) | last | .messages[-1] | .role == "assistant" and .stopReason == "stop" and any(.content[]?; .type == "text" and (.text | length > 0))' "${prime_output}" >/dev/null 2>&1; then
		echo 'ANVIL_PRIME_TERMINAL_REPLY_MISSING' >&2; exit 1
	fi
elif [[ ! -s "${prime_output}" ]]; then
	echo 'ANVIL_PRIME_REPLY_MISSING' >&2; exit 1
fi
anvil-agent-status progress --stage harness-complete --summary "Prime Agent harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
