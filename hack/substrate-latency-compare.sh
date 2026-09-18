#!/usr/bin/env bash
# substrate-latency-compare.sh — Job cold-start baseline vs warm Substrate
# resume for a direct turn AND a peer delivery (standing-chat spike, slice 2).
#
# Fake-backend by default: the fake run needs no cluster and is safe in
# CI. The live run needs a Kind cluster with Substrate ATE (see
# docs/substrate-spike.md) plus Austin's observed Job baseline flags, and is
# pending the generated-stub gRPC dialer (the harness fails fast with --live
# until the dialer lands).
#
# Usage:
#   hack/substrate-latency-compare.sh [--iterations 20] [--out /tmp/substrate-latency.json]
#   hack/substrate-latency-compare.sh --live --iterations 20 \
#     --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000
set -euo pipefail

ITERATIONS=20
OUT=""
LIVE=0
JOB_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --live) LIVE=1; shift ;;
    --job-baseline-p50-ms|--job-baseline-p95-ms) JOB_ARGS+=("$1" "$2"); shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0"
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
fi

if [[ -n "$OUT" ]]; then
  "$BIN" -n "$ITERATIONS" -out "$OUT" "${JOB_ARGS[@]}"
else
  "$BIN" -n "$ITERATIONS" "${JOB_ARGS[@]}"
fi
