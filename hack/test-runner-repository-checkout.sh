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

echo "Runner repository checkout contract passed"
