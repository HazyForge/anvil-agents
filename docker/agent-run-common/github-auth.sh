#!/usr/bin/env bash

anvil_github_auth_base64url() {
	openssl base64 -A | tr '+/' '-_' | tr -d '='
}

anvil_github_auth_clear_app_environment() {
	unset \
		GITHUB_APP_ID \
		GITHUB_APP_INSTALLATION_ID \
		GITHUB_APP_PRIVATE_KEY \
		ANVIL_GITHUB_APP_REPOSITORY \
		ANVIL_GITHUB_APP_REPOSITORY_ID \
		ANVIL_GITHUB_APP_PERMISSIONS_JSON \
		ANVIL_GITHUB_HOST
}

anvil_github_auth_trim() {
	local value="${1-}"
	value="${value#"${value%%[![:space:]]*}"}"
	value="${value%"${value##*[![:space:]]}"}"
	printf '%s' "${value}"
}

# Bounded network/PVC helpers. Default stays well under a typical 8s /events poll
# so ANVIL_AGENT_RUN_START can still appear after a skipped or timed-out bootstrap.
anvil_github_auth_positive_seconds() {
	local value="${1:-}"
	local fallback="${2:-5}"
	local max="${3:-30}"
	if [[ ! "${value}" =~ ^[1-9][0-9]*$ ]] || ((value > max)); then
		printf '%s' "${fallback}"
		return 0
	fi
	printf '%s' "${value}"
}

anvil_github_auth_bootstrap_seconds() {
	anvil_github_auth_positive_seconds "${ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT_SECONDS:-5}" 5 30
}

# Run COMMAND with a hard deadline. Prefer timeout(1). On timeout the status is 124.
anvil_run_bounded() {
	local duration="${1:-}"
	shift
	if [[ -z "${duration}" ]]; then
		"$@"
		return $?
	fi
	if command -v timeout >/dev/null 2>&1; then
		timeout --signal=TERM --kill-after=2 "${duration}" "$@"
		return $?
	fi
	"$@"
}

# Same deadline as anvil_run_bounded, but do not wait(1) after kill. An
# uninterruptible grok-build PVC syscall must not block ANVIL_AGENT_RUN_START.
anvil_run_bounded_detach() {
	local duration="${1:-}"
	shift
	local pid=""
	local waited=0
	if [[ ! "${duration}" =~ ^[1-9][0-9]*$ ]]; then
		"$@"
		return $?
	fi
	"$@" &
	pid=$!
	while kill -0 "${pid}" 2>/dev/null; do
		if ((waited >= duration)); then
			kill -TERM "${pid}" 2>/dev/null || true
			sleep 1
			kill -KILL "${pid}" 2>/dev/null || true
			return 124
		fi
		sleep 1
		waited=$((waited + 1))
	done
	wait "${pid}"
	return $?
}

anvil_github_auth_timeout_status() {
	local status="${1:-0}"
	[[ "${status}" -eq 124 || "${status}" -eq 137 || "${status}" -eq 28 ]]
}

anvil_github_auth_normalize_host() {
	local github_host="${1:-}"
	if [[ -z "${github_host}" || ! "${github_host}" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ || "${github_host}" == *..* ]]; then
		return 1
	fi
	printf '%s\n' "${github_host,,}"
}

anvil_github_auth_api_url() {
	local github_host=""
	github_host="$(anvil_github_auth_normalize_host "${1:-}")" || return 1
	if [[ "${github_host}" == "github.com" ]]; then
		printf '%s\n' "https://api.github.com"
	else
		printf 'https://%s/api/v3\n' "${github_host}"
	fi
}

anvil_github_auth_permissions() {
	local permissions_json="${1:-}"
	jq -ce '
		if (
			type == "object" and length > 0 and
			([keys[]] - ["checks", "contents", "issues", "metadata", "pull_requests", "statuses"] | length == 0) and
			all(to_entries[];
				if (.key == "checks" or .key == "metadata" or .key == "statuses") then
					.value == "read"
				else
					(.value == "read" or .value == "write")
				end)
		) then . else error("unsupported GitHub App permission set") end
	' <<< "${permissions_json}"
}

anvil_mint_github_app_token() {
	local app_id="${1:-}"
	local installation_id="${2:-}"
	local private_key="${3:-}"
	local repository="${4:-}"
	local repository_id="${5:-}"
	local permissions_json="${6:-}"
	local github_host="${7:-github.com}"
	local timeout_seconds="${8:-}"
	local scoped_repository="${repository}"
	local repository_name=""
	local api_url=""
	local now=""
	local jwt_header=""
	local jwt_payload=""
	local jwt_input=""
	local jwt_signature=""
	local jwt=""
	local request_body=""
	local response=""
	local token=""
	local expires_at=""
	local expires_epoch=""
	local http_timeout=""
	local curl_status=0
	http_timeout="$(anvil_github_auth_bootstrap_seconds)"

	if [[ ! "${app_id}" =~ ^[1-9][0-9]*$ || ! "${installation_id}" =~ ^[1-9][0-9]*$ ]]; then
		echo "AgentRun GitHub App IDs must be positive integers." >&2
		return 1
	fi
	if [[ -z "${private_key}" ]]; then
		echo "AgentRun GitHub App private key is required." >&2
		return 1
	fi
	if [[ -n "${repository_id}" && ! "${repository_id}" =~ ^[1-9][0-9]*$ ]]; then
		echo "ANVIL_GITHUB_APP_REPOSITORY_ID must be a positive integer." >&2
		return 1
	fi
	if [[ -n "${repository}" ]]; then
		if [[ ! "${repository}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
			echo "ANVIL_GITHUB_APP_REPOSITORY must use exact owner/repository form." >&2
			return 1
		fi
		repository_name="${repository#*/}"
	fi
	if [[ -z "${repository}" && -z "${repository_id}" ]]; then
		echo "AgentRun GitHub App auth requires an exact repository name or repository ID." >&2
		return 1
	fi
	if [[ -n "${ANVIL_AGENT_RUN_REPOSITORY:-}" ]]; then
		if [[ ! "${ANVIL_AGENT_RUN_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
			echo "ANVIL_AGENT_RUN_REPOSITORY must use exact owner/repository form for GitHub App auth." >&2
			return 1
		fi
		if [[ -n "${repository}" && "${repository,,}" != "${ANVIL_AGENT_RUN_REPOSITORY,,}" ]]; then
			echo "GitHub App repository scope does not match the AgentRun repository." >&2
			return 1
		fi
		if [[ -z "${scoped_repository}" ]]; then
			scoped_repository="${ANVIL_AGENT_RUN_REPOSITORY}"
		fi
	fi
	if ! permissions_json="$(anvil_github_auth_permissions "${permissions_json}")"; then
		echo "ANVIL_GITHUB_APP_PERMISSIONS_JSON contains an empty, unsupported, or over-privileged permission set." >&2
		return 1
	fi
	github_host="$(anvil_github_auth_normalize_host "${github_host}")" || {
		echo "ANVIL_GITHUB_HOST is not a valid exact GitHub hostname." >&2
		return 1
	}
	if [[ "${github_host}" != "github.com" ]]; then
		echo "AgentRun GitHub App auth currently supports github.com only." >&2
		return 1
	fi
	if [[ ! "${timeout_seconds}" =~ ^[1-9][0-9]*$ || "${timeout_seconds}" -gt 3000 ]]; then
		echo "AgentRun GitHub App auth requires timeoutSeconds between 1 and 3000." >&2
		return 1
	fi
	api_url="$(anvil_github_auth_api_url "${github_host}")" || {
		echo "ANVIL_GITHUB_HOST is not a valid exact GitHub hostname." >&2
		return 1
	}

	# Kubernetes Secret data sometimes preserves PEM newlines as literal \n.
	# Normalize in the shell after the exported source variable has been unset.
	private_key="${private_key//\\n/$'\n'}"
	now="$(date +%s)"
	jwt_header="$(printf '%s' '{"alg":"RS256","typ":"JWT"}' | anvil_github_auth_base64url)"
	jwt_payload="$(jq -cn --argjson iat "$((now - 60))" --argjson exp "$((now + 540))" --arg iss "${app_id}" '{iat:$iat,exp:$exp,iss:$iss}' | anvil_github_auth_base64url)"
	jwt_input="${jwt_header}.${jwt_payload}"
	if ! jwt_signature="$(printf '%s' "${jwt_input}" | openssl dgst -sha256 -sign <(printf '%s' "${private_key}") -binary 2>/dev/null | anvil_github_auth_base64url)" || [[ -z "${jwt_signature}" ]]; then
		echo "AgentRun GitHub App private key could not sign an authentication JWT." >&2
		return 1
	fi
	private_key=""
	jwt="${jwt_input}.${jwt_signature}"
	jwt_input=""
	jwt_signature=""

	if [[ -n "${repository_id}" ]]; then
		request_body="$(jq -cn --argjson repository_id "${repository_id}" --argjson permissions "${permissions_json}" '{repository_ids:[$repository_id],permissions:$permissions}')"
	else
		request_body="$(jq -cn --arg repository "${repository_name}" --argjson permissions "${permissions_json}" '{repositories:[$repository],permissions:$permissions}')"
	fi

	# Feed the JWT header through curl's stdin configuration so it never appears
	# in argv or the inherited process environment. Bound connect/transfer so a
	# blackholed GitHub API cannot hang the runner before ANVIL_AGENT_RUN_START.
	# Do not toggle errexit: a timeout return must remain catchable by the caller.
	response="$({
		printf '%s\n' 'silent'
		printf '%s\n' 'show-error'
		printf '%s\n' 'fail-with-body'
		printf '%s\n' 'header = "Accept: application/vnd.github+json"'
		printf '%s\n' 'header = "X-GitHub-Api-Version: 2022-11-28"'
		printf 'connect-timeout = %s\n' "${http_timeout}"
		printf 'max-time = %s\n' "${http_timeout}"
		printf 'header = "Authorization: Bearer %s"\n' "${jwt}"
	} | curl --config - --request POST --data-binary "${request_body}" "${api_url}/app/installations/${installation_id}/access_tokens")" || curl_status=$?
	if [[ "${curl_status}" -ne 0 ]]; then
		jwt=""
		if anvil_github_auth_timeout_status "${curl_status}"; then
			echo "ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT host=${github_host} command=mint timeoutSeconds=${http_timeout}"
			return 124
		fi
		echo "AgentRun GitHub App installation-token request failed." >&2
		return 1
	fi
	jwt=""

	if ! jq -e --argjson expected_permissions "${permissions_json}" '
		(.token | type == "string" and length > 0) and
		(.expires_at | type == "string" and length > 0) and
		(.permissions == $expected_permissions or .permissions == ($expected_permissions + {metadata:"read"})) and
		(.repositories | type == "array" and length == 1)
	' >/dev/null <<< "${response}"; then
		echo "GitHub returned a token outside the requested repository or permission boundary." >&2
		return 1
	fi
	if [[ -n "${repository_id}" ]] && ! jq -e --argjson expected_id "${repository_id}" '.repositories[0].id == $expected_id' >/dev/null <<< "${response}"; then
		echo "GitHub installation token did not resolve the requested repository ID." >&2
		return 1
	fi
	if [[ -n "${scoped_repository}" ]] && ! jq -e --arg expected_name "${scoped_repository,,}" '(.repositories[0].full_name | ascii_downcase) == $expected_name' >/dev/null <<< "${response}"; then
		echo "GitHub installation token did not resolve the requested repository name." >&2
		return 1
	fi
	expires_at="$(jq -r '.expires_at' <<< "${response}")"
	if ! expires_epoch="$(date -u -d "${expires_at}" +%s 2>/dev/null)" || [[ ! "${expires_epoch}" =~ ^[0-9]+$ ]]; then
		echo "GitHub installation token returned an invalid expiration time." >&2
		return 1
	fi
	now="$(date +%s)"
	if (( expires_epoch - now < timeout_seconds + 300 )); then
		echo "GitHub installation token expires before the bounded AgentRun can finish safely." >&2
		return 1
	fi
	token="$(jq -r '.token' <<< "${response}")"
	response=""
	printf '%s' "${token}"
	token=""
}

anvil_github_auth_reexec_sanitized() {
	local auth_source="${1:-}"
	local github_host="${2:-}"
	local entrypoint="${3:-}"
	shift 3 || true
	if [[ -z "${entrypoint}" || ! -x "${entrypoint}" ]]; then
		echo "AgentRun GitHub auth requires an executable second-stage entrypoint." >&2
		return 1
	fi
	# An unset shell variable remains recoverable from /proc/1/environ until the
	# process image is replaced. Re-exec the runner with a newly constructed
	# environment before any model or tool process starts.
	exec env \
		-u GH_TOKEN \
		-u GITHUB_TOKEN \
		-u GH_HOST \
		-u GITHUB_APP_ID \
		-u GITHUB_APP_INSTALLATION_ID \
		-u GITHUB_APP_PRIVATE_KEY \
		-u ANVIL_GITHUB_APP_REPOSITORY \
		-u ANVIL_GITHUB_APP_REPOSITORY_ID \
		-u ANVIL_GITHUB_APP_PERMISSIONS_JSON \
		-u ANVIL_GITHUB_HOST \
		${github_host:+GH_HOST="${github_host}"} \
		ANVIL_AGENT_RUN_GITHUB_AUTH_SOURCE="${auth_source}" \
		"${entrypoint}" "$@"
}

anvil_configure_github_token() {
	local token="${1:-}"
	local github_host="${2:-github.com}"
	local config_root="${ANVIL_AGENT_RUN_GH_CONFIG_DIR:-/tmp/anvil-agent-run-gh}"
	local bootstrap_seconds=""
	local gh_status=0
	local -a gh_cmd=(gh)

	token="$(anvil_github_auth_trim "${token}")"
	[[ -n "${token}" ]] || return 0
	mkdir -p "${config_root}"
	chmod 0700 "${config_root}"
	export GH_CONFIG_DIR="${config_root}"
	if [[ "${github_host}" != "github.com" ]]; then
		export GH_HOST="${github_host}"
	fi
	bootstrap_seconds="$(anvil_github_auth_bootstrap_seconds)"
	if command -v timeout >/dev/null 2>&1; then
		gh_cmd=(timeout --signal=TERM --kill-after=2 "${bootstrap_seconds}" gh)
	fi

	# Agent tool sandboxes intentionally omit raw credential environment
	# variables. gh keeps the scoped token only in its pod-local credential
	# store so authenticated gh and git operations remain available.
	echo "ANVIL_AGENT_RUN_GITHUB_AUTH_LOGIN host=${github_host} timeoutSeconds=${bootstrap_seconds}"
	printf '%s\n' "${token}" \
		| env -u GH_TOKEN -u GITHUB_TOKEN \
			HOME="${HOME:-/tmp}" GH_CONFIG_DIR="${GH_CONFIG_DIR}" \
			"${gh_cmd[@]}" auth login --hostname "${github_host}" --git-protocol https --with-token \
			>/dev/null 2>&1 || gh_status=$?
	if [[ "${gh_status}" -ne 0 ]]; then
		if anvil_github_auth_timeout_status "${gh_status}"; then
			echo "ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT host=${github_host} command=login timeoutSeconds=${bootstrap_seconds}"
			return 124
		fi
		echo "AgentRun GitHub credential bootstrap failed for ${github_host}." >&2
		return 1
	fi
	env -u GH_TOKEN -u GITHUB_TOKEN "${gh_cmd[@]}" auth setup-git --hostname "${github_host}" --force >/dev/null 2>&1 || gh_status=$?
	if [[ "${gh_status}" -ne 0 ]]; then
		if anvil_github_auth_timeout_status "${gh_status}"; then
			echo "ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT host=${github_host} command=setup-git timeoutSeconds=${bootstrap_seconds}"
			return 124
		fi
		echo "AgentRun git credential-helper setup failed for ${github_host}." >&2
		return 1
	fi
}

anvil_configure_github_auth() {
	local entrypoint="${1:-}"
	shift || true
	local token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
	local app_id="${GITHUB_APP_ID:-}"
	local installation_id="${GITHUB_APP_INSTALLATION_ID:-}"
	local private_key="${GITHUB_APP_PRIVATE_KEY:-}"
	local repository="${ANVIL_GITHUB_APP_REPOSITORY:-}"
	local repository_id="${ANVIL_GITHUB_APP_REPOSITORY_ID:-}"
	local permissions_json="${ANVIL_GITHUB_APP_PERMISSIONS_JSON:-}"
	local github_host="${ANVIL_GITHUB_HOST:-github.com}"
	local app_configured="false"
	local minted_token=""
	local timeout_seconds="${ANVIL_AGENT_RUN_TIMEOUT_SECONDS:-}"
	local token_status=0

	token="$(anvil_github_auth_trim "${token}")"
	app_id="$(anvil_github_auth_trim "${app_id}")"
	installation_id="$(anvil_github_auth_trim "${installation_id}")"
	# PEM payloads keep interior and trailing newlines; only blank a whitespace-only key.
	if [[ -z "$(anvil_github_auth_trim "${private_key}")" ]]; then
		private_key=""
	fi
	repository="$(anvil_github_auth_trim "${repository}")"
	repository_id="$(anvil_github_auth_trim "${repository_id}")"
	permissions_json="$(anvil_github_auth_trim "${permissions_json}")"
	github_host="$(anvil_github_auth_trim "${github_host}")"

	if [[ -n "${app_id}" || -n "${installation_id}" || -n "${private_key}" || -n "${repository}" || -n "${repository_id}" || -n "${permissions_json}" ]]; then
		app_configured="true"
	fi
	# Capture credential inputs in non-exported locals, then remove every raw
	# bootstrap value before invoking jq, openssl, curl, gh, git, or a model.
	unset GH_TOKEN GITHUB_TOKEN
	anvil_github_auth_clear_app_environment
	github_host="$(anvil_github_auth_normalize_host "${github_host}")" || {
		echo "ANVIL_GITHUB_HOST is not a valid exact GitHub hostname." >&2
		return 1
	}

	if [[ -z "${token}" && "${app_configured}" != "true" ]]; then
		echo "ANVIL_AGENT_RUN_GITHUB_AUTH_SKIPPED reason=empty-credentials host=${github_host}"
		return 0
	fi
	if [[ -n "${token}" && "${app_configured}" == "true" ]]; then
		echo "AgentRun GitHub auth must select either a static token or a GitHub App, not both." >&2
		return 1
	fi
	if [[ -n "${token}" ]]; then
		anvil_configure_github_token "${token}" "${github_host}" || token_status=$?
		token=""
		if [[ "${token_status}" -ne 0 ]]; then
			if anvil_github_auth_timeout_status "${token_status}"; then
				anvil_github_auth_reexec_sanitized skipped "${github_host}" "${entrypoint}" "$@"
			fi
			return 1
		fi
		anvil_github_auth_reexec_sanitized static-token "${github_host}" "${entrypoint}" "$@"
	fi
	if [[ "${app_configured}" != "true" ]]; then
		return 0
	fi
	if [[ -z "${app_id}" || -z "${installation_id}" || -z "${private_key}" || -z "${permissions_json}" ]]; then
		echo "ANVIL_AGENT_RUN_GITHUB_AUTH_SKIPPED reason=incomplete-github-app host=${github_host}"
		return 0
	fi
	minted_token="$(anvil_mint_github_app_token \
		"${app_id}" "${installation_id}" "${private_key}" \
		"${repository}" "${repository_id}" "${permissions_json}" "${github_host}" "${timeout_seconds}")" || token_status=$?
	if [[ "${token_status}" -ne 0 ]]; then
		private_key=""
		if anvil_github_auth_timeout_status "${token_status}"; then
			anvil_github_auth_reexec_sanitized skipped "${github_host}" "${entrypoint}" "$@"
		fi
		return 1
	fi
	private_key=""
	anvil_configure_github_token "${minted_token}" "${github_host}" || token_status=$?
	if [[ "${token_status}" -ne 0 ]]; then
		minted_token=""
		if anvil_github_auth_timeout_status "${token_status}"; then
			anvil_github_auth_reexec_sanitized skipped "${github_host}" "${entrypoint}" "$@"
		fi
		return 1
	fi
	minted_token=""
	anvil_github_auth_reexec_sanitized github-app "${github_host}" "${entrypoint}" "$@"
}
