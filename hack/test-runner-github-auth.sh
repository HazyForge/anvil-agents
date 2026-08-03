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

jq -cn \
	--arg token "${TEST_INSTALLATION_TOKEN}" \
	--arg full_name "${TEST_REPOSITORY}" \
	--argjson repository_id "${TEST_REPOSITORY_ID}" \
	--argjson permissions "${TEST_PERMISSIONS_JSON}" \
	'{token:$token,expires_at:"2099-01-01T00:00:00Z",permissions:($permissions + {metadata:"read"}),repositories:[{id:$repository_id,full_name:$full_name}]}'
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
	'auth login --hostname github.com --git-protocol https --with-token')
		IFS= read -r token
		if [[ "${token}" != "${TEST_EXPECTED_GH_TOKEN}" ]]; then
			echo "gh received an unexpected credential" >&2
			exit 1
		fi
		printf '%s\n' authenticated >"${TEST_GH_LOGIN_MARKER}"
		;;
	'auth setup-git --hostname github.com --force')
		[[ -f "${TEST_GH_LOGIN_MARKER}" ]]
		;;
	*)
		echo "unexpected gh invocation: $*" >&2
		exit 1
		;;
esac
EOF

chmod 0755 "${fake_bin}/curl" "${fake_bin}/gh"

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

export GITHUB_APP_ID=123
export GITHUB_APP_INSTALLATION_ID="${TEST_INSTALLATION_ID}"
export GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")"
export ANVIL_GITHUB_APP_REPOSITORY="${TEST_REPOSITORY}"
export ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}"
export ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}"
export ANVIL_AGENT_RUN_REPOSITORY="${TEST_REPOSITORY}"
export ANVIL_AGENT_RUN_GH_CONFIG_DIR="${test_dir}/gh-config-app"

app_output_file="${test_dir}/app-output"
set +e
anvil_configure_github_auth >"${app_output_file}" 2>&1
app_status=$?
set -e
app_output="$(<"${app_output_file}")"
if [[ "${app_status}" -ne 0 ]]; then
	echo "GitHub App bootstrap failed: ${app_output}" >&2
	exit 1
fi
if [[ -n "${app_output}" ]]; then
	echo "GitHub App bootstrap emitted unexpected output: ${app_output}" >&2
	exit 1
fi
for name in GITHUB_APP_ID GITHUB_APP_INSTALLATION_ID GITHUB_APP_PRIVATE_KEY ANVIL_GITHUB_APP_REPOSITORY ANVIL_GITHUB_APP_REPOSITORY_ID ANVIL_GITHUB_APP_PERMISSIONS_JSON GH_TOKEN GITHUB_TOKEN; do
	if [[ -n "${!name+x}" ]]; then
		echo "GitHub App bootstrap retained raw environment variable ${name}" >&2
		exit 1
	fi
done
if [[ "${ANVIL_AGENT_RUN_GITHUB_AUTH_SOURCE}" != "github-app" || ! -f "${TEST_GH_LOGIN_MARKER}" ]]; then
	echo "GitHub App bootstrap did not configure gh" >&2
	exit 1
fi

TEST_GH_LOGIN_MARKER="${test_dir}/gh-login-static"
TEST_EXPECTED_GH_TOKEN=legacy-static-token
ANVIL_AGENT_RUN_GH_CONFIG_DIR="${test_dir}/gh-config-static"
export TEST_GH_LOGIN_MARKER TEST_EXPECTED_GH_TOKEN ANVIL_AGENT_RUN_GH_CONFIG_DIR
export GH_TOKEN="${TEST_EXPECTED_GH_TOKEN}"
anvil_configure_github_auth
if [[ "${ANVIL_AGENT_RUN_GITHUB_AUTH_SOURCE}" != "static-token" || -n "${GH_TOKEN+x}" || -n "${GITHUB_TOKEN+x}" ]]; then
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
	"${TEST_REPOSITORY}" "${TEST_REPOSITORY_ID}" '{"administration":"write"}' github.com
assert_rejected "does not match" \
	env ANVIL_AGENT_RUN_REPOSITORY=HazyForge/another-repo bash -c \
	'source "$1"; anvil_mint_github_app_token 123 456 "$2" HazyForge/example-agent 789 "$3" github.com' \
	_ "${root_dir}/docker/agent-run-common/github-auth.sh" "$(<"${private_key_file}")" "${TEST_PERMISSIONS_JSON}"

export GITHUB_APP_ID=123 GITHUB_APP_INSTALLATION_ID=456
export GITHUB_APP_PRIVATE_KEY="$(<"${private_key_file}")"
export ANVIL_GITHUB_APP_REPOSITORY="${TEST_REPOSITORY}"
export ANVIL_GITHUB_APP_REPOSITORY_ID="${TEST_REPOSITORY_ID}"
export ANVIL_GITHUB_APP_PERMISSIONS_JSON="${TEST_PERMISSIONS_JSON}"
export GH_TOKEN=ambiguous-static-token
assert_rejected "either a static token or a GitHub App" anvil_configure_github_auth

echo "Runner GitHub auth contract passed"
