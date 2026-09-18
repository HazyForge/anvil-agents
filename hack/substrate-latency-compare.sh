#!/usr/bin/env bash
# substrate-latency-compare.sh — Job cold-start baseline vs warm Substrate
# resume for a direct turn AND a peer delivery (standing-chat spike).
#
# API-first by default: the fake-backend run needs no cluster and is safe in
# CI. The live run needs a Kind cluster with ate installed (see
# docs/substrate-spike.md): atenet-router for the resume/probe data plane plus
# the cmd/substrate-ate-shim control bridge for create/suspend, plus Austin's
# observed Job baseline flags.
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
    echo "error: --live requires ANVIL_AGENTS_SUBSTRATE_ENDPOINT (atenet-router origin, e.g. http://localhost:8000 via port-forward)" >&2
    exit 2
  fi
  if [[ -z "${ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT:-}" ]]; then
    echo "error: --live requires ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT (cmd/substrate-ate-shim origin for create/suspend)" >&2
    echo "hint: go run ./cmd/substrate-ate-shim -listen 127.0.0.1:8081, then export ANVIL_AGENTS_SUBSTRATE_SHIM_ENDPOINT=http://127.0.0.1:8081 (see docs/substrate-spike.md)" >&2
    exit 2
  fi
fi

if [[ -n "$OUT" ]]; then
  "$BIN" -n "$ITERATIONS" -out "$OUT" "${JOB_ARGS[@]}"
else
  "$BIN" -n "$ITERATIONS" "${JOB_ARGS[@]}"
fi
