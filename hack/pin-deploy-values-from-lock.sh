#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
values_file="${repo_root}/.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml"
image_lock=""

usage() {
	cat <<'EOF'
Pin a deployment values file to an Anvil Agents release image lock.

Usage:
  ./hack/pin-deploy-values-from-lock.sh --image-lock dist/images-vX.Y.Z.lock.tsv [options]

Options:
  --image-lock FILE  Seven-image release lock from publish-release.sh.
  --values FILE      Values file to update. Default: first-party Anvil Primaris
                     deploy overlay.
  -h, --help         Show this help.

The script updates only the top-level controller image reference and the six
runnerImages entries. It is intended for the repo-local first-party deployment
overlay, not arbitrary Helm values.
EOF
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--image-lock)
			require_value "$1" "${2:-}"
			image_lock="$2"
			shift 2
			;;
		--values)
			require_value "$1" "${2:-}"
			values_file="$2"
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

[[ -n "${image_lock}" ]] || { echo "--image-lock is required" >&2; exit 2; }
[[ -f "${image_lock}" ]] || { echo "image lock does not exist: ${image_lock}" >&2; exit 2; }
[[ -f "${values_file}" ]] || { echo "values file does not exist: ${values_file}" >&2; exit 2; }

declare -A refs=()
lock_schema=""
while IFS=$'\t' read -r key value extra; do
	[[ -z "${extra:-}" ]] || { echo "invalid image lock row for ${key}" >&2; exit 2; }
	case "${key}" in
		schema) lock_schema="${value}" ;;
		controller|codex|opencode|grok-build|hermes|openclaw|pi) refs["${key}"]="${value}" ;;
	esac
done < "${image_lock}"
[[ "${lock_schema}" == "anvil-agents-image-lock/v1" ]] || { echo "image lock has an unsupported schema" >&2; exit 2; }
for component in controller codex opencode grok-build hermes openclaw pi; do
	[[ -n "${refs[${component}]:-}" ]] || { echo "image lock is missing component: ${component}" >&2; exit 2; }
	[[ "${refs[${component}]}" == *@sha256:* ]] || { echo "${component} is not digest-pinned" >&2; exit 2; }
done

tmp_file="$(mktemp "${values_file}.tmp.XXXXXX")"
trap 'rm -f "${tmp_file}"' EXIT
awk \
	-v controller="${refs[controller]}" \
	-v codex="${refs[codex]}" \
	-v opencode="${refs[opencode]}" \
	-v hermes="${refs[hermes]}" \
	-v openclaw="${refs[openclaw]}" \
	-v grokbuild="${refs[grok-build]}" \
	-v pi="${refs[pi]}" '
BEGIN {
	in_image = 0
	in_runners = 0
	found_controller = 0
	found_codex = 0
	found_opencode = 0
	found_hermes = 0
	found_openclaw = 0
	found_grokbuild = 0
	found_pi = 0
}
/^image:$/ {
	in_image = 1
	print
	next
}
in_image && /^  reference:/ {
	print "  reference: " controller
	found_controller = 1
	in_image = 0
	next
}
/^runnerImages:$/ {
	in_runners = 1
	print
	next
}
in_runners && /^  codex:/ {
	print "  codex: " codex
	found_codex = 1
	next
}
in_runners && /^  openCode:/ {
	print "  openCode: " opencode
	found_opencode = 1
	next
}
in_runners && /^  hermesAgent:/ {
	print "  hermesAgent: " hermes
	found_hermes = 1
	next
}
in_runners && /^  openClaw:/ {
	print "  openClaw: " openclaw
	found_openclaw = 1
	next
}
in_runners && /^  grokBuild:/ {
	print "  grokBuild: " grokbuild
	found_grokbuild = 1
	next
}
in_runners && /^  piAgent:/ {
	print "  piAgent: " pi
	found_pi = 1
	next
}
in_runners && /^[^[:space:]]/ {
	in_runners = 0
}
{
	print
}
END {
	if (!found_controller || !found_codex || !found_opencode || !found_hermes || !found_openclaw || !found_grokbuild || !found_pi) {
		exit 3
	}
}
' "${values_file}" > "${tmp_file}" || {
	status=$?
	if [[ "${status}" -eq 3 ]]; then
		echo "values file is missing one or more expected image fields" >&2
	else
		echo "failed to update values file" >&2
	fi
	exit "${status}"
}
mv "${tmp_file}" "${values_file}"
trap - EXIT
printf 'Pinned %s to %s\n' "${values_file}" "${image_lock}"
