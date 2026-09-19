#!/usr/bin/env bash
# Kind-local harness CLI + local auth readiness gate for standing chat.
#
# Blocker (c) on the path to a real signed-in Desktop standing-chat latency
# sample: ProcessBackend turns need one harness CLI with that CLI's own local
# auth on the API host (for kind codex: `codex` on PATH plus
# ~/.codex/auth.json from the CLI's own login flow). Without it, turns hold
# as NeedsHuman/InProcessNotWired and no latency sample is produced or
# invented. This helper verifies that gate offline (no network, no Docker,
# no model call) and prints the exact remediation when blocked.
#
# Where InProcessNotWired is set (see docs/standing-inprocess-harness.md):
# controller agentRunInProcessHold holds well-formed InProcess runs with no
# live claim, and the runapi turn path (reconcileStandingTurn) falls back to
# the same hold when the live gate is off, the harness is unresolvable, or
# the backend errors (missing CLI on PATH, non-zero exit, timeout, empty
# output, Fake-only kind). Provider credentials stay in the harness CLI's
# own auth home and outside the API Secret surface throughout; the API
# bearer below (ANVIL_AGENTS_ACCESS_TOKEN / --token-file, minted by
# cmd/kind-oidc-issuer) authenticates the operator/probe to the API only and
# is never injected into the harness child env (standing.processChildEnv
# strips it; see internal/standing/process_local_auth_test.go).
#
# Scope: host-run Kind-local API only (loopback bring-up). Never prints
# token bytes or auth-file contents — only paths and presence.
#
# Usage:
#   hack/kind-standing-harness.sh [--check] [options]
#
# Options:
#   --harness KIND      harness kind to check (default: $KIND_STANDING_HARNESS
#                       or codex, matching
#                       examples/live-api/kind-local-standing-manager.yaml).
#                       Live ProcessBackend kinds: codex, openCode, openClaw,
#                       grokBuild, primeAgent, agy. Fake-only kinds
#                       (hermesAgent, piAgent, custom, empty) always report
#                       blocked: turns hold as InProcessNotWired by design.
#   --bin PATH          explicit harness binary (default: PATH-resolved
#                       per-kind binary; $KIND_STANDING_BIN overrides).
#   --auth-file PATH    explicit provider auth file (default: per-kind auth
#                       home below; $KIND_STANDING_AUTH_FILE overrides).
#   --token-file PATH   read the API bearer from a protected file instead of
#                       $ANVIL_AGENTS_ACCESS_TOKEN (same convention as
#                       hack/desktop-standing-chat-live.mjs --token-file and
#                       hack/stream-agent-run.sh --token-file; compatible with
#                       `kind-oidc-issuer mint` output saved to a file).
#   --require-bearer    fail when no API bearer is present (default: bearer
#                       presence is reported as a next step, harness gate
#                       alone decides the exit code).
#   --print-auth-path   print the resolved provider auth file path and exit.
#   --check             run the readiness gate (default action).
#   -h, --help          print this help and exit.
#
# Exit codes: 0 ready, 1 blocked (with remediation), 2 usage error.
#
# Per-kind provider auth homes (CLI-managed; seed with each CLI's own login
# flow, never a Kubernetes Secret):
#   codex       ${CODEX_HOME:-$HOME/.codex}/auth.json
#   grokBuild   ${GROK_HOME:-$HOME/.grok}/auth.json
#   openCode    ${XDG_DATA_HOME:-$HOME/.local/share}/opencode/auth.json
#               (also accepted: $HOME/.config/opencode/auth.json)
#   openClaw    CLI-managed home (no single auth.json contract in this repo):
#               binary presence is the gate; confirm with one manual turn.
#   primeAgent  CLI-managed home: binary presence is the gate; confirm with
#               one manual turn.
#   agy         native Google auth (an invented ~/.agy/auth.json is NOT
#               consumed): binary presence is the gate; confirm with one
#               manual turn.
set -euo pipefail

HARNESS="${KIND_STANDING_HARNESS:-codex}"
BIN_OVERRIDE="${KIND_STANDING_BIN:-}"
AUTH_OVERRIDE="${KIND_STANDING_AUTH_FILE:-}"
TOKEN_FILE=""
REQUIRE_BEARER="0"
ACTION="check"

usage() {
  sed -n '2,/^set -euo pipefail$/p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --harness) HARNESS="${2:-}"; shift 2 ;;
    --bin) BIN_OVERRIDE="${2:-}"; shift 2 ;;
    --auth-file) AUTH_OVERRIDE="${2:-}"; shift 2 ;;
    --token-file) TOKEN_FILE="${2:-}"; shift 2 ;;
    --require-bearer) REQUIRE_BEARER="1"; shift ;;
    --print-auth-path) ACTION="print-auth-path"; shift ;;
    --check) ACTION="check"; shift ;;
    -h|--help) usage; exit 0 ;;
    --*) printf 'unknown flag %q (see --help)\n' "$1" >&2; exit 2 ;;
    *) printf 'unexpected argument %q (see --help)\n' "$1" >&2; exit 2 ;;
  esac
done

# harness_bin_name <kind>: PATH-resolved binary per the ProcessBackend recipe
# table (internal/standing/process.go processRecipes). Mirrors the desktop
# PATH catalog (internal/desktop/catalog.go Binaries).
harness_bin_name() {
  case "$1" in
    codex) printf 'codex' ;;
    openCode) printf 'opencode' ;;
    openClaw) printf 'openclaw' ;;
    grokBuild) printf 'grok' ;;
    primeAgent) printf 'prime-agent' ;;
    agy) printf 'agy' ;;
    *) return 1 ;;
  esac
}

# harness_is_live <kind>: live ProcessBackend kinds only. Fake-only kinds
# (hermesAgent, piAgent, custom, empty) name no local process and always
# hold as InProcessNotWired (see SupportedProcessKinds).
harness_is_live() {
  harness_bin_name "$1" >/dev/null 2>&1
}

# harness_auth_path <kind>: resolved provider auth file, or empty when the
# kind is CLI-managed with no single auth.json contract.
harness_auth_path() {
  if [[ -n "${AUTH_OVERRIDE}" ]]; then
    printf '%s' "${AUTH_OVERRIDE}"
    return 0
  fi
  case "$1" in
    codex)
      printf '%s' "${CODEX_HOME:-$HOME/.codex}/auth.json"
      ;;
    grokBuild)
      printf '%s' "${GROK_HOME:-$HOME/.grok}/auth.json"
      ;;
    openCode)
      if [[ -n "${XDG_DATA_HOME:-}" ]]; then
        printf '%s' "${XDG_DATA_HOME}/opencode/auth.json"
      else
        printf '%s' "$HOME/.local/share/opencode/auth.json"
      fi
      ;;
    openClaw|primeAgent|agy)
      # CLI-managed homes; no single auth.json contract to check.
      printf ''
      ;;
    *)
      return 1
      ;;
  esac
}

# harness_manual_turn <kind>: one manual turn proving the CLI's own auth
# works, mirroring the slice-5a live collection checklist.
harness_manual_turn() {
  case "$1" in
    codex) printf "printf 'reply briefly: hi' | codex exec --skip-git-repo-check --json | head -c 300" ;;
    openCode) printf "printf 'reply briefly: hi' | opencode run --format json | head -c 300" ;;
    openClaw) printf "printf 'reply briefly: hi' > /tmp/kind-harness-prompt.txt && openclaw agent --message-file /tmp/kind-harness-prompt.txt | head -c 300" ;;
    grokBuild) printf "printf 'reply briefly: hi' > /tmp/kind-harness-prompt.txt && grok --prompt-file /tmp/kind-harness-prompt.txt | head -c 300" ;;
    primeAgent) printf "printf 'reply briefly: hi' | prime-agent --print --mode json --no-session | head -c 300" ;;
    agy) printf "printf 'reply briefly: hi' | agy --dangerously-skip-permissions --input-format stream-json --output-format stream-json | head -c 300" ;;
    *) return 1 ;;
  esac
}

# load_api_bearer: env first, then --token-file (first non-blank line).
# Prints the bearer on stdout when present; returns 1 with no bytes printed
# when absent. Callers must never echo the result to logs.
load_api_bearer() {
  local from_env="${ANVIL_AGENTS_ACCESS_TOKEN:-}"
  # Trim whitespace without echoing the value anywhere.
  from_env="$(printf '%s' "${from_env}" | tr -d '[:space:]')"
  if [[ -n "${from_env}" ]]; then
    printf '%s' "${from_env}"
    return 0
  fi
  if [[ -n "${TOKEN_FILE}" ]]; then
    if [[ ! -f "${TOKEN_FILE}" ]]; then
      return 1
    fi
    local line=""
    line="$(grep -m1 -o '[^[:space:]]\+' "${TOKEN_FILE}" 2>/dev/null || true)"
    if [[ -n "${line}" ]]; then
      printf '%s' "${line}"
      return 0
    fi
    return 1
  fi
  return 1
}

if [[ "${ACTION}" == "print-auth-path" ]]; then
  if ! harness_is_live "${HARNESS}"; then
    printf 'harness kind %q is Fake-only: no local auth path (turns hold as InProcessNotWired)\n' "${HARNESS}" >&2
    exit 1
  fi
  path="$(harness_auth_path "${HARNESS}")"
  if [[ -z "${path}" ]]; then
    printf 'harness kind %q is CLI-managed: no single auth file path\n' "${HARNESS}" >&2
    exit 1
  fi
  printf '%s\n' "${path}"
  exit 0
fi

# Default action: --check readiness gate.
failures=0
blocked() {
  printf 'kind-standing-harness: blocked: %s\n' "$1" >&2
  failures=$((failures + 1))
}

if ! harness_is_live "${HARNESS}"; then
  blocked "harness kind \"${HARNESS}\" is Fake-only (hermesAgent, piAgent, custom, or empty name no local process). Turns hold as NeedsHuman/InProcessNotWired by design; select a live kind (codex, openCode, openClaw, grokBuild, primeAgent, agy) — kind-local default is codex per examples/live-api/kind-local-standing-manager.yaml."
  printf 'kind-standing-harness: NOT READY (harness=%s). No latency sample produced.\n' "${HARNESS}" >&2
  exit 1
fi

# 1. Binary on PATH (or explicit --bin).
want_bin="$(harness_bin_name "${HARNESS}")"
if [[ -n "${BIN_OVERRIDE}" ]]; then
  if [[ ! -x "${BIN_OVERRIDE}" ]]; then
    blocked "harness binary \"${BIN_OVERRIDE}\" (--bin) is not executable. Point --bin at the ${HARNESS} CLI or put ${want_bin} on PATH."
  fi
else
  if ! command -v "${want_bin}" >/dev/null 2>&1; then
    blocked "harness CLI \"${want_bin}\" is not on PATH (harness=${HARNESS}). Install the ${HARNESS} CLI on the API host so ProcessBackend can exec it; until then turns hold as NeedsHuman/InProcessNotWired."
  fi
fi

# 2. CLI's own local auth.
auth_path="$(harness_auth_path "${HARNESS}")"
if [[ -z "${auth_path}" ]]; then
  printf 'kind-standing-harness: note: harness=%s keeps provider auth in its CLI-managed home (no single auth.json contract).\n' "${HARNESS}" >&2
  printf 'kind-standing-harness: note: confirm with one manual turn: %s\n' "$(harness_manual_turn "${HARNESS}")" >&2
else
  # openCode accepts either data-home or config-home auth layout.
  candidates=("${auth_path}")
  if [[ "${HARNESS}" == "openCode" && -z "${AUTH_OVERRIDE}" ]]; then
    candidates+=("$HOME/.config/opencode/auth.json")
  fi
  found=""
  for candidate in "${candidates[@]}"; do
    if [[ -f "${candidate}" && -s "${candidate}" ]]; then
      found="${candidate}"
      break
    fi
  done
  if [[ -z "${found}" ]]; then
    case "${HARNESS}" in
      codex)
        blocked "provider auth file \"${auth_path}\" is missing or empty (CODEX_HOME respected when set). Authenticate the CLI itself once with its own login flow on the API host (e.g. codex login), then confirm one manual turn: $(harness_manual_turn "${HARNESS}"). Provider credentials stay in the CLI auth home, never a Kubernetes Secret."
        ;;
      grokBuild)
        blocked "provider auth file \"${auth_path}\" is missing or empty (GROK_HOME respected when set). Complete the Grok/xAI login flow on the API host, then confirm one manual turn: $(harness_manual_turn "${HARNESS}")."
        ;;
      openCode)
        blocked "provider auth file is missing or empty (checked ${candidates[*]}). Authenticate the opencode CLI once on the API host, then confirm one manual turn: $(harness_manual_turn "${HARNESS}")."
        ;;
    esac
  else
    if [[ -n "$(find "${found}" -perm -o+r 2>/dev/null)" ]]; then
      printf 'kind-standing-harness: warning: auth file %s is world-readable; consider chmod 600.\n' "${found}" >&2
    fi
    if command -v python3 >/dev/null 2>&1 && ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${found}" >/dev/null 2>&1; then
      printf 'kind-standing-harness: warning: auth file %s is not JSON; the CLI login flow may need a refresh.\n' "${found}" >&2
    fi
  fi
fi

# 3. API bearer presence for the probe step (operator shell only; never
# injected into the harness child env).
bearer_present="0"
if load_api_bearer >/dev/null 2>&1; then
  bearer_present="1"
fi
if [[ "${bearer_present}" == "0" ]]; then
  msg="no API bearer: set ANVIL_AGENTS_ACCESS_TOKEN or pass --token-file (save \`kind-oidc-issuer mint\` output to a 0600 file). Bearer authenticates the probe to the API only; it never enters the harness child env."
  if [[ "${REQUIRE_BEARER}" == "1" ]]; then
    blocked "${msg}"
  else
    printf 'kind-standing-harness: note: %s\n' "${msg}" >&2
  fi
fi

if [[ "${failures}" -gt 0 ]]; then
  printf 'kind-standing-harness: NOT READY (harness=%s). Turns hold as NeedsHuman/InProcessNotWired; no latency sample produced.\n' "${HARNESS}" >&2
  exit 1
fi

printf 'kind-standing-harness: ready (harness=%s).\n' "${HARNESS}" >&2
if [[ "${bearer_present}" == "1" ]]; then
  printf 'kind-standing-harness: next: API bearer present; run the live probe: node --experimental-strip-types hack/desktop-standing-chat-live.mjs --live --api-origin http://127.0.0.1:18080 --namespace agents --thread <thread-id> --out $PWD/.runtime/chat-latency.jsonl\n' >&2
else
  printf 'kind-standing-harness: next: mint a bearer (go run ./cmd/kind-oidc-issuer mint --key-file /tmp/kind-oidc.key.json --issuer http://127.0.0.1:18081 --audience anvil-agents --subject kind-local-desktop --roles kind-local-desktop --namespaces agents), export ANVIL_AGENTS_ACCESS_TOKEN, then run the live probe.\n' >&2
fi
