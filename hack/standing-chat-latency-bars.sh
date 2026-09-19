#!/usr/bin/env bash
# standing-chat-latency-bars.sh — score Desktop signed-in standing JSONL
# against the Slice 5a promote/reshape/retire bars.
#
# Reads .runtime/chat-latency.jsonl (or --jsonl), filters to
# source desktop-chat-live-signed-in on path standing, computes p50/p95 for
# sendToFirstTokenMs and replyReadyMs, compares to the Job Pod-ready baseline
# flags (default ~12s p50 / ~44s p95), and emits a verdict + reason using the
# SAME vocabulary as Slice 5a in docs/standing-inprocess-harness.md
# (promote / reshape / retire / inconclusive-live / no-baseline — no new bar
# names).
#
# Honest single-plane mapping (see cmd/standing-latency/chat_bars.go):
# sendToFirstTokenMs plays the first-token role (p95 <= 5000ms and <=
# jobP95/10), replyReadyMs plays the full warm-turn role (p95 <= jobP95/10,
# retire when p50 >= jobP50/2). The JSONL has no direct-vs-peer split, so
# reshape is never emitted here — a direct win with an unknown peer reports
# inconclusive-live and points at the full standing-latency matrix for the
# peer dimension. Promote additionally needs --min-samples delivered samples
# (default 10): tonight's n=5 already sits well under the first-token bar but
# cannot carry a p95 promote call.
#
# Usage:
#   hack/standing-chat-latency-bars.sh [--jsonl .runtime/chat-latency.jsonl]
#   hack/standing-chat-latency-bars.sh --jsonl .runtime/chat-latency.jsonl \
#     --out .runtime/standing-chat-latency-bars.json
#
# The sink is gitignored local data: a missing file is an error and no
# samples are ever invented. Fixture scoring is pinned by
# cmd/standing-latency/chat_bars_test.go against
# cmd/standing-latency/testdata/chat-latency-synthetic.jsonl (synthetic
# values only — never live numbers).
set -euo pipefail

JSONL="$PWD/.runtime/chat-latency.jsonl"
OUT=""
SOURCE="desktop-chat-live-signed-in"
PATH_TAG="standing"
MIN_SAMPLES=10
JOB_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --jsonl) JSONL="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --source) SOURCE="$2"; shift 2 ;;
    --path) PATH_TAG="$2"; shift 2 ;;
    --min-samples) MIN_SAMPLES="$2"; shift 2 ;;
    --job-baseline-p50-ms|--job-baseline-p95-ms) JOB_ARGS+=("$1" "$2"); shift 2 ;;
    -h|--help)
      sed -n '2,26p' "$0"
      exit 0
      ;;
    *)
      echo "error: unknown argument $1" >&2
      exit 2
      ;;
  esac
done

if [[ ! -f "$JSONL" ]]; then
  echo "error: no JSONL at $JSONL (collect signed-in standing turns first; no samples invented)" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/standing-latency"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

go build -trimpath -o "$BIN" ./cmd/standing-latency

if [[ -z "$OUT" ]]; then
  OUT="$ROOT/.runtime/standing-chat-latency-bars.json"
fi
mkdir -p "$(dirname "$OUT")"

"$BIN" -chat-jsonl "$JSONL" -chat-source "$SOURCE" -chat-path "$PATH_TAG" -min-samples "$MIN_SAMPLES" "${JOB_ARGS[@]}" -out "$OUT"
