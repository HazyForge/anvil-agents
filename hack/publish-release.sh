#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
prefix=""
version=""
platform="linux/amd64"
output_dir="${repo_root}/dist"
chart_registry=""

usage() {
	cat <<'EOF'
Publish a complete Anvil Agents release without GitHub Actions.

Usage:
  ./hack/publish-release.sh --prefix REGISTRY/PATH --version vX.Y.Z [options]

Options:
  --prefix PREFIX          Image registry prefix, for example ghcr.io/hazyforge.
  --version VERSION        Git tag at HEAD and release version, for example v0.1.1.
  --platform PLATFORM      Image platform. Default: linux/amd64.
  --output DIR             Chart and digest-lock directory. Default: ./dist.
  --chart-registry URL     OCI chart destination. Default: oci://PREFIX/charts.
  -h, --help               Show this help.

Log in to the image registry with Docker and Helm before running this script.
The script publishes and verifies all six images, writes an immutable digest
lock, packages the version-coupled chart, and pushes the chart to OCI.
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
		--prefix)
			require_value "$1" "${2:-}"
			prefix="${2%/}"
			shift 2
			;;
		--version)
			require_value "$1" "${2:-}"
			version="$2"
			shift 2
			;;
		--platform)
			require_value "$1" "${2:-}"
			platform="$2"
			shift 2
			;;
		--output)
			require_value "$1" "${2:-}"
			output_dir="$2"
			shift 2
			;;
		--chart-registry)
			require_value "$1" "${2:-}"
			chart_registry="${2%/}"
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

[[ -n "${prefix}" ]] || { echo "--prefix is required" >&2; exit 2; }
[[ -n "${version}" ]] || { echo "--version is required" >&2; exit 2; }
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || {
	echo "--version must be a v-prefixed SemVer value" >&2
	exit 2
}
chart_registry="${chart_registry:-oci://${prefix}/charts}"
[[ "${chart_registry}" == oci://* ]] || { echo "--chart-registry must use oci://" >&2; exit 2; }
command -v helm >/dev/null 2>&1 || { echo "helm is required" >&2; exit 1; }

mkdir -p "${output_dir}"
lock="${output_dir}/images-${version}.lock.tsv"
chart_version="${version#v}"
chart="${output_dir}/anvil-agents-${chart_version}.tgz"

"${repo_root}/hack/publish-images.sh" \
	--prefix "${prefix}" \
	--version "${version}" \
	--platform "${platform}" \
	--output "${lock}"

"${repo_root}/hack/package-chart.sh" \
	--version "${version}" \
	--output "${output_dir}" \
	--image-prefix "${prefix}"

helm push "${chart}" "${chart_registry}"
"${repo_root}/hack/publish-images.sh" --verify-lock "${lock}"

printf 'Published Anvil Agents %s\nChart: %s\nImage lock: %s\n' \
	"${version}" "${chart_registry}" "${lock}"
