#!/usr/bin/env bash
# Launch Anvil Agents Desktop on a machine with a display (including Cursor cloud VMs).
# Control plane is the anvil-agents OIDC API only — no kubeconfig.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="${ANVIL_DESKTOP_VM_DIR:-${XDG_RUNTIME_DIR:-/tmp}/anvil-desktop-vm}"
listen="${ANVIL_DESKTOP_LISTEN:-127.0.0.1:1738}"
api_origin="${ANVIL_DESKTOP_API_ORIGIN:-}"
open_window=0
install_fixtures=1
detach=0
replace=1
build_ui=1
stub_ok=1
check_only=0

usage() {
	cat <<'EOF'
Launch Anvil Agents Desktop for a local display (loopback OIDC API client).

Usage:
  ./hack/run-anvil-desktop.sh [--open] [--detach] [--fixtures|--no-fixtures]
  ./hack/run-anvil-desktop.sh --check
  ./hack/run-anvil-desktop.sh --stop

Options:
  --open              Open a Chrome --app window on $DISPLAY
  --detach            Leave the host (and window) running after this script exits
  --fixtures          Install fixture Codex/Grok/OpenClaw CLIs (default)
  --no-fixtures        Discover whatever is already on PATH
  --api-origin URL     anvil-agents OIDC API origin
  --listen ADDR       Loopback listen address (default 127.0.0.1:1738)
  --no-build          Skip rebuilding web/desktop/dist when it already exists
  --no-stub           Do not start a loopback stub API if the live origin is down
  --stop              Stop a previous detached run from this workdir
  --check             Print a snapshot/OIDC report against an already-running host
  -h, --help          Show this help

Environment:
  ANVIL_DESKTOP_API_ORIGIN        OIDC API origin (Kind-local for tests)
  ANVIL_DESKTOP_KIND_API_ORIGIN  Fallback Kind API origin (default http://127.0.0.1:18080)
  ANVIL_DESKTOP_VM_DIR           Work directory (pid files, fixtures, chrome profile)
  DISPLAY                        Required for --open

The OIDC token never goes in a query string. Completing PKCE requires
http://127.0.0.1:1738/auth/callback on the Kind OIDC client (later: Zitadel).
This helper does not default to production Zitadel and does not register
that URI.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--open) open_window=1 ;;
	--detach) detach=1 ;;
	--fixtures) install_fixtures=1 ;;
	--no-fixtures) install_fixtures=0 ;;
	--api-origin)
		api_origin="${2:-}"
		shift
		;;
	--listen)
		listen="${2:-}"
		shift
		;;
	--no-build) build_ui=0 ;;
	--no-stub) stub_ok=0 ;;
	--stop) replace=1; check_only=0; open_window=0; detach=0
		stop_only=1
		;;
	--check) check_only=1 ;;
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
	shift
done

stop_only="${stop_only:-0}"
mkdir -p "${workdir}"
pidfile="${workdir}/anvil-desktop.pid"
stub_pidfile="${workdir}/stub-api.pid"
chrome_pidfile="${workdir}/chrome.pid"
report="${workdir}/report.txt"
fixture_dir="${workdir}/bin"
ui_dir="${root}/web/desktop/dist"
stub_listen="127.0.0.1:1739"
url="http://${listen}"

host_port() {
	local addr="$1"
	echo "${addr##*:}"
}

is_running() {
	local file="$1"
	[[ -f "${file}" ]] || return 1
	local pid
	pid="$(cat "${file}")"
	[[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

stop_pidfile() {
	local file="$1"
	local name="$2"
	if is_running "${file}"; then
		local pid
		pid="$(cat "${file}")"
		kill "${pid}" 2>/dev/null || true
		for _ in 1 2 3 4 5; do
			kill -0 "${pid}" 2>/dev/null || break
			sleep 0.2
		done
		kill -9 "${pid}" 2>/dev/null || true
		echo "stopped ${name} pid ${pid}" >&2
	fi
	rm -f "${file}"
}

stop_listen() {
	local port
	port="$(host_port "${listen}")"
	local pids
	pids="$(ss -ltnp 2>/dev/null | awk -v p=":${port}" '$4 ~ p {print}' || true)"
	# Fallback: /proc
	if command -v fuser >/dev/null 2>&1; then
		fuser -k "${port}/tcp" >/dev/null 2>&1 || true
	fi
	unset pids
}

stop_all() {
	stop_pidfile "${chrome_pidfile}" "chrome"
	stop_pidfile "${pidfile}" "anvil-desktop"
	stop_pidfile "${stub_pidfile}" "stub-api"
}

install_fixture_bins() {
	local dir="$1"
	mkdir -p "${dir}"
	write_fixture() {
		local name="$1"
		local version="$2"
		cat >"${dir}/${name}" <<EOF
#!/bin/sh
if [ "\$1" = "--version" ] || [ "\$1" = "version" ]; then
  echo "${version}"
  exit 0
fi
echo "anvil-desktop-fixture:${name}"
while [ \$# -gt 0 ]; do
  if [ "\$1" = "--prompt-file" ] || [ "\$1" = "--message-file" ]; then
    cat "\$2"
    exit 0
  fi
  shift
done
cat
EOF
		chmod +x "${dir}/${name}"
	}
	write_fixture codex "codex-cli 0.50.0-desktop-vm"
	write_fixture grok "grok 1.2.3-desktop-vm"
	write_fixture openclaw "openclaw 0.9.0-desktop-vm"
}

origin_ok() {
	local origin="$1"
	local body
	body="$(curl -fsS --max-time 8 "${origin}/ui-config.json" 2>/dev/null || true)"
	[[ -n "${body}" ]] || return 1
	python3 - "${body}" <<'PY'
import json, sys
body = sys.argv[1]
try:
    data = json.loads(body)
except Exception:
    raise SystemExit(1)
oidc = data.get("oidc") or {}
if not oidc.get("issuer") or not oidc.get("clientId"):
    raise SystemExit(1)
PY
}

probe_issuer() {
	local issuer="$1"
	curl -fsS --max-time 8 "${issuer%/}/.well-known/openid-configuration" >/dev/null
}

json_field() {
	python3 -c 'import json,sys; d=json.load(sys.stdin); v=d
for k in sys.argv[1].split("."):
    v = (v or {}).get(k) if isinstance(v, dict) else None
print(v or "")' "$1"
}

start_stub() {
	python3 "${root}/hack/anvil-desktop-stub-api.py" --listen "${stub_listen}" &
	echo $! >"${stub_pidfile}"
	for _ in $(seq 1 25); do
		if curl -fsS --max-time 1 "http://${stub_listen}/healthz" >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.1
	done
	echo "stub API failed to start on ${stub_listen}" >&2
	return 1
}

write_report() {
	local snap="$1"
	{
		echo "Anvil Agents Desktop VM report"
		echo "url: ${url}"
		echo "apiOrigin: ${api_origin}"
		echo "uiConfigIssuer: ${issuer:-}"
		echo "issuerDiscovery: ${issuer_status:-unknown}"
		echo "redirectUri: ${url}/auth/callback"
		echo "signedIn: no (OIDC PKCE is interactive)"
		echo "harnesses:"
		python3 - "${snap}" <<'PY'
import json, sys
data = json.loads(sys.argv[1])
for item in data.get("harnesses") or []:
    mark = "present" if item.get("present") else "missing"
    extra = ""
    if item.get("delegatable"):
        extra = " delegatable"
    version = item.get("version") or ""
    print(f"  - {item.get('id')}: {mark}{extra} {version}".rstrip())
print("wrapperTools:", ", ".join(t.get("id","") for t in (data.get("wrapper") or {}).get("tools") or []))
print("kubernetesFields:", "kubeconfig" in json.dumps(data))
PY
		if [[ "${issuer_status:-}" != "ok" ]]; then
			echo "blocker: OIDC issuer discovery failed from this VM (${issuer:-none}). Login surface is shown; PKCE cannot complete until the Kind issuer is up. Production IdP is Zitadel; this helper does not use it as the test default."
		else
			echo "blocker: completing Sign in requires registering ${url}/auth/callback on the Kind OIDC client (later: the Zitadel client). This helper does not change IdP clients."
		fi
	} | tee "${report}"
}

if [[ "${stop_only}" == 1 ]]; then
	stop_all
	exit 0
fi

if [[ "${check_only}" == 1 ]]; then
	snap="$(curl -fsS --max-time 5 "${url}/local/v1/snapshot")"
	api_origin="$(printf '%s' "${snap}" | json_field prefs.apiOrigin)"
	cfg="$(curl -fsS --max-time 5 "${url}/ui-config.json" || true)"
	issuer="$(printf '%s' "${cfg}" | json_field oidc.issuer)"
	issuer_status="skipped"
	if [[ -n "${issuer}" ]] && probe_issuer "${issuer}"; then
		issuer_status="ok"
	elif [[ -n "${issuer}" ]]; then
		issuer_status="failed"
	fi
	write_report "${snap}"
	exit 0
fi

if [[ "${replace}" == 1 ]]; then
	stop_all
	stop_listen
	sleep 0.3
fi

if [[ "${install_fixtures}" == 1 ]]; then
	install_fixture_bins "${fixture_dir}"
	path_flag=("${fixture_dir}")
else
	path_flag=()
fi

if [[ -z "${api_origin}" ]]; then
	kind_origin="${ANVIL_DESKTOP_KIND_API_ORIGIN:-http://127.0.0.1:18080}"
	if [[ -n "${ANVIL_DESKTOP_API_ORIGIN:-}" ]] && origin_ok "${ANVIL_DESKTOP_API_ORIGIN}"; then
		api_origin="${ANVIL_DESKTOP_API_ORIGIN}"
	elif origin_ok "${kind_origin}"; then
		api_origin="${kind_origin}"
		echo "using Kind-local OIDC API ${api_origin}" >&2
	elif [[ "${stub_ok}" == 1 ]]; then
		start_stub
		api_origin="http://${stub_listen}"
		echo "Kind OIDC API origin was not reachable; using stub ${api_origin} (login surface only). Production Zitadel is not the test default." >&2
	else
		echo "no anvil-agents OIDC API origin is reachable (set ANVIL_DESKTOP_API_ORIGIN to the Kind API)" >&2
		exit 1
	fi
fi

if [[ "${build_ui}" == 1 || ! -f "${ui_dir}/index.html" ]]; then
	if [[ ! -d "${root}/web/desktop/node_modules" ]]; then
		(cd "${root}/web/desktop" && npm ci)
	fi
	(cd "${root}/web/desktop" && npm run build)
fi

bin="${workdir}/anvil-desktop"
(cd "${root}" && go build -o "${bin}" ./cmd/anvil-desktop)

host_args=(
	--listen "${listen}"
	--ui-dir "${ui_dir}"
	--api-origin "${api_origin}"
	--config-dir "${workdir}/prefs"
)
if [[ "${install_fixtures}" == 1 ]]; then
	host_args+=(--path "${fixture_dir}")
fi

"${bin}" "${host_args[@]}" >"${workdir}/host.log" 2>&1 &
echo $! >"${pidfile}"

for _ in $(seq 1 50); do
	if curl -fsS --max-time 1 "${url}/healthz" >/dev/null 2>&1; then
		break
	fi
	if ! is_running "${pidfile}"; then
		echo "anvil-desktop failed to start:" >&2
		cat "${workdir}/host.log" >&2 || true
		exit 1
	fi
	sleep 0.15
done

if ! curl -fsS --max-time 2 "${url}/healthz" >/dev/null; then
	echo "anvil-desktop did not become healthy on ${url}" >&2
	cat "${workdir}/host.log" >&2 || true
	exit 1
fi

snap="$(curl -fsS --max-time 5 "${url}/local/v1/snapshot")"
cfg="$(curl -fsS --max-time 5 "${url}/ui-config.json")"
issuer="$(printf '%s' "${cfg}" | json_field oidc.issuer)"
issuer_status="failed"
if [[ -n "${issuer}" ]] && probe_issuer "${issuer}"; then
	issuer_status="ok"
fi
write_report "${snap}"

if [[ "${open_window}" == 1 ]]; then
	if [[ -z "${DISPLAY:-}" ]]; then
		echo "DISPLAY is unset; skipped Chrome window. Host is at ${url}" >&2
	else
		chrome="$(command -v google-chrome || command -v google-chrome-stable || command -v chromium || true)"
		if [[ -z "${chrome}" ]]; then
			echo "no Chrome/Chromium on PATH; host is at ${url}" >&2
		else
			mkdir -p "${workdir}/chrome-profile"
			"${chrome}" \
				--no-sandbox \
				--disable-dev-shm-usage \
				--user-data-dir="${workdir}/chrome-profile" \
				--app="${url}" \
				--window-size=1280,840 \
				--window-position=80,40 \
				>"${workdir}/chrome.log" 2>&1 &
			echo $! >"${chrome_pidfile}"
			echo "opened ${url} on DISPLAY=${DISPLAY}" >&2
		fi
	fi
fi

echo "Anvil Agents Desktop is at ${url}" >&2

if [[ "${detach}" == 1 ]]; then
	exit 0
fi

cleanup() {
	stop_all
}
trap cleanup EXIT INT TERM
echo "foreground run; Ctrl-C to stop" >&2
wait "$(cat "${pidfile}")"
