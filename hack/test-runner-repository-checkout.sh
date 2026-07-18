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
git -C "${worktree}" add tracked
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

echo "Runner repository checkout contract passed"
