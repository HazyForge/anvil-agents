#!/usr/bin/env bash
set -euo pipefail

prompt_file="${ANVIL_AGENT_RUN_PROMPT_FILE:-/var/run/anvil-agent-run/prompt.md}"
context_file="${ANVIL_AGENT_RUN_CONTEXT_FILE:-/var/run/anvil-agent-run/source.json}"
agents_file="${ANVIL_AGENT_RUN_AGENTS_FILE:-/opt/anvil-agent-run/AGENTS.md}"
immutable_prompt_dir="/opt/anvil-agent-run/static-prompts"
skill_files="${ANVIL_AGENT_RUN_SKILL_FILES:-}"
skill_dir="${ANVIL_AGENT_RUN_SKILLS_DIR:-/opt/anvil-agent-run/skills}"
workdir="${ANVIL_AGENT_RUN_WORKDIR:-/workspace}"
status_file="${ANVIL_AGENT_RUN_STATUS_FILE:-/tmp/anvil-agent-run-status/status.jsonl}"
openclaw_state="${OPENCLAW_STATE_DIR:-/opt/anvil/openclaw/state}"
openclaw_workspace="${OPENCLAW_WORKSPACE_DIR:-/opt/anvil/openclaw/workspace}"
codex_home="${CODEX_HOME:-/codex-home}"

source /opt/anvil-agent-run/lib/github-auth.sh

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

anvil_configure_github_auth

cd "${workdir}"
git config --global --add safe.directory "${workdir}" >/dev/null 2>&1 || true

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
- Use `anvil-agent-feedback` to ask one narrow Discord-backed operator question when a human decision blocks safe progress.
- Use `gh` for GitHub issue, branch, and pull request work when the adapter's runtime GitHub credential preflight succeeds.
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
