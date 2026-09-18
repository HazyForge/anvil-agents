#!/usr/bin/env bash
# standing-latency-compare.sh — standing InProcess warm path vs the Job
# cold-start baseline for a direct turn AND a peer delivery.
#
# Fake-backend by default: deterministic, no cluster, no subprocess, no model
# call — safe in CI. The report lands under .runtime/ (gitignored local
# artifact) and is also printed to stdout.
#
# Usage:
#   hack/standing-latency-compare.sh [--iterations 20] [--out .runtime/standing-latency-fake.json]
#   hack/standing-latency-compare.sh --process-stub --iterations 20
#   hack/standing-latency-compare.sh --live -harness codex -n 10 \
#     --out .runtime/standing-latency-live.json
#
# --live runs one real harness subprocess per measured turn through
# standing.ProcessBackend (PATH-resolved CLI, harness's own auth home).
# Local-only: needs the CLI for -harness on PATH. Never reads Secrets.
# The Job baseline defaults to the documented Primaris Pod-ready figures
# (~12s p50 / ~44s p95); override with --job-baseline-p50-ms /
# --job-baseline-p95-ms for the same cluster shape, or pass 0/0 for raw
# warm-path numbers with no comparison.
set -euo pipefail

ITERATIONS=20
OUT=""
BACKEND="fake"
HARNESS="openCode"
PROMPT="standing latency probe: reply briefly"
JOB_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--iterations) ITERATIONS="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --live) BACKEND="process-exec"; shift ;;
    --process-stub) BACKEND="process-stub"; shift ;;
    --harness) HARNESS="$2"; shift 2 ;;
    --prompt) PROMPT="$2"; shift 2 ;;
    --job-baseline-p50-ms|--job-baseline-p95-ms) JOB_ARGS+=("$1" "$2"); shift 2 ;;
    -h|--help)
      sed -n '2,22p' "$0"
      exit 0
      ;;
    *)
      echo "error: unknown argument $1" >&2
      exit 2
      ;;
  esac
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/standing-latency"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

go build -trimpath -o "$BIN" ./cmd/standing-latency

if [[ "$BACKEND" == "process-exec" ]]; then
  # Recipe binary names differ from harness kind names (openCode kind ->
  # opencode CLI); warn early but let the backend fail closed with its own
  # error when the CLI is missing.
  BIN_NAME="$HARNESS"
  case "$HARNESS" in
    openCode) BIN_NAME="opencode" ;;
    openClaw) BIN_NAME="openclaw" ;;
    grokBuild) BIN_NAME="grok" ;;
    primeAgent) BIN_NAME="prime-agent" ;;
  esac
  if ! command -v "$BIN_NAME" >/dev/null 2>&1; then
    echo "warning: no $BIN_NAME binary on PATH for harness kind $HARNESS; the backend fails closed if its CLI is missing" >&2
  fi
fi

if [[ -z "$OUT" ]]; then
  OUT="$ROOT/.runtime/standing-latency-$BACKEND.json"
fi
mkdir -p "$(dirname "$OUT")"

"$BIN" -backend "$BACKEND" -n "$ITERATIONS" -harness "$HARNESS" -prompt "$PROMPT" "${JOB_ARGS[@]}" -out "$OUT"
