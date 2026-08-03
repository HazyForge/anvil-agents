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
openclaw_state="${OPENCLAW_STATE_DIR:-/opt/anvil/openclaw/state}"
openclaw_workspace="${OPENCLAW_WORKSPACE_DIR:-/opt/anvil/openclaw/workspace}"
codex_home="${CODEX_HOME:-/codex-home}"

source /opt/anvil-agent-run/lib/github-auth.sh
anvil_configure_github_auth "$0" "$@"
source /opt/anvil-agent-run/lib/repository-checkout.sh

mkdir -p "$(dirname "${status_file}")" "${openclaw_state}" "${openclaw_workspace}" "${codex_home}" "${workdir}"
: > "${status_file}"
export ANVIL_AGENT_RUN_STATUS_FILE="${status_file}"
export ANVIL_AGENT_RUN_STATUS_LOG_PREFIX="${ANVIL_AGENT_RUN_STATUS_LOG_PREFIX:-ANVIL_AGENT_RUN_STATUS_JSON=}"
export OPENCLAW_STATE_DIR="${openclaw_state}"
export OPENCLAW_WORKSPACE_DIR="${openclaw_workspace}"
export CODEX_HOME="${codex_home}"

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON) return 0 ;;
		*) return 1 ;;
	esac
}

if [[ -n "${CODEX_AUTH_JSON:-}" && ! -f "${CODEX_HOME}/auth.json" ]]; then
	umask 077
	printf '%s' "${CODEX_AUTH_JSON}" > "${CODEX_HOME}/auth.json"
fi

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

if [[ ! -f "${openclaw_workspace}/AGENTS.md" ]]; then
	{
		echo "# AgentRun OpenClaw Agent"
		echo
		echo "This workspace belongs to the configured AgentRun scope."
		echo "Keep durable memory limited to that scope and the evidence needed to operate it."
		echo
		if [[ -f "${agents_file}" ]]; then
			cat "${agents_file}"
		fi
	} > "${openclaw_workspace}/AGENTS.md"
fi

if [[ ! -f "${openclaw_workspace}/TOOLS.md" ]]; then
	cat > "${openclaw_workspace}/TOOLS.md" <<'EOF'
# Tools

- Use `kubectl` for in-cluster Kubernetes inspection with the mounted service account.
- Use `anvil-observability` before raw curl for Prometheus, Loki, Tempo, and Grafana checks.
- Use `anvil-hotline` to ask one narrow human question when a configured feedback transport is available and a decision blocks safe progress (especially when the agent does not know what to do after gathering evidence).
- Use only the configured repository and issue adapters after their credential preflights succeed; do not assume GitHub or another delivery provider is present.
- Report progress with `anvil-agent-status`; the controller owns AgentRun status updates.
EOF
fi

if [[ ! -f "${openclaw_workspace}/MEMORY.md" ]]; then
	cat > "${openclaw_workspace}/MEMORY.md" <<'EOF'
# Memory

This memory belongs to AgentRuns. Record durable lessons only for the declared
scope. Do not store credentials.
EOF
fi

if [[ ! -f "${openclaw_state}/openclaw.json" && -n "${OPENAI_API_KEY:-}" ]]; then
	openclaw onboard --non-interactive --accept-risk \
		--mode local \
		--auth-choice openai-api-key \
		--secret-input-mode ref \
		--gateway-bind loopback \
		--skip-bootstrap \
		--skip-skills \
		--skip-health \
		--no-install-daemon >/dev/null 2>&1 || true
fi

openclaw_model="${ANVIL_OPENCLAW_MODEL:-}"
openclaw_provider="${ANVIL_OPENCLAW_PROVIDER:-${ANVIL_AGENT_RUN_MODEL_PROVIDER:-}}"
if [[ -n "${openclaw_provider}" && -n "${openclaw_model}" && "${openclaw_model}" != */* ]]; then
	openclaw_model="${openclaw_provider}/${openclaw_model}"
fi
if [[ -z "${openclaw_model}" ]]; then
	openclaw_model="openai/gpt-5.5"
fi

if [[ ! -f "${openclaw_state}/openclaw.json" && "${openclaw_provider}" == "xai" && -n "${XAI_API_KEY:-}" ]]; then
	openclaw onboard --non-interactive --accept-risk \
		--mode local \
		--auth-choice xai-api-key \
		--secret-input-mode ref \
		--gateway-bind loopback \
		--skip-bootstrap \
		--skip-skills \
		--skip-health \
		--no-install-daemon >/dev/null 2>&1 || true
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

if ! command -v openclaw >/dev/null 2>&1; then
	anvil-agent-status needsHuman --stage backend-start --summary "OpenClaw executable is missing from the adapter image." >/dev/null || true
	exit 1
fi

agent_id="${ANVIL_OPENCLAW_AGENT_ID:-anvil}"
session_key="agent:${agent_id}:${ANVIL_AGENT_RUN_NAMESPACE:-default}:${ANVIL_AGENT_RUN:-manual}"
timeout_seconds="${ANVIL_AGENT_RUN_BACKEND_TIMEOUT_SECONDS:-${ANVIL_AGENT_RUN_TIMEOUT_SECONDS:-1800}}"

if ! openclaw agents list --json 2>/dev/null | jq -e --arg id "${agent_id}" '.[] | select(.id == $id)' >/dev/null 2>&1; then
	openclaw agents add "${agent_id}" \
		--non-interactive \
		--workspace "${openclaw_workspace}" \
		--agent-dir "${openclaw_state}/${agent_id}" \
		--model "${openclaw_model}" \
		--json >/dev/null
fi

openclaw_args=(agent --agent "${agent_id}" --session-key "${session_key}" --message-file "${combined_prompt}" --json --timeout "${timeout_seconds}")
if [[ "${ANVIL_OPENCLAW_LOCAL:-true}" != "false" ]]; then
	openclaw_args+=(--local)
fi
if [[ -n "${openclaw_model}" ]]; then
	openclaw_args+=(--model "${openclaw_model}")
fi
if [[ -n "${ANVIL_OPENCLAW_THINKING:-}" ]]; then
	openclaw_args+=(--thinking "${ANVIL_OPENCLAW_THINKING}")
fi
if [[ -n "${ANVIL_OPENCLAW_ADDITIONAL_ARGS_JSON:-}" ]]; then
	while IFS= read -r arg; do
		openclaw_args+=("${arg}")
	done < <(jq -r '.[]' <<< "${ANVIL_OPENCLAW_ADDITIONAL_ARGS_JSON}")
fi

echo "ANVIL_AGENT_RUN_START name=${ANVIL_AGENT_RUN:-unknown} namespace=${ANVIL_AGENT_RUN_NAMESPACE:-unknown} backend=openClaw intent=${ANVIL_AGENT_RUN_INTENT:-observe}"
anvil-agent-status progress --stage harness-start --summary "OpenClaw AgentRun harness started." >/dev/null || true
openclaw_output="$(mktemp)"
set +e
openclaw "${openclaw_args[@]}" 2>&1 | tee "${openclaw_output}"
openclaw_status="${PIPESTATUS[0]}"
set -e
if [[ "${openclaw_status}" -ne 0 ]]; then
	exit "${openclaw_status}"
fi
if grep -Eq 'ProviderAuthError|FailoverError|missing-provider-auth|No API key found for provider' "${openclaw_output}"; then
	anvil-agent-status needsHuman --stage harness-auth --summary "OpenClaw model provider authentication is missing or invalid." >/dev/null || true
	exit 1
fi
anvil-agent-status progress --stage harness-complete --summary "OpenClaw AgentRun harness completed." >/dev/null || true
echo "ANVIL_AGENT_RUN_COMPLETE name=${ANVIL_AGENT_RUN:-unknown}"
