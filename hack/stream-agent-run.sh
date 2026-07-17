#!/usr/bin/env bash
set -euo pipefail

endpoint="${ANVIL_AGENTS_API_URL:-}"
namespace=""
run=""
token_file=""
tail_lines=""
last_event_id=""
ca_file=""

usage() {
	cat <<'EOF'
Stream one AgentRun from the OIDC-protected standalone API.

Usage:
  ANVIL_AGENTS_ACCESS_TOKEN=... ./hack/stream-agent-run.sh \
    --endpoint https://agents.example.com --namespace NAMESPACE --run NAME

Options:
  --endpoint URL          HTTPS base URL. Defaults to ANVIL_AGENTS_API_URL.
  --namespace NAMESPACE  AgentRun namespace.
  --run NAME             AgentRun name.
  --token-file PATH      Read the access token from a file instead of
                         ANVIL_AGENTS_ACCESS_TOKEN.
  --tail-lines NUMBER    Initial log tail. The server enforces its configured cap.
  --last-event-id ID     Resume from an SSE event cursor when it is still available.
  --ca-file PATH         CA bundle for the API endpoint's HTTPS certificate.
  -h, --help             Show this help.

The token is sent only in the Authorization header. This script deliberately
does not support query-string tokens or a --token command-line argument.
EOF
}

require_value() {
	local option="$1"
	local value="${2:-}"
	if [[ -z "${value}" ]]; then
		echo "${option} requires a value" >&2
		exit 2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--endpoint)
			require_value "$1" "${2:-}"
			endpoint="${2%/}"
			shift 2
			;;
		--namespace)
			require_value "$1" "${2:-}"
			namespace="$2"
			shift 2
			;;
		--run)
			require_value "$1" "${2:-}"
			run="$2"
			shift 2
			;;
		--token-file)
			require_value "$1" "${2:-}"
			token_file="$2"
			shift 2
			;;
		--tail-lines)
			require_value "$1" "${2:-}"
			tail_lines="$2"
			shift 2
			;;
		--last-event-id)
			require_value "$1" "${2:-}"
			last_event_id="$2"
			shift 2
			;;
		--ca-file)
			require_value "$1" "${2:-}"
			ca_file="$2"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

if [[ -z "${endpoint}" || -z "${namespace}" || -z "${run}" ]]; then
	echo "--endpoint, --namespace, and --run are required" >&2
	exit 2
fi
if [[ ! "${endpoint}" =~ ^https://(\[[0-9A-Fa-f:]+\]|[A-Za-z0-9.-]+)(:[0-9]{1,5})?$ ]]; then
	echo "--endpoint must be an HTTPS origin without a path, query, or fragment" >&2
	exit 2
fi
for value in "${namespace}" "${run}"; do
	if [[ ! "${value}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]]; then
		echo "namespace and run must be URI-safe Kubernetes names" >&2
		exit 2
	fi
done
if [[ -n "${tail_lines}" && ! "${tail_lines}" =~ ^[1-9][0-9]*$ ]]; then
	echo "--tail-lines must be a positive integer" >&2
	exit 2
fi

if [[ -n "${token_file}" ]]; then
	if [[ ! -r "${token_file}" ]]; then
		echo "cannot read token file: ${token_file}" >&2
		exit 2
	fi
	IFS= read -r token <"${token_file}" || true
else
	token="${ANVIL_AGENTS_ACCESS_TOKEN:-}"
fi
if [[ -z "${token}" ]]; then
	echo "set ANVIL_AGENTS_ACCESS_TOKEN or pass --token-file" >&2
	exit 2
fi
if [[ ! "${token}" =~ ^[A-Za-z0-9._~+/=-]+$ ]]; then
	echo "access token contains characters outside the bearer-token syntax" >&2
	exit 2
fi
if [[ -n "${last_event_id}" && ! "${last_event_id}" =~ ^[A-Za-z0-9:._=-]+$ ]]; then
	echo "--last-event-id contains unsupported characters" >&2
	exit 2
fi
if [[ -n "${ca_file}" && ! -r "${ca_file}" ]]; then
	echo "cannot read CA file: ${ca_file}" >&2
	exit 2
fi
unset ANVIL_AGENTS_ACCESS_TOKEN

command -v curl >/dev/null 2>&1 || {
	echo "curl is required" >&2
	exit 1
}

url="${endpoint}/api/v1/namespaces/${namespace}/agent-runs/${run}/events"
if [[ -n "${tail_lines}" ]]; then
	url="${url}?tailLines=${tail_lines}"
fi

# Keep the bearer token out of the process argument list. The protected
# temporary curl config is removed on normal exit and common termination
# signals, and the token environment variable is not inherited by curl.
umask 077
curl_config="$(mktemp "${TMPDIR:-/tmp}/anvil-agents-stream.XXXXXX")"
trap 'rm -f "${curl_config}"' EXIT HUP INT TERM
{
	printf 'header = "Authorization: Bearer %s"\n' "${token}"
	printf 'header = "Accept: text/event-stream"\n'
	if [[ -n "${last_event_id}" ]]; then
		printf 'header = "Last-Event-ID: %s"\n' "${last_event_id}"
	fi
} >"${curl_config}"
unset token

curl_args=(
	--no-buffer
	--fail-with-body
	--silent
	--show-error
	--proto '=https'
	--config "${curl_config}"
	--url "${url}"
)
if [[ -n "${ca_file}" ]]; then
	curl_args+=(--cacert "${ca_file}")
fi

env -u ANVIL_AGENTS_ACCESS_TOKEN curl "${curl_args[@]}"
