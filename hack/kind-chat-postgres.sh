#!/usr/bin/env bash
# Disposable loopback-only PostgreSQL for Kind-local standing-chat bring-up.
#
# Blocker (1) on the path to a real signed-in Desktop standing-chat latency
# sample: the API requires ANVIL_AGENTS_CHAT_DATABASE_URL when chat.enabled
# is true (see docs/standing-chat.md), and the Kind-local host-run API has no
# database to point at. This helper starts a throwaway postgres:17-alpine
# container bound to 127.0.0.1, waits for readiness, and prints the export
# line the host-run API needs. Schema migration is applied by the API itself
# on startup (chat.OpenPostgresStore -> Migrate), so no manual SQL is needed.
#
# Scope: host-run Kind-local API only (loopback bind, e.g. 127.0.0.1:18080).
# The container publishes to 127.0.0.1, so an in-cluster API cannot reach it;
# in-cluster installs should use the chart's archive standalone /
# cloudnativepg modes instead (see docs/archive.md). Disposable only: the
# default credential is a committed dev-only placeholder, data lives on a
# tmpfs, and --stop destroys everything.
#
# Usage:
#   hack/kind-chat-postgres.sh [--up] [--url] [--stop] [--check-url URL] [options]
#
# Options:
#   --name NAME         container name (default: anvil-agents-kind-chat-postgres)
#   --db DB             database/role name (default: anvil_agents)
#   --user USER         role name (default: anvil_agents)
#   --password PASS     role password (default: $ANVIL_KIND_CHAT_POSTGRES_PASSWORD
#                       or dev-only placeholder; override for any shared host)
#   --port PORT         fixed host port (default: docker-assigned, discovered
#                       via `docker port`, avoids clashing with local Postgres)
#   --image IMG         postgres image (default: postgres:17-alpine)
#   --check-url URL     validate a chat database URL shape offline (no Docker,
#                       no network) and exit; enforces the exact shape this
#                       helper emits
#   --url               print the export line for the running container and exit
#   --stop              remove the container and exit
#   -h, --help          print this help and exit
set -euo pipefail

CONTAINER="anvil-agents-kind-chat-postgres"
DB="anvil_agents"
DB_USER="anvil_agents"
PASSWORD="${ANVIL_KIND_CHAT_POSTGRES_PASSWORD:-anvil-kind-chat-dev-only}"
HOST_PORT=""
IMAGE="postgres:17-alpine"
ACTION="up"
CHECK_URL=""

usage() {
  sed -n '2,/^set -euo pipefail$/p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'
}

# check_chat_url <url>: enforce the exact disposable-Postgres URL shape this
# helper emits. Offline: string checks only, no Docker, no network. The API
# parses the same string with pgxpool.ParseConfig (see
# internal/chat/database_url_test.go for the parse-level contract), so a URL
# that passes here is loadable by the API process.
check_chat_url() {
  local url="${1:-}"
  local reason=""
  if [[ -z "${url}" ]]; then
    reason="database URL is empty"
  elif [[ "${url}" != postgresql://* ]]; then
    reason="scheme must be postgresql:// (got ${url%%:*})"
  elif [[ "${url}" != *@"127.0.0.1:"* && "${url}" != *"@localhost:"* && "${url}" != *"@[::1]:"* ]]; then
    reason="host must be loopback (127.0.0.1, localhost, or ::1) with an explicit port"
  elif [[ "${url}" != *"sslmode=disable"* ]]; then
    reason="query must contain sslmode=disable for the disposable loopback container"
  fi
  # Generic structural checks that apply regardless of the branch above.
  if [[ -z "${reason}" ]]; then
    local without_scheme="${url#postgresql://}"
    local userinfo="${without_scheme%%@*}"
    if [[ "${without_scheme}" != *"@"* || -z "${userinfo}" || "${userinfo}" == *"/"* ]]; then
      reason="URL must carry userinfo (user[:password]@) before the host"
    else
      local hostpath="${without_scheme#*@}"
      local dbname="${hostpath#*/}"
      dbname="${dbname%%\?*}"
      if [[ "${hostpath}" != *"/"* || -z "${dbname}" ]]; then
        reason="URL must name a database path"
      fi
    fi
  fi
  if [[ -n "${reason}" ]]; then
    printf 'kind-chat-postgres: invalid chat database URL: %s\n' "${reason}" >&2
    return 1
  fi
  printf 'kind-chat-postgres: chat database URL shape ok\n'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name) CONTAINER="$2"; shift 2 ;;
    --db) DB="$2"; shift 2 ;;
    --user) DB_USER="$2"; shift 2 ;;
    --password) PASSWORD="$2"; shift 2 ;;
    --port) HOST_PORT="$2"; shift 2 ;;
    --image) IMAGE="$2"; shift 2 ;;
    --check-url) ACTION="check"; CHECK_URL="$2"; shift 2 ;;
    --url) ACTION="url"; shift ;;
    --stop) ACTION="stop"; shift ;;
    --up) ACTION="up"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'kind-chat-postgres: unknown argument %q (see --help)\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ "${ACTION}" == "check" ]]; then
  check_chat_url "${CHECK_URL}"
  exit $?
fi

if ! command -v docker >/dev/null 2>&1; then
  printf 'kind-chat-postgres: docker not found; cannot manage the disposable Postgres container\n' >&2
  printf 'kind-chat-postgres: install Docker (WSL: Docker Desktop or dockerd) and retry\n' >&2
  exit 1
fi

if [[ "${ACTION}" == "stop" ]]; then
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
  printf 'kind-chat-postgres: removed container %s (if it existed)\n' "${CONTAINER}"
  exit 0
fi

container_url() {
  local port="$1"
  printf 'postgresql://%s:%s@127.0.0.1:%s/%s?sslmode=disable' "${DB_USER}" "${PASSWORD}" "${port}" "${DB}"
}

if [[ "${ACTION}" == "url" ]]; then
  if ! docker inspect "${CONTAINER}" >/dev/null 2>&1; then
    printf 'kind-chat-postgres: container %s does not exist; run without flags to start it\n' "${CONTAINER}" >&2
    exit 1
  fi
  host_port="$(docker port "${CONTAINER}" 5432/tcp 2>/dev/null | awk -F: 'NR == 1 { print $NF }')"
  if [[ -z "${host_port}" ]]; then
    printf 'kind-chat-postgres: container %s is not running or has no published 5432/tcp\n' "${CONTAINER}" >&2
    exit 1
  fi
  printf "export ANVIL_AGENTS_CHAT_DATABASE_URL='%s'\n" "$(container_url "${host_port}")"
  exit 0
fi

# ACTION == up from here.
publish_arg=(--publish "127.0.0.1::5432")
if [[ -n "${HOST_PORT}" ]]; then
  publish_arg=(--publish "127.0.0.1:${HOST_PORT}:5432")
fi

if docker inspect "${CONTAINER}" >/dev/null 2>&1; then
  if [[ "$(docker inspect -f '{{.State.Running}}' "${CONTAINER}")" != "true" ]]; then
    printf 'kind-chat-postgres: starting existing container %s\n' "${CONTAINER}" >&2
    docker start "${CONTAINER}" >/dev/null
  else
    printf 'kind-chat-postgres: reusing running container %s\n' "${CONTAINER}" >&2
  fi
else
  printf 'kind-chat-postgres: creating disposable container %s (%s, tmpfs data)\n' "${CONTAINER}" "${IMAGE}" >&2
  docker run --rm -d \
    --name "${CONTAINER}" \
    --user 70:70 \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --tmpfs /var/lib/postgresql/data:rw,uid=70,gid=70,mode=0700 \
    "${publish_arg[@]}" \
    --env "POSTGRES_DB=${DB}" \
    --env "POSTGRES_USER=${DB_USER}" \
    --env "POSTGRES_PASSWORD=${PASSWORD}" \
    "${IMAGE}" \
    -p 5432 >/dev/null
fi

for _ in {1..60}; do
  if docker exec "${CONTAINER}" pg_isready -U "${DB_USER}" -d "${DB}" -p 5432 >/dev/null 2>&1; then
    host_port="$(docker port "${CONTAINER}" 5432/tcp | awk -F: 'NR == 1 { print $NF }')"
    url="$(container_url "${host_port}")"
    if ! check_chat_url "${url}" >/dev/null; then
      printf 'kind-chat-postgres: internal error: generated URL failed shape check\n' >&2
      exit 1
    fi
    printf "export ANVIL_AGENTS_CHAT_DATABASE_URL='%s'\n" "${url}"
    printf 'kind-chat-postgres: disposable Postgres ready on 127.0.0.1:%s; eval the export above, then start the API with chat.enabled=true\n' "${host_port}" >&2
    printf 'kind-chat-postgres: disposable only (--stop destroys data); never use this URL outside loopback Kind-local bring-up\n' >&2
    exit 0
  fi
  sleep 1
done

docker logs "${CONTAINER}" >&2 || true
printf 'kind-chat-postgres: PostgreSQL did not become ready within 60s\n' >&2
exit 1
