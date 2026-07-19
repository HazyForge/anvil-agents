#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=""
output_dir="${repo_root}/dist"
image_prefix="${ANVIL_AGENTS_IMAGE_PREFIX:-ghcr.io/hazyforge}"
image_lock=""
source_revision=""

usage() {
	cat <<'EOF'
Package a version-coupled anvil-agents Helm chart locally.

Usage:
  ./hack/package-chart.sh --version VERSION [--output DIR] [--image-prefix PREFIX] [--image-lock FILE] [--source-revision REVISION]

Options:
  --version VERSION  SemVer with or without a leading v, for example 0.1.1.
  --output DIR       Package destination. Default: ./dist.
  --image-prefix     Registry/repository prefix for all seven images.
                     Default: ghcr.io/hazyforge.
  --image-lock FILE  Seven-image lock produced by publish-images.sh. When set,
                     the packaged chart uses its immutable digest references.
  --source-revision  Expected 40- or 64-character source commit for the image
                     lock. Required when the matching local version tag is not
                     available.
  -h, --help         Show this help.

Without --image-lock, the packaged chart uses vVERSION for the controller and
all built-in runner defaults. Release automation supplies --image-lock so
published charts are immutable. This script does not push the chart or require
GitHub Actions.
EOF
}

require_value() {
	local option="$1"
	local value="${2:-}"
	if [[ -z "${value}" || "${value}" == --* ]]; then
		echo "${option} requires a value" >&2
		exit 2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--version)
			require_value "$1" "${2:-}"
			version="${2:-}"
			shift 2
			;;
		--output)
			require_value "$1" "${2:-}"
			output_dir="${2:-}"
			shift 2
			;;
		--image-prefix)
			require_value "$1" "${2:-}"
			image_prefix="${2:-}"
			shift 2
			;;
		--image-lock)
			require_value "$1" "${2:-}"
			image_lock="${2:-}"
			shift 2
			;;
		--source-revision)
			require_value "$1" "${2:-}"
			source_revision="${2:-}"
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

version="${version#v}"
if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
	echo "--version must be a SemVer value" >&2
	exit 2
fi
command -v helm >/dev/null 2>&1 || {
	echo "helm is required" >&2
	exit 1
}
if [[ -z "${image_prefix}" || ! "${image_prefix}" =~ ^[A-Za-z0-9._:/-]+$ ]]; then
	echo "--image-prefix must be a non-empty registry path" >&2
	exit 2
fi
image_prefix="${image_prefix%/}"

declare -A locked_refs=()
if [[ -n "${image_lock}" ]]; then
	[[ -f "${image_lock}" ]] || { echo "image lock does not exist: ${image_lock}" >&2; exit 2; }
	lock_schema=""
	lock_revision=""
	lock_platform=""
	while IFS=$'\t' read -r key value extra; do
		[[ -z "${extra:-}" ]] || { echo "invalid image lock row for ${key}" >&2; exit 2; }
		case "${key}" in
			schema)
				[[ -z "${lock_schema}" ]] || { echo "duplicate image lock schema" >&2; exit 2; }
				lock_schema="${value}"
				;;
			source-revision)
				[[ -z "${lock_revision}" ]] || { echo "duplicate image lock source revision" >&2; exit 2; }
				lock_revision="${value}"
				;;
			platform)
				[[ -z "${lock_platform}" ]] || { echo "duplicate image lock platform" >&2; exit 2; }
				lock_platform="${value}"
				;;
			"") echo "image lock contains an empty row" >&2; exit 2 ;;
			controller|codex|opencode|grok-build|hermes|openclaw|pi)
				[[ -z "${locked_refs[${key}]:-}" ]] || { echo "duplicate image lock component: ${key}" >&2; exit 2; }
				locked_refs["${key}"]="${value}"
				;;
			*) echo "unknown image lock field: ${key}" >&2; exit 2 ;;
		esac
	done < "${image_lock}"
	[[ "${lock_schema}" == "anvil-agents-image-lock/v1" ]] || { echo "image lock is missing or has an unsupported schema" >&2; exit 2; }
	[[ "${lock_revision}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || { echo "image lock has an invalid source revision" >&2; exit 2; }
	[[ "${lock_platform}" =~ ^[a-z0-9]+/[a-z0-9][a-z0-9._-]*$ ]] || { echo "image lock has an invalid platform" >&2; exit 2; }
	tag_revision=""
	if command -v git >/dev/null 2>&1; then
		tag_revision="$(git -C "${repo_root}" rev-parse -q --verify "refs/tags/v${version}^{commit}" 2>/dev/null || true)"
	fi
	if [[ -n "${source_revision}" && ! "${source_revision}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]]; then
		echo "--source-revision must be a 40- or 64-character lowercase hexadecimal commit" >&2
		exit 2
	fi
	if [[ -n "${tag_revision}" && -n "${source_revision}" && "${tag_revision}" != "${source_revision}" ]]; then
		echo "source revision ${source_revision} does not match version tag v${version} at ${tag_revision}" >&2
		exit 2
	fi
	expected_revision="${tag_revision:-${source_revision}}"
	[[ -n "${expected_revision}" ]] || { echo "--source-revision is required when version tag v${version} is unavailable" >&2; exit 2; }
	[[ "${lock_revision}" == "${expected_revision}" ]] || { echo "image lock source revision ${lock_revision} does not match expected ${expected_revision}" >&2; exit 2; }
	declare -A image_names=(
		[controller]=anvil-agents
		[codex]=anvil-agent-run-codex
		[opencode]=anvil-agent-run-opencode
		[grok-build]=anvil-agent-run-grok-build
		[hermes]=anvil-agent-run-hermes
		[openclaw]=anvil-agent-run-openclaw
		[pi]=anvil-agent-run-pi
	)
	for component in controller codex opencode grok-build hermes openclaw pi; do
		ref="${locked_refs[${component}]:-}"
		[[ "${ref}" == "${image_prefix}/${image_names[${component}]}"@sha256:* ]] || {
			echo "image lock component ${component} does not match ${image_prefix}/${image_names[${component}]}" >&2
			exit 2
		}
		digest="${ref##*@sha256:}"
		[[ "${digest}" =~ ^[0-9a-f]{64}$ ]] || { echo "image lock component ${component} has an invalid digest" >&2; exit 2; }
	done
elif [[ -n "${source_revision}" ]]; then
	echo "--source-revision requires --image-lock" >&2
	exit 2
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
cp -R "${repo_root}/charts/anvil-agents" "${tmp_dir}/anvil-agents"
sed -i \
	-e "s#repository: anvil-agents#repository: ${image_prefix}/anvil-agents#" \
	-e "s#tag: dev#tag: v${version}#" \
	-e "s#codex: anvil-agent-run-codex:dev#codex: ${image_prefix}/anvil-agent-run-codex:v${version}#" \
	-e "s#openCode: anvil-agent-run-opencode:dev#openCode: ${image_prefix}/anvil-agent-run-opencode:v${version}#" \
	-e "s#hermesAgent: anvil-agent-run-hermes:dev#hermesAgent: ${image_prefix}/anvil-agent-run-hermes:v${version}#" \
	-e "s#openClaw: anvil-agent-run-openclaw:dev#openClaw: ${image_prefix}/anvil-agent-run-openclaw:v${version}#" \
	-e "s#grokBuild: anvil-agent-run-grok-build:dev#grokBuild: ${image_prefix}/anvil-agent-run-grok-build:v${version}#" \
	-e "s#piAgent: anvil-agent-run-pi:dev#piAgent: ${image_prefix}/anvil-agent-run-pi:v${version}#" \
	"${tmp_dir}/anvil-agents/values.yaml"

if [[ -n "${image_lock}" ]]; then
	sed -i \
		-e "/^image:$/,/^[^ ]/ s#^  reference: \"\"#  reference: ${locked_refs[controller]}#" \
		-e "s#codex: ${image_prefix}/anvil-agent-run-codex:v${version}#codex: ${locked_refs[codex]}#" \
		-e "s#openCode: ${image_prefix}/anvil-agent-run-opencode:v${version}#openCode: ${locked_refs[opencode]}#" \
		-e "s#hermesAgent: ${image_prefix}/anvil-agent-run-hermes:v${version}#hermesAgent: ${locked_refs[hermes]}#" \
		-e "s#openClaw: ${image_prefix}/anvil-agent-run-openclaw:v${version}#openClaw: ${locked_refs[openclaw]}#" \
		-e "s#grokBuild: ${image_prefix}/anvil-agent-run-grok-build:v${version}#grokBuild: ${locked_refs[grok-build]}#" \
		-e "s#piAgent: ${image_prefix}/anvil-agent-run-pi:v${version}#piAgent: ${locked_refs[pi]}#" \
		"${tmp_dir}/anvil-agents/values.yaml"
fi

mkdir -p "${output_dir}"
helm lint --strict "${tmp_dir}/anvil-agents" >/dev/null
helm package "${tmp_dir}/anvil-agents" \
	--version "${version}" \
	--app-version "v${version}" \
	--destination "${output_dir}"
