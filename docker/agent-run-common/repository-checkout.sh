#!/usr/bin/env bash

# Check out a requested ref without making an already-present workspace depend
# on remote availability. Fetch only when neither the local ref nor its cached
# origin tracking ref can satisfy the request.
anvil_checkout_repository_ref() {
	local repository_ref="${1:-}"

	if [[ -z "${repository_ref}" ]]; then
		return 0
	fi
	if [[ "${repository_ref}" == -* ]]; then
		echo "ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED ref=${repository_ref}" >&2
		return 22
	fi
	# --no-guess prevents a cached origin ref from being mistaken for an exact
	# local ref before we have had a chance to refresh it.
	if git checkout --no-guess "${repository_ref}" >/dev/null 2>&1; then
		return 0
	fi
	if git fetch --all --prune >/dev/null 2>&1; then
		if git checkout "${repository_ref}" >/dev/null 2>&1 || \
			git checkout -B agentrun-work "origin/${repository_ref}" >/dev/null 2>&1; then
			return 0
		fi
		echo "ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED ref=${repository_ref}" >&2
		return 22
	fi
	if git checkout -B agentrun-work "origin/${repository_ref}" >/dev/null 2>&1; then
		return 0
	fi

	echo "ANVIL_AGENT_RUN_REPO_FETCH_FAILED" >&2
	return 21
}
