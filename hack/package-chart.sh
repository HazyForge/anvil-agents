#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=""
output_dir="${repo_root}/dist"
image_prefix="${ANVIL_AGENTS_IMAGE_PREFIX:-ghcr.io/hazyforge}"

usage() {
	cat <<'EOF'
Package a version-coupled anvil-agents Helm chart locally.

Usage:
  ./hack/package-chart.sh --version VERSION [--output DIR] [--image-prefix PREFIX]

Options:
  --version VERSION  SemVer with or without a leading v, for example 0.1.1.
  --output DIR       Package destination. Default: ./dist.
  --image-prefix     Registry/repository prefix for all seven images.
                     Default: ghcr.io/hazyforge.
  -h, --help         Show this help.

The packaged chart uses vVERSION for the controller and all built-in runner
image defaults. This script does not push the chart or require GitHub Actions.
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

mkdir -p "${output_dir}"
helm lint --strict "${tmp_dir}/anvil-agents" >/dev/null
helm package "${tmp_dir}/anvil-agents" \
	--version "${version}" \
	--app-version "v${version}" \
	--destination "${output_dir}"
