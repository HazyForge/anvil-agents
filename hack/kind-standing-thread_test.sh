#!/usr/bin/env bash
# kind-standing-thread_test.sh — unit tests for hack/kind-standing-thread.sh.
#
# Fake curl on PATH: no cluster, no OIDC, no network. Run:
#   bash hack/kind-standing-thread_test.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="${repo_root}/hack/kind-standing-thread.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

mkdir -p "${tmp_dir}/bin"
export FAKE_CURL_LOG="${tmp_dir}/curl.log"
export FAKE_DATA_LOG="${tmp_dir}/data.log"
export FAKE_HTTP_CODE="201"
export FAKE_HTTP_BODY='{"id":"thread-abc"}'
cat > "${tmp_dir}/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
prev=""
for arg in "$@"; do
	if [[ "${prev}" == "-o" ]]; then
		out="${arg}"
	fi
	if [[ "${prev}" == "-d" || "${prev}" == "--data" ]]; then
		if [[ "${arg}" == @* ]]; then
			cat "${arg:1}" >> "${FAKE_DATA_LOG}"
		fi
	fi
	prev="${arg}"
done
printf '%s' "${FAKE_HTTP_BODY}" > "${out}"
printf '%s' "${FAKE_HTTP_CODE}"
printf '%s\n' "$*" >> "${FAKE_CURL_LOG}"
EOF
chmod 0755 "${tmp_dir}/bin/curl"
export PATH="${tmp_dir}/bin:${PATH}"

fail() {
	echo "kind-standing-thread test failed: $*" >&2
	exit 1
}

reset_fakes() {
	: >"${FAKE_CURL_LOG}"
	: >"${FAKE_DATA_LOG}"
	export FAKE_HTTP_CODE="201"
	export FAKE_HTTP_BODY='{"id":"thread-abc"}'
}

# 1. No bearer is an honest usage error, never a network call.
reset_fakes
if env -u ANVIL_AGENTS_ACCESS_TOKEN "${script}" --api-origin http://127.0.0.1:9 >"${tmp_dir}/no-bearer.out" 2>&1; then
	fail "accepted a missing bearer"
fi
rg -q -- 'no bearer' "${tmp_dir}/no-bearer.out" || fail "did not explain the missing bearer"
[[ -s "${FAKE_CURL_LOG}" ]] && fail "called the network without a bearer"

# 2. Blank profile is rejected: standing threads require profileName.
reset_fakes
if ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" --profile '   ' >"${tmp_dir}/blank.out" 2>&1; then
	fail "accepted a blank profile"
fi
rg -q -- 'profileName' "${tmp_dir}/blank.out" || fail "did not explain the profileName requirement"

# 3. 201 created: request shape is a standing ensure, token stays in the header.
reset_fakes
ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" \
	--api-origin http://127.0.0.1:18080 --namespace agents \
	--profile kind-local-manager >"${tmp_dir}/created.out" 2>&1 || fail "201 run failed"
rg -q -- 'standing thread created: thread-abc' "${tmp_dir}/created.out" || fail "did not report the created thread"
tail -n 1 "${tmp_dir}/created.out" | rg -q -- '^thread-abc$' || fail "last line is not the bare thread id"
rg -q -- 'test-token-xyz' "${tmp_dir}/created.out" && fail "leaked the bearer to stdout"
rg -q -- 'Authorization: Bearer test-token-xyz' "${FAKE_CURL_LOG}" || fail "bearer did not travel in the Authorization header"
rg -q -- '"standing": true' "${FAKE_DATA_LOG}" || fail "request is not a standing ensure"
rg -q -- '"profileName": "kind-local-manager"' "${FAKE_DATA_LOG}" || fail "request lost the manager profile"
rg -q -- 'harnessProfileName' "${FAKE_DATA_LOG}" && fail "request smuggled a harness override (standing threads forbid it)"

# 4. 200 existing converges instead of minting a second thread.
reset_fakes
export FAKE_HTTP_CODE="200"
ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" >"${tmp_dir}/existing.out" 2>&1 || fail "200 run failed"
rg -q -- 'standing thread existing: thread-abc' "${tmp_dir}/existing.out" || fail "did not report the existing thread"

# 5. 401 keeps the stub-denial copy (mint a Kind-local token, do not retry).
reset_fakes
export FAKE_HTTP_CODE="401"
export FAKE_HTTP_BODY='{"error":{"code":"unauthorized"}}'
if ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" >"${tmp_dir}/denied.out" 2>&1; then
	fail "accepted a 401 denial"
fi
rg -q -- '401' "${tmp_dir}/denied.out" || fail "did not surface the 401"

# 6. 400 invalid target points at the standing-manager YAML, not at a retry.
reset_fakes
export FAKE_HTTP_CODE="400"
export FAKE_HTTP_BODY='{"error":{"code":"invalid_target","message":"profile is unavailable in this namespace"}}'
if ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" >"${tmp_dir}/bad.out" 2>&1; then
	fail "accepted a 400 invalid target"
fi
rg -q -- 'kind-local-standing-manager.yaml' "${tmp_dir}/bad.out" || fail "did not point at the standing-manager YAML"

# 7. --token-file supplies the bearer without env.
reset_fakes
printf 'file-token-abc\n' >"${tmp_dir}/token"
chmod 0600 "${tmp_dir}/token"
env -u ANVIL_AGENTS_ACCESS_TOKEN "${script}" --token-file "${tmp_dir}/token" >"${tmp_dir}/file.out" 2>&1 || fail "--token-file run failed"
rg -q -- 'thread-abc' "${tmp_dir}/file.out" || fail "--token-file run lost the thread id"
rg -q -- 'file-token-abc' "${tmp_dir}/file.out" && fail "leaked the file token to stdout"
rg -q -- 'Authorization: Bearer file-token-abc' "${FAKE_CURL_LOG}" || fail "file token did not travel in the Authorization header"

# 8. Unknown arguments fail fast.
reset_fakes
if ANVIL_AGENTS_ACCESS_TOKEN=test-token-xyz "${script}" --bogus >"${tmp_dir}/bogus.out" 2>&1; then
	fail "accepted an unknown argument"
fi

echo "kind-standing-thread tests passed"
