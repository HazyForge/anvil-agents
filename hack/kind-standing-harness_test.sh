#!/usr/bin/env bash
# Smoke test for hack/kind-standing-harness.sh.
#
# Docker-free and network-free: exercises --help, the Fake-only rejection,
# the PATH/auth-file gates with stub binaries and temp HOME dirs, the
# env/file bearer loading (kind-oidc-issuer compatible), and the honest
# failure contract. Live CLI auth stays a manual Kind-local step (see
# docs/standing-inprocess-harness.md) because CI has no provider
# credentials; the wired turn path itself is pinned in Go
# (internal/runapi/kind_local_harness_test.go,
# internal/standing/process_local_auth_test.go).
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${root_dir}/hack/kind-standing-harness.sh"

bash -n "${helper}"

"${helper}" --help >/dev/null

# Isolated HOME + PATH so the host's real CLIs and auth files cannot leak in.
stub_home="$(mktemp -d)"
stub_path="$(mktemp -d)"
trap 'rm -rf "${stub_home}" "${stub_path}"' EXIT
export HOME="${stub_home}"
export CODEX_HOME="${stub_home}/.codex"
unset CODEX_AUTH_JSON || true

run_helper() {
  # Run with a stub-only PATH plus system dirs for interpreters (python3 for
  # the JSON sniff lives outside the stub dir).
  PATH="${stub_path}:/usr/bin:/bin" "$@"
}

# Fake-only kinds always report blocked (InProcessNotWired by design).
for fake in hermesAgent piAgent custom ""; do
  if run_helper "${helper}" --harness "${fake}" --check >/dev/null 2>&1; then
    printf 'expected Fake-only harness %q to report blocked\n' "${fake}" >&2
    exit 1
  fi
done

# Missing binary reports blocked with InProcessNotWired guidance.
out="$(run_helper "${helper}" --harness codex --check 2>&1 || true)"
[[ "${out}" == *"blocked"* ]] || { printf 'expected blocked without codex on PATH\n' >&2; exit 1; }
[[ "${out}" == *"InProcessNotWired"* ]] || { printf 'expected InProcessNotWired guidance\n' >&2; exit 1; }

# Stub codex binary, still no auth file: blocked on provider auth.
printf '#!/usr/bin/env bash\nexit 0\n' > "${stub_path}/codex"
chmod +x "${stub_path}/codex"
out="$(run_helper "${helper}" --harness codex --check 2>&1 || true)"
[[ "${out}" == *"blocked"* ]] || { printf 'expected blocked without auth file\n' >&2; exit 1; }
[[ "${out}" == *"auth.json"* ]] || { printf 'expected auth.json remediation\n' >&2; exit 1; }

# Seed the CLI's own auth home: binary + auth present, bearer absent by
# default is a note (not a failure) unless --require-bearer.
mkdir -p "${CODEX_HOME}"
printf '{"auth":"stub"}' > "${CODEX_HOME}/auth.json"
chmod 600 "${CODEX_HOME}/auth.json"
out="$(run_helper env -u ANVIL_AGENTS_ACCESS_TOKEN "${helper}" --harness codex --check 2>&1)"
[[ "${out}" == *"ready (harness=codex)"* ]] || { printf 'expected ready, got: %s\n' "${out}" >&2; exit 1; }
# Bearer bytes must never appear in output.
[[ "${out}" != *"stub-bearer"* ]] || { printf 'unexpected bearer leak\n' >&2; exit 1; }

# --require-bearer fails honestly with no bearer configured.
if run_helper env -u ANVIL_AGENTS_ACCESS_TOKEN "${helper}" --harness codex --check --require-bearer >/dev/null 2>&1; then
  printf 'expected --require-bearer without bearer to fail\n' >&2
  exit 1
fi

# Env bearer satisfies --require-bearer (value is presence-checked only).
out="$(run_helper env ANVIL_AGENTS_ACCESS_TOKEN="header.payload.signature" "${helper}" --harness codex --check --require-bearer 2>&1)"
[[ "${out}" == *"ready (harness=codex)"* ]] || { printf 'expected ready with env bearer, got: %s\n' "${out}" >&2; exit 1; }
[[ "${out}" != *"header.payload.signature"* ]] || { printf 'bearer bytes leaked to output\n' >&2; exit 1; }

# --token-file bearer (kind-oidc-issuer mint output saved to a file).
token_file="$(mktemp)"
printf 'header.payload.signature\n' > "${token_file}"
chmod 600 "${token_file}"
out="$(run_helper env -u ANVIL_AGENTS_ACCESS_TOKEN "${helper}" --harness codex --check --require-bearer --token-file "${token_file}" 2>&1)"
[[ "${out}" == *"ready (harness=codex)"* ]] || { printf 'expected ready with token file, got: %s\n' "${out}" >&2; exit 1; }
[[ "${out}" != *"header.payload.signature"* ]] || { printf 'token-file bytes leaked to output\n' >&2; exit 1; }
rm -f "${token_file}"

# Empty token file fails under --require-bearer.
empty_file="$(mktemp)"
: > "${empty_file}"
if run_helper env -u ANVIL_AGENTS_ACCESS_TOKEN "${helper}" --harness codex --check --require-bearer --token-file "${empty_file}" >/dev/null 2>&1; then
  printf 'expected empty token file to fail\n' >&2
  exit 1
fi
rm -f "${empty_file}"

# --print-auth-path resolves the per-kind auth home (respects CODEX_HOME).
resolved="$(run_helper "${helper}" --harness codex --print-auth-path)"
[[ "${resolved}" == "${CODEX_HOME}/auth.json" ]] || { printf 'auth path = %q\n' "${resolved}" >&2; exit 1; }
if run_helper "${helper}" --harness hermesAgent --print-auth-path >/dev/null 2>&1; then
  printf 'expected --print-auth-path for Fake-only kind to fail\n' >&2
  exit 1
fi

# CLI-managed kinds gate on binary presence with a manual-turn advisory.
printf '#!/usr/bin/env bash\nexit 0\n' > "${stub_path}/agy"
chmod +x "${stub_path}/agy"
out="$(run_helper env -u ANVIL_AGENTS_ACCESS_TOKEN "${helper}" --harness agy --check 2>&1)"
[[ "${out}" == *"ready (harness=agy)"* ]] || { printf 'expected agy ready-advisory, got: %s\n' "${out}" >&2; exit 1; }
[[ "${out}" == *"manual turn"* ]] || { printf 'expected manual-turn advisory for agy\n' >&2; exit 1; }

# Unknown flags fail fast with usage hint exit code 2.
if run_helper "${helper}" --nope >/dev/null 2>&1; then
  printf 'expected unknown flag to fail\n' >&2
  exit 1
fi

printf 'kind-standing-harness smoke test passed\n'
