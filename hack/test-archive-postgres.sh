#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container="anvil-agents-archive-postgres-test-$$"
password="archive-test-password"

cleanup() {
  docker rm -f "${container}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "${container}" \
  --user 70:70 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /var/lib/postgresql/data:rw,uid=70,gid=70,mode=0700 \
  --publish 127.0.0.1::5432 \
  --env POSTGRES_DB=anvil_agents \
  --env POSTGRES_USER=anvil_agents \
  --env POSTGRES_PASSWORD="${password}" \
  postgres:17-alpine \
  -p 5432 >/dev/null

for _ in {1..60}; do
  if docker exec "${container}" pg_isready -U anvil_agents -d anvil_agents -p 5432 >/dev/null 2>&1; then
    host_port="$(docker port "${container}" 5432/tcp | awk -F: 'NR == 1 { print $NF }')"
    ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL="postgresql://anvil_agents:${password}@127.0.0.1:${host_port}/anvil_agents?sslmode=disable" \
      go test ./internal/archive -run '^TestPostgresAgentRunArchiveStoreIntegration$' -count=1
    printf 'PostgreSQL archive migration/upsert integration passed\n'
    exit 0
  fi
  sleep 1
done

docker logs "${container}" >&2
printf 'PostgreSQL archive did not become ready\n' >&2
exit 1
