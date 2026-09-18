#!/usr/bin/env bash
# substrate-latency-compare.sh — Job cold-start baseline vs warm Substrate
# resume for a direct turn AND a peer delivery (standing-chat spike).
#
# Fake-backend by default: the fake run needs no cluster and is safe in
# CI. The live run dials a Kind cluster's Substrate ATE over gRPC (see
# docs/substrate-spike.md for the port-forward setup) plus Austin's observed
# Job baseline flags. ateapi always serves TLS: prefer verified TLS (unset
# INSECURE); ANVIL_AGENTS_SUBSTRATE_INSECURE=true is skip-verify TLS for a
# loopback port-forward only (never plaintext).
#
# Usage:
#   hack/substrate-latency-compare.sh [--iterations 20] [--out /tmp/substrate-latency.json]
#   hack/substrate-latency-compare.sh --live --iterations 20 \
#     --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000
#   hack/substrate-latency-compare.sh --live -n 10 \
#     --namespace ate-demo-counter --actor-class counter --pool '' \
#     --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000 \
#     --out /tmp/substrate-latency-counter.json
#
# Live env (see docs/substrate-spike.md "Kind port-forward + live env"):
#   export ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED=true
#   export ANVIL_AGENTS_SUBSTRATE_ENDPOINT=127.0.0.1:<port-forward-port>
#   export ANVIL_AGENTS_SUBSTRATE_TEMPLATE=standing-chat
#   export ANVIL_AGENTS_SUBSTRATE_INSECURE=true   # skip-verify TLS, Kind loopback only
#   # or export ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE=/run/ate/token for verified TLS
#   # Counter demo without editing Go defaults:
#   # export ANVIL_AGENTS_SUBSTRATE_ATESPACE=ate-demo-counter
#   # export ANVIL_AGENTS_SUBSTRATE_TEMPLATE=counter
#   # hack/substrate-latency-compare.sh --live -n 10 \
#   #   --namespace ate-demo-counter --actor-class counter --pool '' ...
set -euo pipefail

ITERATIONS=20
OUT=""
LIVE=0
NAMESPACE="agents"
ACTOR_CLASS="standing-chat"
POOL="warm"
JOB_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--iterations) ITERATIONS="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --live) LIVE=1; shift ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --actor-class) ACTOR_CLASS="$2"; shift 2 ;;
    --pool) POOL="$2"; shift 2 ;;
    --job-baseline-p50-ms|--job-baseline-p95-ms) JOB_ARGS+=("$1" "$2"); shift 2 ;;
    -h|--help)
      sed -n '2,27p' "$0"
      exit 0
      ;;
    *)
      echo "error: unknown argument $1" >&2
      exit 2
      ;;
  esac
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/substrate-latency"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

go build -trimpath -o "$BIN" ./cmd/substrate-latency

if [[ "$LIVE" -eq 1 ]]; then
  if [[ "${ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED:-}" != "true" && "${ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED:-}" != "1" ]]; then
    echo "error: --live requires ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED=true" >&2
    exit 2
  fi
  if [[ -z "${ANVIL_AGENTS_SUBSTRATE_ENDPOINT:-}" ]]; then
    echo "error: --live requires ANVIL_AGENTS_SUBSTRATE_ENDPOINT (ateapi address)" >&2
    exit 2
  fi
  if [[ -z "${ANVIL_AGENTS_SUBSTRATE_TEMPLATE:-}" ]]; then
    echo "error: --live requires ANVIL_AGENTS_SUBSTRATE_TEMPLATE (default ActorTemplate, e.g. standing-chat)" >&2
    exit 2
  fi
fi

BIN_ARGS=(-n "$ITERATIONS" -namespace "$NAMESPACE" -actor-class "$ACTOR_CLASS" -pool "$POOL" "${JOB_ARGS[@]}")

if [[ -n "$OUT" ]]; then
  "$BIN" "${BIN_ARGS[@]}" -out "$OUT"
else
  "$BIN" "${BIN_ARGS[@]}"
fi
