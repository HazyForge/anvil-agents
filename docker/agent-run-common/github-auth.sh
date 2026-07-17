#!/usr/bin/env bash

anvil_configure_github_auth() {
	local token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
	if [[ -z "${token}" ]]; then
		return 0
	fi

	local github_host="${ANVIL_GITHUB_HOST:-github.com}"
	local config_root="${ANVIL_AGENT_RUN_GH_CONFIG_DIR:-/tmp/anvil-agent-run-gh}"
	mkdir -p "${config_root}"
	chmod 0700 "${config_root}"
	export GH_CONFIG_DIR="${config_root}"
	if [[ "${github_host}" != "github.com" ]]; then
		export GH_HOST="${github_host}"
	fi

	# Agent tool sandboxes may intentionally omit secret environment variables.
	# Persist the credential only in the pod filesystem so gh and git still work.
	if ! printf '%s\n' "${token}" \
		| env -u GH_TOKEN -u GITHUB_TOKEN \
			HOME="${HOME:-/tmp}" GH_CONFIG_DIR="${GH_CONFIG_DIR}" \
			gh auth login --hostname "${github_host}" --git-protocol https --with-token \
			>/dev/null 2>&1; then
		echo "AgentRun GitHub credential bootstrap failed for ${github_host}." >&2
		return 1
	fi
	if ! gh auth setup-git --hostname "${github_host}" --force >/dev/null 2>&1; then
		echo "AgentRun git credential-helper setup failed for ${github_host}." >&2
		return 1
	fi

	# Keep the raw token outside the model/tool environment. The pod-local gh
	# credential store remains available for authenticated gh and git commands.
	unset GH_TOKEN GITHUB_TOKEN
}
