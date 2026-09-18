#!/usr/bin/env bash
# standing-latency-compare.sh — standing InProcess warm path vs the Job
# cold-start baseline for a direct turn AND a peer delivery, including cold
# FIRST turns and resumed SECOND turns (slice-5b native session ids).
#
# Fake-backend by default: deterministic, no cluster, no subprocess, no model
# call — safe in CI. The report lands under .runtime/ (gitignored local
# artifact) and is also printed to stdout.
#
# Usage:
#   hack/standing-latency-compare.sh [--iterations 20] [--out .runtime/standing-latency-fake.json]
#   hack/standing-latency-compare.sh --process-stub --iterations 20
#   hack/standing-latency-compare.sh --live --harness codex -n 10 \
#     --out .runtime/standing-latency-live.json
#   hack/standing-latency-compare.sh --live --harness codex -n 5 \
#     --only directTurnCold,directTurnResumed \
#     --out .runtime/standing-latency-live-cold-resume.json
#
# Scenarios (sixteen, per plane direct + peer-child): session cold-create
# (*EnsureCold), warm resume (*EnsureWarm), full warm turn (*TurnWarm),
# time-to-first-token (*FirstToken), cold FIRST turns (*TurnCold /
# *FirstTokenCold: fresh thread, first StreamTurn, records the native session
# id when the harness kind supports one), and resumed SECOND turns
# (*TurnResumed / *FirstTokenResumed: same thread, second StreamTurn reusing
# the recorded native id). The report's nativeResume section
# ({supported, coldTurns, resumedTurns, nativeIDsObserved}) proves whether
# second turns actually resumed. --only runs a comma-separated scenario
# subset for cheap live probes; the promote/reshape/retire bars need the full
# matrix (re-run without --only for a verdict).
#
# --live runs one real harness subprocess per measured turn through
# standing.ProcessBackend (PATH-resolved CLI, harness's own auth home).
# Local-only: needs the CLI for -harness on PATH plus that CLI's own local
# auth (e.g. ~/.codex/auth.json for codex — never a Kubernetes Secret, never
# a flag or env var to this script). The backend never reads Secrets; the
# child env is the filtered ambient env (no KUBE*/KUBERNETES_*, no OIDC/token
# material, no KUBECONFIG, no *DATABASE_URL). Resume-supporting kinds
# (codex, openCode, openClaw) resume the recorded native session on second
# turns; grokBuild/primeAgent/agy and Fake-only kinds honestly run cold.
# The Job baseline defaults to the documented Primaris Pod-ready figures
# (~12s p50 / ~44s p95); override with --job-baseline-p50-ms /
# --job-baseline-p95-ms for the same cluster shape, or pass 0/0 for raw
# warm-path numbers with no comparison.
#
# Live collection checklist (run on the same cluster shape as the baseline):
#   1. command -v <cli>  (codex | opencode | openclaw | grok | prime-agent | agy)
#   2. Authenticate the CLI itself once (its own login flow / auth home);
#      confirm one manual turn works, e.g.: printf 'reply briefly: hi' | codex exec --skip-git-repo-check --json | head -c 300
#   3. hack/standing-latency-compare.sh --live --harness <kind> -n 10 --out .runtime/standing-latency-live.json
#   4. Compare directTurnCold vs directTurnResumed p50/p95 and nativeResume.nativeIDsObserved (= 4*n when
#      every cold turn records and every resumed turn reuses); feed the bars in docs/standing-inprocess-harness.md.
# No provider credentials enter the API Secret surface at any step.
set -euo pipefail

ITERATIONS=20
OUT=""
BACKEND="fake"
HARNESS="openCode"
PROMPT="standing latency probe: reply briefly"
ONLY=""
JOB_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--iterations) ITERATIONS="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --live) BACKEND="process-exec"; shift ;;
    --process-stub) BACKEND="process-stub"; shift ;;
    --harness) HARNESS="$2"; shift 2 ;;
    --prompt) PROMPT="$2"; shift 2 ;;
    --only) ONLY="$2"; shift 2 ;;
    --job-baseline-p50-ms|--job-baseline-p95-ms) JOB_ARGS+=("$1" "$2"); shift 2 ;;
    -h|--help)
      sed -n '2,44p' "$0"
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
  case "$HARNESS" in
    grokBuild|primeAgent|agy|hermesAgent|piAgent|custom|"")
      echo "note: harness kind $HARNESS has no documented native resume surface; resumed second turns run the cold path (only the process-local warm-reuse win applies)" >&2
      ;;
  esac
fi

if [[ -z "$OUT" ]]; then
  OUT="$ROOT/.runtime/standing-latency-$BACKEND.json"
fi
mkdir -p "$(dirname "$OUT")"

ONLY_ARGS=()
if [[ -n "$ONLY" ]]; then
  ONLY_ARGS=(-only "$ONLY")
fi

"$BIN" -backend "$BACKEND" -n "$ITERATIONS" -harness "$HARNESS" -prompt "$PROMPT" "${ONLY_ARGS[@]}" "${JOB_ARGS[@]}" -out "$OUT"
