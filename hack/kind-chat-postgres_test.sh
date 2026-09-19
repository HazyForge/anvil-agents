#!/usr/bin/env bash
# Smoke test for hack/kind-chat-postgres.sh.
#
# Docker-free: exercises --help and the offline --check-url shape gate only.
# Live container bring-up stays a manual Kind-local step (see
# docs/standing-inprocess-harness.md) because CI has no guaranteed Docker
# daemon here; hack/test-archive-postgres.sh already covers real PostgreSQL
# migration/upsert against Docker when a daemon exists.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
helper="${root_dir}/hack/kind-chat-postgres.sh"

bash -n "${helper}"

"${helper}" --help >/dev/null

GOOD="postgresql://anvil_agents:anvil-kind-chat-dev-only@127.0.0.1:54329/anvil_agents?sslmode=disable"
out="$("${helper}" --check-url "${GOOD}")"
[[ "${out}" == *"shape ok"* ]] || { printf 'expected shape ok for %s\n' "${GOOD}" >&2; exit 1; }

# Localhost and IPv6 loopback spellings are accepted too.
"${helper}" --check-url "postgresql://anvil_agents:pw@localhost:5432/anvil_agents?sslmode=disable" >/dev/null
"${helper}" --check-url "postgresql://anvil_agents:pw@[::1]:5432/anvil_agents?sslmode=disable" >/dev/null

# --db override composes with --check-url (custom database name passes).
"${helper}" --check-url "postgresql://custom:secret@127.0.0.1:5432/customdb?sslmode=disable" >/dev/null

rejects=(
  ""
  "postgres://anvil_agents:pw@127.0.0.1:5432/anvil_agents?sslmode=disable"
  "http://anvil_agents:pw@127.0.0.1:5432/anvil_agents?sslmode=disable"
  "postgresql://anvil_agents:pw@postgres.default.svc:5432/anvil_agents?sslmode=disable"
  "postgresql://anvil_agents:pw@10.0.0.5:5432/anvil_agents?sslmode=disable"
  "postgresql://anvil_agents:pw@127.0.0.1:5432/anvil_agents"
  "postgresql://anvil_agents:pw@127.0.0.1:5432/anvil_agents?sslmode=require"
  "postgresql://127.0.0.1:5432/anvil_agents?sslmode=disable"
  "postgresql://anvil_agents:pw@127.0.0.1:?sslmode=disable"
  "postgresql://anvil_agents:pw@127.0.0.1:5432?sslmode=disable"
  "postgresql://anvil_agents:pw@127.0.0.1:5432/?sslmode=disable"
  "postgresql://anvil_agents:pw@127.0.0.1/anvil_agents?sslmode=disable"
  "not-a-url"
)
for bad in "${rejects[@]}"; do
  if "${helper}" --check-url "${bad}" >/dev/null 2>&1; then
    printf 'expected --check-url to reject %q\n' "${bad}" >&2
    exit 1
  fi
done

# Unknown flags fail fast with usage hint exit code 2.
if "${helper}" --nope >/dev/null 2>&1; then
  printf 'expected unknown flag to fail\n' >&2
  exit 1
fi

# Without Docker, lifecycle commands fail honestly instead of hanging.
if ! command -v docker >/dev/null 2>&1; then
  if "${helper}" --stop >/dev/null 2>&1; then
    printf 'expected --stop without docker to fail\n' >&2
    exit 1
  fi
fi

printf 'kind-chat-postgres smoke test passed\n'
