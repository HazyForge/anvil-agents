#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

fake_bin="${test_dir}/bin"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

for name in GITHUB_APP_ID GITHUB_APP_INSTALLATION_ID GITHUB_APP_PRIVATE_KEY GH_TOKEN GITHUB_TOKEN; do
	if [[ -n "${!name+x}" ]]; then
		echo "raw credential environment reached curl: ${name}" >&2
		exit 1
	fi
done

request_body=""
endpoint=""
while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--data-binary)
			request_body="$2"
			shift 2
			;;
		https://*)
			endpoint="$1"
			shift
			;;
		*)
			shift
			;;
	esac
done
config="$(cat)"
if [[ "${config}" != *'Authorization: Bearer '* ]]; then
	echo "curl did not receive the JWT through stdin configuration" >&2
	exit 1
fi
jwt="$(sed -n 's/^header = "Authorization: Bearer \(.*\)"$/\1/p' <<< "${config}")"
if [[ "${jwt}" != *.*.* || "${jwt}" == *"${TEST_PRIVATE_MARKER}"* ]]; then
	echo "curl received an invalid or private-key-bearing JWT" >&2
	exit 1
fi
if [[ "${endpoint}" != "https://api.github.com/app/installations/${TEST_INSTALLATION_ID}/access_tokens" ]]; then
	echo "unexpected GitHub installation-token endpoint" >&2
	exit 1
fi
if ! jq -e \
	--argjson repository_id "${TEST_REPOSITORY_ID}" \
	--argjson permissions "${TEST_PERMISSIONS_JSON}" \
	'.repository_ids == [$repository_id] and .permissions == $permissions' \
	>/dev/null <<< "${request_body}"; then
	echo "installation-token request was not exactly repository and permission scoped" >&2
	exit 1
fi

response="$(jq -cn \
	--arg token "${TEST_INSTALLATION_TOKEN}" \
	--arg full_name "${TEST_REPOSITORY}" \
	--arg expires_at "${TEST_EXPIRES_AT}" \
	--argjson repository_id "${TEST_REPOSITORY_ID}" \
	--argjson permissions "${TEST_PERMISSIONS_JSON}" \
	'{token:$token,expires_at:$expires_at,permissions:$permissions,repositories:[{id:$repository_id,full_name:$full_name}]}')"
if [[ "${TEST_INCLUDE_METADATA:-false}" == "true" ]]; then
	response="$(jq -c '.permissions += {metadata:"read"}' <<< "${response}")"
fi
printf '%s\n' "${response}"
EOF

cat >"${fake_bin}/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

for name in GITHUB_APP_ID GITHUB_APP_INSTALLATION_ID GITHUB_APP_PRIVATE_KEY GH_TOKEN GITHUB_TOKEN; do
	if [[ -n "${!name+x}" ]]; then
		echo "raw credential environment reached gh: ${name}" >&2
		exit 1
	fi
done

case "$*" in
	"auth login --hostname ${TEST_EXPECTED_GH_HOST} --git-protocol https --with-token")
		IFS= read -r token
		if [[ "${token}" != "${TEST_EXPECTED_GH_TOKEN}" ]]; then
			echo "gh received an unexpected credential" >&2
			exit 1
		fi
		printf '%s\n' authenticated >"${TEST_GH_LOGIN_MARKER}"
		;;
	"auth setup-git --hostname ${TEST_EXPECTED_GH_HOST} --force")
		[[ -f "${TEST_GH_LOGIN_MARKER}" ]]
		;;
	*)
		echo "unexpected gh invocation: $*" >&2
		exit 1
		;;
esac
EOF

proc_probe="${test_dir}/github-auth-probe"
cat >"${proc_probe}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=/dev/null
source "${TEST_GITHUB_AUTH_SCRIPT}"

if [[ -n "${ANVIL_AGENT_RUN_GITHUB_AUTH_SOURCE:-}" ]]; then
	parent_environment="$(tr '\0' '\n' < "/proc/$$/environ")"
	child_view="$(bash -c 'tr "\0" "\n" < "/proc/${PPID}/environ"')"
	for exposed in GITHUB_APP_PRIVATE_KEY GITHUB_APP_ID GITHUB_APP_INSTALLATION_ID GH_TOKEN GITHUB_TOKEN; do
		if grep -Eq "^${exposed}=" <<< "${parent_environment}" || grep -Eq "^${exposed}=" <<< "${child_view}"; then
			echo "sanitized second stage exposed ${exposed} through proc" >&2
			exit 1
		fi
	done
	if [[ "${parent_environment}" == *'BEGIN PRIVATE KEY'* || "${child_view}" == *'BEGIN PRIVATE KEY'* ]]; then
		echo "sanitized second stage exposed private key bytes through proc" >&2
		exit 1
	fi
	printf 'second-stage:%s\n' "${ANVIL_AGENT_RUN_GITHUB_AUTH_SOURCE}"
	exit 0
fi

anvil_configure_github_auth "$0" "$@"
EOF

chmod 0755 "${fake_bin}/curl" "${fake_bin}/gh" "${proc_probe}"

# shellcheck source=../docker/agent-run-common/github-auth.sh
source "${root_dir}/docker/agent-run-common/github-auth.sh"

private_key_file="${test_dir}/github-app.pem"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "${private_key_file}" >/dev/null 2>&1

export PATH="${fake_bin}:${PATH}"
export TEST_PRIVATE_MARKER="PRIVATE-KEY-MARKER"
export TEST_INSTALLATION_ID=456
export TEST_REPOSITORY_ID=789
export TEST_REPOSITORY=HazyForge/example-agent
export TEST_PERMISSIONS_JSON='{"checks":"read","contents":"write","issues":"write","pull_requests":"write","statuses":"read"}'
export TEST_INSTALLATION_TOKEN=installation-token-value
export TEST_EXPECTED_GH_TOKEN="${TEST_INSTALLATION_TOKEN}"
export TEST_GH_LOGIN_MARKER="${test_dir}/gh-login"
export TEST_EXPECTED_GH_HOST=github.com
export TEST_EXPIRES_AT="$(date -u -d '+1 hour' '+%Y-%m-%dT%H:%M:%SZ')"
export TEST_GITHUB_AUTH_SCRIPT="${root_dir}/docker/agent-run-common/github-auth.sh"

app_output_file="${test_dir}/app-output"
set +e
env \
	GITHUB_APP_ID=123 \
	GITHUB_APP_INSTALLATION_ID="${TEST_INSTALLATION_ID}" \
	GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")" \
	ANVIL_GITHUB_APP_REPOSITORY="${TEST_REPOSITORY}" \
	ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}" \
	ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}" \
	ANVIL_AGENT_RUN_REPOSITORY="${TEST_REPOSITORY}" \
	ANVIL_AGENT_RUN_TIMEOUT_SECONDS=2700 \
	ANVIL_AGENT_RUN_GH_CONFIG_DIR="${test_dir}/gh-config-app" \
	"${proc_probe}" >"${app_output_file}" 2>&1
app_status=$?
set -e
app_output="$(<"${app_output_file}")"
if [[ "${app_status}" -ne 0 ]]; then
	echo "GitHub App bootstrap failed: ${app_output}" >&2
	exit 1
fi
if [[ "${app_output}" != "second-stage:github-app" || ! -f "${TEST_GH_LOGIN_MARKER}" ]]; then
	echo "GitHub App bootstrap did not configure gh" >&2
	exit 1
fi

TEST_GH_LOGIN_MARKER="${test_dir}/gh-login-static"
TEST_EXPECTED_GH_TOKEN=legacy-static-token
ANVIL_AGENT_RUN_GH_CONFIG_DIR="${test_dir}/gh-config-static"
export TEST_GH_LOGIN_MARKER TEST_EXPECTED_GH_TOKEN ANVIL_AGENT_RUN_GH_CONFIG_DIR
static_output="$(env GH_TOKEN="${TEST_EXPECTED_GH_TOKEN}" "${proc_probe}")"
if [[ "${static_output}" != "second-stage:static-token" ]]; then
	echo "legacy static-token bootstrap contract changed" >&2
	exit 1
fi

assert_rejected() {
	local expected="$1"
	shift
	local output=""
	local status=0
	set +e
	output="$("$@" 2>&1)"
	status=$?
	set -e
	if [[ "${status}" -eq 0 || "${output}" != *"${expected}"* ]]; then
		echo "expected rejection containing '${expected}', got status=${status}: ${output}" >&2
		exit 1
	fi
}

assert_rejected "unsupported, or over-privileged" \
	anvil_mint_github_app_token 123 456 "$(<"${private_key_file}")" \
	"${TEST_REPOSITORY}" "${TEST_REPOSITORY_ID}" '{"administration":"write"}' github.com 2700
assert_rejected "does not match" \
	env ANVIL_AGENT_RUN_REPOSITORY=HazyForge/another-repo bash -c \
	'source "$1"; anvil_mint_github_app_token 123 456 "$2" HazyForge/example-agent 789 "$3" github.com 2700' \
	_ "${root_dir}/docker/agent-run-common/github-auth.sh" "$(<"${private_key_file}")" "${TEST_PERMISSIONS_JSON}"

assert_rejected "either a static token or a GitHub App" env \
	GITHUB_APP_ID=123 GITHUB_APP_INSTALLATION_ID=456 \
	GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")" \
	ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}" \
	ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}" \
	GH_TOKEN=ambiguous-static-token ANVIL_AGENT_RUN_TIMEOUT_SECONDS=2700 \
	"${proc_probe}"

assert_rejected "supports github.com only" env \
	GITHUB_APP_ID=123 GITHUB_APP_INSTALLATION_ID=456 \
	GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")" \
	ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}" \
	ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}" \
	ANVIL_GITHUB_HOST=attacker.example ANVIL_AGENT_RUN_TIMEOUT_SECONDS=2700 \
	"${proc_probe}"

assert_rejected "between 1 and 3000" env \
	GITHUB_APP_ID=123 GITHUB_APP_INSTALLATION_ID=456 \
	GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")" \
	ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}" \
	ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}" \
	ANVIL_AGENT_RUN_TIMEOUT_SECONDS=3601 \
	"${proc_probe}"

short_expiry="$(date -u -d '+10 minutes' '+%Y-%m-%dT%H:%M:%SZ')"
assert_rejected "expires before" env \
	GITHUB_APP_ID=123 GITHUB_APP_INSTALLATION_ID=456 \
	GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")" \
	ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}" \
	ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}" \
	ANVIL_AGENT_RUN_TIMEOUT_SECONDS=2700 TEST_EXPIRES_AT="${short_expiry}" \
	"${proc_probe}"

[[ "$(anvil_github_auth_normalize_host GitHub.com)" == "github.com" ]]
[[ "$(anvil_github_auth_api_url GitHub.com)" == "https://api.github.com" ]]

TEST_GH_LOGIN_MARKER="${test_dir}/gh-login-ghes"
TEST_EXPECTED_GH_TOKEN=ghes-static-token
TEST_EXPECTED_GH_HOST=ghe.example.com
export TEST_GH_LOGIN_MARKER TEST_EXPECTED_GH_TOKEN TEST_EXPECTED_GH_HOST
ghes_output="$(env GH_TOKEN="${TEST_EXPECTED_GH_TOKEN}" ANVIL_GITHUB_HOST=GHE.Example.com ANVIL_AGENT_RUN_GH_CONFIG_DIR="${test_dir}/gh-config-ghes" "${proc_probe}")"
[[ "${ghes_output}" == "second-stage:static-token" ]]

echo "Runner GitHub auth contract passed"
