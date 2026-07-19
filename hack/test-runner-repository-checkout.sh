#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

# shellcheck source=../docker/agent-run-common/repository-checkout.sh
source "${root_dir}/docker/agent-run-common/repository-checkout.sh"

worktree="${test_dir}/worktree"
git init --quiet --initial-branch=main "${worktree}"
git -C "${worktree}" config user.name "Runner Contract"
git -C "${worktree}" config user.email "runner-contract@example.invalid"
touch "${worktree}/tracked"
printf 'tracked collision\n' > "${worktree}/missing-ref"
mkdir -p "${worktree}/missing-dir"
printf 'tracked directory collision\n' > "${worktree}/missing-dir/file"
git -C "${worktree}" add tracked missing-ref missing-dir/file
git -C "${worktree}" commit --quiet -m initial
git -C "${worktree}" branch local-ready
git -C "${worktree}" remote add origin "file://${test_dir}/unreachable.git"

cd "${worktree}"
anvil_checkout_repository_ref local-ready
if [[ "$(git branch --show-current)" != "local-ready" ]]; then
	echo "runner checkout did not use the available local ref" >&2
	exit 1
fi

git update-ref refs/remotes/origin/cached-only HEAD
anvil_checkout_repository_ref cached-only
if [[ "$(git branch --show-current)" != "agentrun-work" ]]; then
	echo "runner checkout did not use the cached remote ref while offline" >&2
	exit 1
fi

set +e
missing_output="$(anvil_checkout_repository_ref missing-ref 2>&1)"
missing_status=$?
set -e
if [[ "${missing_status}" -ne 21 ]]; then
	echo "missing ref with unreachable remote exited ${missing_status}, want 21" >&2
	exit 1
fi
if [[ "${missing_output}" != *ANVIL_AGENT_RUN_REPO_FETCH_FAILED* ]]; then
	echo "missing ref with unreachable remote did not report fetch failure" >&2
	exit 1
fi
if [[ "$(git branch --show-current)" != "agentrun-work" || "$(cat missing-ref)" != "tracked collision" ]]; then
	echo "tracked path collision changed the current revision or file" >&2
	exit 1
fi

set +e
directory_output="$(anvil_checkout_repository_ref missing-dir 2>&1)"
directory_status=$?
set -e
if [[ "${directory_status}" -ne 21 || "${directory_output}" != *ANVIL_AGENT_RUN_REPO_FETCH_FAILED* ]]; then
	echo "tracked directory collision was mistaken for a ref" >&2
	exit 1
fi

remote="${test_dir}/remote.git"
publisher="${test_dir}/publisher"
consumer="${test_dir}/consumer"
git init --quiet --bare "${remote}"
git -C "${remote}" symbolic-ref HEAD refs/heads/main
git init --quiet --initial-branch=main "${publisher}"
git -C "${publisher}" config user.name "Runner Contract"
git -C "${publisher}" config user.email "runner-contract@example.invalid"
printf 'one\n' > "${publisher}/version"
git -C "${publisher}" add version
git -C "${publisher}" commit --quiet -m one
git -C "${publisher}" branch remote-ready
git -C "${publisher}" remote add origin "${remote}"
git -C "${publisher}" push --quiet origin main remote-ready
git clone --quiet "${remote}" "${consumer}"
printf 'two\n' > "${publisher}/version"
git -C "${publisher}" add version
git -C "${publisher}" commit --quiet -m two
git -C "${publisher}" branch --force remote-ready
git -C "${publisher}" push --quiet --force origin remote-ready

cd "${consumer}"
anvil_checkout_repository_ref remote-ready
if [[ "$(cat version)" != "two" ]]; then
	echo "runner checkout used a stale cached remote ref" >&2
	exit 1
fi

set +e
option_output="$(anvil_checkout_repository_ref --help 2>&1)"
option_status=$?
set -e
if [[ "${option_status}" -ne 22 || "${option_output}" != *ANVIL_AGENT_RUN_REPO_CHECKOUT_FAILED* ]]; then
	echo "option-like repository ref was not rejected" >&2
	exit 1
fi

credential_url="https://runner:topsecret@example.invalid/repo.git"
credential_consumer="${test_dir}/credential-consumer"
credential_clone_invocation="${test_dir}/credential-clone-invocation"
real_git="$(command -v git)"
git() {
	if [[ "${1:-}" == clone && "${2:-}" == "https://example.invalid/repo.git" ]]; then
		printf '%s\n' "$@" > "${credential_clone_invocation}"
		if [[ "$*" == *topsecret* ]]; then
			echo "credential-bearing URL appeared in git clone argv" >&2
			return 1
		fi
		config_index="$((GIT_CONFIG_COUNT - 1))"
		key_name="GIT_CONFIG_KEY_${config_index}"
		value_name="GIT_CONFIG_VALUE_${config_index}"
		if [[ "${!key_name}" != "url.${credential_url}.insteadOf" || "${!value_name}" != "https://example.invalid/repo.git" ]]; then
			echo "credential transport rewrite was not isolated in Git process environment" >&2
			return 1
		fi
		"${real_git}" clone --quiet "${remote}" "${3}"
		"${real_git}" -C "${3}" remote set-url origin "${2}"
		return
	fi
	"${real_git}" "$@"
}
anvil_clone_repository_url "${credential_url}" "${credential_consumer}"
if [[ "$(sed -n '1p' "${credential_clone_invocation}")" != "clone" || "$(sed -n '2p' "${credential_clone_invocation}")" != "https://example.invalid/repo.git" ]]; then
	echo "runner clone did not use the sanitized repository URL as its persisted source" >&2
	exit 1
fi
origin_url="$(git -C "${credential_consumer}" remote get-url origin)"
if [[ "${origin_url}" != "https://example.invalid/repo.git" ]]; then
	echo "runner clone did not sanitize credential-bearing origin URL" >&2
	exit 1
fi
if rg -q 'topsecret|runner@|runner:topsecret' "${credential_consumer}/.git/config"; then
	echo "runner clone persisted URL credentials in .git/config" >&2
	exit 1
fi
credential_consumer_with_config="${test_dir}/credential-consumer-with-config"
GIT_CONFIG_COUNT=1 \
	GIT_CONFIG_KEY_0="credential.helper" \
	GIT_CONFIG_VALUE_0="" \
	anvil_clone_repository_url "${credential_url}" "${credential_consumer_with_config}"
if [[ "$(git -C "${credential_consumer_with_config}" remote get-url origin)" != "https://example.invalid/repo.git" ]]; then
	echo "runner clone did not preserve existing process-local Git configuration" >&2
	exit 1
fi
sanitized_url="$(anvil_sanitize_repository_url 'https://runner:secret@example.invalid/repo.git?access_token=query-secret#fragment')"
if [[ "${sanitized_url}" != "https://example.invalid/repo.git" ]]; then
	echo "runner URL sanitizer did not strip userinfo, query, and fragment" >&2
	exit 1
fi
pathless_url="$(anvil_sanitize_repository_url 'https://runner:secret@example.invalid?access_token=query-secret#fragment')"
if [[ "${pathless_url}" != "https://example.invalid" ]]; then
	echo "runner URL sanitizer did not strip credentials from a pathless URL" >&2
	exit 1
fi
if anvil_sanitize_repository_url 'ftp://runner:topsecret@example.invalid/repo.git?access_token=query-secret#fragment' >/dev/null; then
	echo "runner URL sanitizer accepted credentials in an unsupported URI scheme" >&2
	exit 1
fi
if anvil_sanitize_repository_url 'ssh://runner:topsecret@example.invalid/repo.git' >/dev/null; then
	echo "runner URL sanitizer accepted an SSH password in URI userinfo" >&2
	exit 1
fi
if [[ "$(anvil_sanitize_repository_url 'ssh://git@example.invalid/repo.git')" != "ssh://git@example.invalid/repo.git" ]]; then
	echo "runner URL sanitizer rejected a normal SSH username" >&2
	exit 1
fi
if anvil_sanitize_repository_url 'runner:topsecret@example.invalid:repo.git' >/dev/null; then
	echo "runner URL sanitizer accepted password-like SCP userinfo" >&2
	exit 1
fi

echo "Runner repository checkout contract passed"
