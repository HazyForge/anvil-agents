#!/usr/bin/env bash

# Check out a requested ref without making an already-present workspace depend
# on remote availability. Fetch only when neither the local ref nor its cached
# origin tracking ref can satisfy the request.
anvil_sanitize_repository_url() {
	local repository_url="${1:-}"
	case "${repository_url}" in
		http://*|https://*)
			local scheme="${repository_url%%://*}"
			local remainder="${repository_url#*://}"
			remainder="${remainder%%#*}"
			remainder="${remainder%%\?*}"
			local authority="${remainder%%/*}"
			local path=""
			if [[ "${remainder}" == */* ]]; then
				path="/${remainder#*/}"
			fi
			authority="${authority##*@}"
			[[ -n "${authority}" ]] || return 1
			printf '%s://%s%s\n' "${scheme}" "${authority}" "${path}"
			;;
		ssh://*)
			[[ "${repository_url}" != *\?* && "${repository_url}" != *#* ]] || return 1
			local remainder="${repository_url#*://}"
			local authority="${remainder%%/*}"
			if [[ "${authority}" == *@* ]]; then
				local username="${authority%%@*}"
				[[ "${username}" =~ ^[A-Za-z0-9._-]+$ ]] || return 1
			fi
			[[ -n "${authority##*@}" ]] || return 1
			printf '%s\n' "${repository_url}"
			;;
		git://*)
			[[ "${repository_url}" != *@* && "${repository_url}" != *\?* && "${repository_url}" != *#* ]] || return 1
			[[ -n "${repository_url#git://}" ]] || return 1
			printf '%s\n' "${repository_url}"
			;;
		file://*)
			[[ "${repository_url}" != *@* && "${repository_url}" != *\?* && "${repository_url}" != *#* ]] || return 1
			printf '%s\n' "${repository_url}"
			;;
		*://*)
			# Git supports additional URI schemes, but there is no generic way to
			# separate their authentication material from the persisted origin.
			return 1
			;;
		*)
			if [[ "${repository_url}" == *@*:* ]]; then
				local scp_username="${repository_url%%@*}"
				[[ "${scp_username}" =~ ^[A-Za-z0-9._-]+$ ]] || return 1
			fi
			printf '%s\n' "${repository_url}"
			;;
	esac
}

anvil_clone_repository_url() {
	local repository_url="${1:-}"
	local destination="${2:-.}"
	local sanitized_url

	sanitized_url="$(anvil_sanitize_repository_url "${repository_url}")" || return 1
	if [[ "${sanitized_url}" != "${repository_url}" ]]; then
		local config_count="${GIT_CONFIG_COUNT:-0}"
		[[ "${config_count}" =~ ^[0-9]+$ ]] || return 1
		(
			local key_name="GIT_CONFIG_KEY_${config_count}"
			local value_name="GIT_CONFIG_VALUE_${config_count}"
			printf -v "${key_name}" '%s' "url.${repository_url}.insteadOf"
			printf -v "${value_name}" '%s' "${sanitized_url}"
			export "${key_name}" "${value_name}"
			export GIT_CONFIG_COUNT="$((config_count + 1))"
			git clone "${sanitized_url}" "${destination}" >/dev/null 2>&1
		) || return 1
	else
		git clone "${repository_url}" "${destination}" >/dev/null 2>&1 || return 1
	fi
}

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
