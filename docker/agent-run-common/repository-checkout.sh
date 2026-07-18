#!/usr/bin/env bash

# Check out a requested ref without making an already-present workspace depend
# on remote availability. Fetch only when neither the local ref nor its cached
# origin tracking ref can satisfy the request.
anvil_checkout_commitish() {
	local candidate="${1:-}"

	git rev-parse --verify --quiet --end-of-options "${candidate}^{commit}" >/dev/null 2>&1 || return 1
	git checkout --no-guess "${candidate}" -- >/dev/null 2>&1
}

anvil_checkout_origin_ref() {
	local repository_ref="${1:-}"
	local candidate="origin/${repository_ref}"

	git rev-parse --verify --quiet --end-of-options "${candidate}^{commit}" >/dev/null 2>&1 || return 1
	git checkout -B agentrun-work "${candidate}" -- >/dev/null 2>&1
}

anvil_checkout_repository_ref() {
	local repository_ref="${1:-}"

	if [[ -z "${repository_ref}" ]]; then
		return 0
	fi
	if [[ "${repository_ref}" == -* ]]; then
		echo "ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED ref=${repository_ref}" >&2
		return 22
	fi
	# Commit-ish verification prevents a tracked path with the same token from
	# being mistaken for a successful revision checkout. --no-guess prevents a
	# cached origin ref from bypassing a reachable remote refresh.
	if anvil_checkout_commitish "${repository_ref}"; then
		return 0
	fi
	if git fetch --all --prune >/dev/null 2>&1; then
		if anvil_checkout_commitish "${repository_ref}" || anvil_checkout_origin_ref "${repository_ref}"; then
			return 0
		fi
		echo "ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED ref=${repository_ref}" >&2
		return 22
	fi
	if anvil_checkout_origin_ref "${repository_ref}"; then
		return 0
	fi

	echo "ANVIL_AGENT_RUN_REPO_FETCH_FAILED" >&2
	return 21
}
