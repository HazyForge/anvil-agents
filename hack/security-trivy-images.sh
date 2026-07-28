#!/usr/bin/env bash
# Build (optional) and Trivy-scan every anvil-agents container image.
# Used by:
#   - make security-trivy / local optional mirror of GHA security
#   - optional local full scans
#   - GitHub Actions security.yml (public check runs per container)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
components=(controller codex opencode grok-build hermes openclaw pi)
image_prefix="${ANVIL_AGENTS_IMAGE_PREFIX:-}"
image_tag="${ANVIL_AGENTS_IMAGE_TAG:-security-scan}"
platform="${ANVIL_AGENTS_IMAGE_PLATFORM:-linux/amd64}"
report_dir="${TRIVY_REPORT_DIR:-${repo_root}/dist/security/trivy}"
severity="${TRIVY_SEVERITY:-HIGH,CRITICAL}"
exit_code="${TRIVY_EXIT_CODE:-1}"
ignore_unfixed="${TRIVY_IGNORE_UNFIXED:-true}"
build_images="${TRIVY_BUILD_IMAGES:-true}"
scan_only_refs=()
selected_components=()

usage() {
	cat <<'EOF'
Trivy-scan each anvil-agents container (controller + six runners).

Usage:
  ./hack/security-trivy-images.sh [options]
  ./hack/security-trivy-images.sh --component controller --component codex
  ./hack/security-trivy-images.sh --no-build --ref anvil-agents:dev --ref anvil-agent-run-codex:dev

Options:
  --component NAME     Scan one component; repeatable. Default: all seven.
  --prefix PREFIX      Image prefix (e.g. ghcr.io/hazyforge). Default: local names.
  --tag TAG            Image tag when building/scanning by component. Default: security-scan.
  --platform PLATFORM  Build platform. Default: linux/amd64.
  --report-dir DIR     Write JSON/table/SARIF reports here. Default: dist/security/trivy.
  --severity LIST      Trivy severities. Default: HIGH,CRITICAL.
  --no-build           Do not build; scan existing local/remote refs for components.
  --ref IMAGE          Scan this exact image ref (skips component build matrix).
  --ignore-unfixed     Only fail on fixed CVEs (default).
  --include-unfixed    Fail on unfixed CVEs too.
  -h, --help

Environment:
  ANVIL_AGENTS_IMAGE_PREFIX, ANVIL_AGENTS_IMAGE_TAG, ANVIL_AGENTS_IMAGE_PLATFORM
  TRIVY_SEVERITY, TRIVY_EXIT_CODE, TRIVY_REPORT_DIR, TRIVY_BUILD_IMAGES
  TRIVY_IGNORE_UNFIXED=true|false
EOF
}

image_name() {
	case "$1" in
		controller) printf '%s\n' anvil-agents ;;
		codex) printf '%s\n' anvil-agent-run-codex ;;
		opencode) printf '%s\n' anvil-agent-run-opencode ;;
		grok-build) printf '%s\n' anvil-agent-run-grok-build ;;
		hermes) printf '%s\n' anvil-agent-run-hermes ;;
		openclaw) printf '%s\n' anvil-agent-run-openclaw ;;
		pi) printf '%s\n' anvil-agent-run-pi ;;
		*) return 1 ;;
	esac
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--component)
			require_value "$1" "${2:-}"
			selected_components+=("$2")
			shift 2
			;;
		--prefix)
			require_value "$1" "${2:-}"
			image_prefix="${2%/}"
			shift 2
			;;
		--tag)
			require_value "$1" "${2:-}"
			image_tag="$2"
			shift 2
			;;
		--platform)
			require_value "$1" "${2:-}"
			platform="$2"
			shift 2
			;;
		--report-dir)
			require_value "$1" "${2:-}"
			report_dir="$2"
			shift 2
			;;
		--severity)
			require_value "$1" "${2:-}"
			severity="$2"
			shift 2
			;;
		--no-build)
			build_images="false"
			shift
			;;
		--ref)
			require_value "$1" "${2:-}"
			scan_only_refs+=("$2")
			shift 2
			;;
		--ignore-unfixed)
			ignore_unfixed="true"
			shift
			;;
		--include-unfixed)
			ignore_unfixed="false"
			shift
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

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

ensure_trivy() {
	if command -v trivy >/dev/null 2>&1; then
		return 0
	fi
	echo "Installing trivy into $(go env GOPATH 2>/dev/null || echo /tmp)/bin ..."
	local install_dir
	if command -v go >/dev/null 2>&1; then
		install_dir="$(go env GOPATH)/bin"
		mkdir -p "${install_dir}"
		# Prefer official install script for full DB support.
	fi
	local tmp
	tmp="$(mktemp -d)"
	# shellcheck disable=SC2064
	trap "rm -rf '${tmp}'" RETURN
	curl -fsSL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh |
		sh -s -- -b "${tmp}"
	mkdir -p "${HOME}/.local/bin"
	install -m 0755 "${tmp}/trivy" "${HOME}/.local/bin/trivy"
	export PATH="${HOME}/.local/bin:${PATH}"
	command -v trivy >/dev/null 2>&1 || {
		echo "trivy install failed" >&2
		exit 1
	}
}

ensure_trivy

if [[ ${#selected_components[@]} -eq 0 ]]; then
	selected_components=("${components[@]}")
fi

for component in "${selected_components[@]}"; do
	if ! image_name "${component}" >/dev/null; then
		echo "unknown component: ${component}" >&2
		echo "valid: ${components[*]}" >&2
		exit 2
	fi
done

mkdir -p "${report_dir}"
summary="${report_dir}/summary.txt"
{
	echo "anvil-agents trivy container scan"
	echo "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "severity=${severity}"
	echo "ignore_unfixed=${ignore_unfixed}"
	echo "platform=${platform}"
	echo "tag=${image_tag}"
	echo "prefix=${image_prefix:-<local>}"
	echo
} >"${summary}"

scan_ref() {
	local label="$1"
	local ref="$2"
	local safe
	safe="$(printf '%s' "${label}" | tr '/:' '__')"
	local table_out="${report_dir}/${safe}.txt"
	local json_out="${report_dir}/${safe}.json"
	local sarif_out="${report_dir}/${safe}.sarif"

	echo "==> trivy image [${label}] ${ref}"
	local args=(
		image
		--severity "${severity}"
		--exit-code "${exit_code}"
		--format table
		--output "${table_out}"
	)
	if [[ "${ignore_unfixed}" == "true" ]]; then
		args+=(--ignore-unfixed)
	fi

	# Table for humans / Primaris logs; fail closed on HIGH/CRITICAL.
	if ! trivy "${args[@]}" "${ref}"; then
		echo "FAIL ${label} ${ref}" | tee -a "${summary}"
		cat "${table_out}" >&2 || true
		return 1
	fi

	# JSON + SARIF for artifacts / GitHub code scanning (non-fatal format writes).
	local meta_args=(image --severity "${severity}" --exit-code 0)
	if [[ "${ignore_unfixed}" == "true" ]]; then
		meta_args+=(--ignore-unfixed)
	fi
	trivy "${meta_args[@]}" --format json --output "${json_out}" "${ref}" || true
	trivy "${meta_args[@]}" --format sarif --output "${sarif_out}" "${ref}" || true

	echo "PASS ${label} ${ref}" | tee -a "${summary}"
	echo "  report: ${table_out}"
}

failed=0

if [[ ${#scan_only_refs[@]} -gt 0 ]]; then
	idx=0
	for ref in "${scan_only_refs[@]}"; do
		idx=$((idx + 1))
		label="ref${idx}"
		if ! scan_ref "${label}" "${ref}"; then
			failed=1
		fi
	done
else
	if [[ "${build_images}" == "true" ]]; then
		build_args=(
			--tag "${image_tag}"
			--platform "${platform}"
			--load
		)
		if [[ -n "${image_prefix}" ]]; then
			build_args+=(--prefix "${image_prefix}")
		fi
		for component in "${selected_components[@]}"; do
			build_args+=(--component "${component}")
		done
		echo "Building images for Trivy scan: ${selected_components[*]}"
		"${repo_root}/hack/build-images.sh" "${build_args[@]}"
	fi

	for component in "${selected_components[@]}"; do
		name="$(image_name "${component}")"
		if [[ -n "${image_prefix}" ]]; then
			ref="${image_prefix}/${name}:${image_tag}"
		else
			ref="${name}:${image_tag}"
		fi
		if ! scan_ref "${component}" "${ref}"; then
			failed=1
		fi
	done
fi

echo >>"${summary}"
if [[ "${failed}" -ne 0 ]]; then
	echo "RESULT=FAIL" | tee -a "${summary}"
	echo "Trivy found HIGH/CRITICAL issues in one or more containers." >&2
	echo "Reports: ${report_dir}" >&2
	exit 1
fi

echo "RESULT=PASS" | tee -a "${summary}"
echo "All container Trivy scans passed. Reports: ${report_dir}"
