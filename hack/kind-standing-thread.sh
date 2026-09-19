#!/usr/bin/env bash
# kind-standing-thread.sh — ensure the Kind-local standing-enabled manager thread.
#
# TEST ONLY. Ensures (idempotently) the standing chat thread the signed-in
# live probe needs: POSTs {"standing": true, "profileName": ...} so re-runs
# converge on one thread (201 created, 200 existing) instead of minting a new
# thread per run. Requires the Kind-local standing-manager composition
# applied first:
#
#   kubectl apply -f examples/live-api/kind-local-standing-manager.yaml
#   export ANVIL_AGENTS_ACCESS_TOKEN=<minted Kind-local bearer>
#   thread="$(hack/kind-standing-thread.sh | tail -n 1)"
#   node --experimental-strip-types hack/desktop-standing-chat-live.mjs --live \
#     --api-origin http://127.0.0.1:18080 --namespace agents --thread "${thread}"
#
# The bearer comes from ANVIL_AGENTS_ACCESS_TOKEN or --token-file only. It is
# sent in the Authorization header, never placed in a URL or query string,
# and never printed (success prints the thread id only).
#
# Full runbook: docs/standing-inprocess-harness.md ("Desktop chat e2e over
# the standing WebSocket").
set -euo pipefail

api_origin="http://127.0.0.1:18080"
namespace="agents"
profile="kind-local-manager"
mode="persona"
title="Kind-local standing manager"
token_file=""
timeout_s="30"

usage() {
	cat <<'EOF'
Usage: hack/kind-standing-thread.sh [options]

  --api-origin URL   anvil-agents API origin (default http://127.0.0.1:18080).
  --namespace NS     chat namespace (default agents).
  --profile NAME     direct agent profile bound to an InProcess harness
                     (default kind-local-manager).
  --mode MODE        thread mode (default persona).
  --title TEXT       thread title for a fresh thread (default "Kind-local standing manager").
  --token-file PATH  read the bearer from a protected file instead of env.
  --timeout-s N      curl max time in seconds (default 30).
  -h, --help         show this help.

Bearer: ANVIL_AGENTS_ACCESS_TOKEN or --token-file. Never pass it as an
argument value in shared logs.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--api-origin)
		api_origin="${2:?--api-origin requires a value}"
		shift 2
		;;
	--namespace)
		namespace="${2:?--namespace requires a value}"
		shift 2
		;;
	--profile)
		profile="${2:?--profile requires a value}"
		shift 2
		;;
	--mode)
		mode="${2:?--mode requires a value}"
		shift 2
		;;
	--title)
		title="${2:?--title requires a value}"
		shift 2
		;;
	--token-file)
		token_file="${2:?--token-file requires a value}"
		shift 2
		;;
	--timeout-s)
		timeout_s="${2:?--timeout-s requires a value}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "kind-standing-thread: unknown argument: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

command -v curl >/dev/null 2>&1 || {
	echo "kind-standing-thread: curl is required" >&2
	exit 2
}
command -v python3 >/dev/null 2>&1 || {
	echo "kind-standing-thread: python3 is required" >&2
	exit 2
}

# Load the bearer without ever printing it. Env wins; a token file holds the
# raw token (first whitespace-delimited field) with 0600 handling left to the
# operator.
token=""
if [[ -n "${ANVIL_AGENTS_ACCESS_TOKEN:-}" ]]; then
	token="${ANVIL_AGENTS_ACCESS_TOKEN}"
elif [[ -n "${token_file}" ]]; then
	token="$(python3 -c 'import sys; print(open(sys.argv[1], encoding="utf-8").read().split()[0])' "${token_file}")"
fi
if [[ -z "${token}" ]]; then
	echo "kind-standing-thread: no bearer: set ANVIL_AGENTS_ACCESS_TOKEN or pass --token-file" >&2
	exit 2
fi
if [[ -z "${profile// }" ]]; then
	echo "kind-standing-thread: --profile must not be blank (standing threads require profileName)" >&2
	exit 2
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
body_file="${tmp_dir}/thread.json"
request_file="${tmp_dir}/request.json"

# Build the standing-ensure request with structured JSON only (never string
# concatenation): standing threads require profileName and use the agent's
# configured harness, so no harnessProfileName or metadata is sent.
python3 - "${request_file}" "${profile}" "${mode}" "${title}" <<'PY'
import json, sys
path, profile, mode, title = sys.argv[1], sys.argv[2].strip(), sys.argv[3].strip(), sys.argv[4]
payload = {"standing": True, "profileName": profile}
if mode:
    payload["mode"] = mode
if title:
    payload["title"] = title
with open(path, "w", encoding="utf-8") as fh:
    json.dump(payload, fh)
PY

url="${api_origin%/}/api/v1/namespaces/${namespace}/chat/threads"
http_code="$(curl -sS --max-time "${timeout_s}" -o "${body_file}" -w '%{http_code}' \
	-X POST "${url}" \
	-H 'Accept: application/json' \
	-H 'Content-Type: application/json' \
	-H "Authorization: Bearer ${token}" \
	-d "@${request_file}")" || {
	echo "kind-standing-thread: API unreachable at ${api_origin}" >&2
	exit 1
}
# The token must never reach stdout: only the status line and the server body
# (which never contains the bearer) are reported from here on.
token=""

case "${http_code}" in
200 | 201) ;;
401)
	echo "kind-standing-thread: 401 unauthorized (stub sessions are local-only; mint a Kind-local token)" >&2
	exit 1
	;;
403)
	echo "kind-standing-thread: 403 forbidden (the bearer lacks chat:write for namespace ${namespace})" >&2
	exit 1
	;;
400)
	echo "kind-standing-thread: 400 invalid target ($(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("error",{}).get("message","unknown"))' "${body_file}"))" >&2
	echo "kind-standing-thread: hint: kubectl apply -f examples/live-api/kind-local-standing-manager.yaml" >&2
	exit 1
	;;
*)
	echo "kind-standing-thread: thread ensure failed with status ${http_code}" >&2
	head -c 500 "${body_file}" >&2
	echo "" >&2
	exit 1
	;;
esac

thread_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id",""))' "${body_file}")"
if [[ -z "${thread_id}" ]]; then
	echo "kind-standing-thread: API response carried no thread id" >&2
	head -c 500 "${body_file}" >&2
	echo "" >&2
	exit 1
fi

if [[ "${http_code}" == "201" ]]; then
	echo "standing thread created: ${thread_id}"
else
	echo "standing thread existing: ${thread_id}"
fi
echo "${thread_id}"
