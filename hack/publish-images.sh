#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
prefix=""
version=""
platform="linux/amd64"
output=""
verify_lock=""

components=(controller codex grok-build hermes openclaw pi)

usage() {
	cat <<'EOF'
Publish and verify one immutable six-image Anvil Agents release.

Usage:
  ./hack/publish-images.sh --prefix REGISTRY/PATH --version vX.Y.Z [options]
  ./hack/publish-images.sh --verify-lock FILE

Options:
  --prefix PREFIX      Registry repository prefix, for example ghcr.io/hazyforge.
  --version VERSION    Git tag at HEAD and image version tag, for example v0.1.0.
  --platform PLATFORM  Image platform used for revision verification. Default: linux/amd64.
  --output FILE        Digest lock output. Default: dist/images-VERSION.lock.tsv.
  --verify-lock FILE   Verify an existing lock without building or pushing.
  -h, --help           Show this help.

The script uses Docker Buildx and an existing `docker login`; it does not
require GitHub Actions, gh, crane, jq, or Helm.
EOF
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

image_name() {
	case "$1" in
		controller) printf '%s\n' anvil-agents ;;
		codex) printf '%s\n' anvil-agent-run-codex ;;
		grok-build) printf '%s\n' anvil-agent-run-grok-build ;;
		hermes) printf '%s\n' anvil-agent-run-hermes ;;
		openclaw) printf '%s\n' anvil-agent-run-openclaw ;;
		pi) printf '%s\n' anvil-agent-run-pi ;;
		*) return 1 ;;
	esac
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
			output="$2"
			shift 2
			;;
		--verify-lock)
			require_value "$1" "${2:-}"
			verify_lock="$2"
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

command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker buildx version >/dev/null

declare -A locked_refs=()
lock_revision=""
lock_platform=""

read_lock() {
	local file="$1" key value
	[[ -f "${file}" ]] || { echo "image lock does not exist: ${file}" >&2; exit 2; }
	while IFS=$'\t' read -r key value extra; do
		[[ -z "${extra:-}" ]] || { echo "invalid image lock row for ${key}" >&2; exit 2; }
		case "${key}" in
			schema)
				[[ "${value}" == "anvil-agents-image-lock/v1" ]] || { echo "unsupported image lock schema: ${value}" >&2; exit 2; }
				;;
			source-revision) lock_revision="${value}" ;;
			platform) lock_platform="${value}" ;;
			controller|codex|grok-build|hermes|openclaw|pi)
				[[ -z "${locked_refs[${key}]:-}" ]] || { echo "duplicate image lock component: ${key}" >&2; exit 2; }
				locked_refs["${key}"]="${value}"
				;;
			"") ;;
			*) echo "unknown image lock field: ${key}" >&2; exit 2 ;;
		esac
	done < "${file}"
	[[ -n "${lock_revision}" && -n "${lock_platform}" ]] || { echo "image lock is missing revision or platform" >&2; exit 2; }
	for key in "${components[@]}"; do
		[[ -n "${locked_refs[${key}]:-}" ]] || { echo "image lock is missing component: ${key}" >&2; exit 2; }
	done
}

inspect_digest() {
	docker buildx imagetools inspect "$1" --format '{{.Manifest.Digest}}'
}

inspect_revision() {
	local ref="$1" target_platform="$2"
	docker buildx imagetools inspect "${ref}" \
		--format "{{index (index .Image \"${target_platform}\").Config.Labels \"org.opencontainers.image.revision\"}}"
}

verify_immutable_ref() {
	local component="$1" ref="$2" expected_revision="$3" target_platform="$4"
	[[ "${ref}" == *@sha256:* ]] || { echo "${component} is not digest-pinned: ${ref}" >&2; exit 1; }
	local expected_digest="${ref##*@}" actual_digest actual_revision
	actual_digest="$(inspect_digest "${ref}")"
	[[ "${actual_digest}" == "${expected_digest}" ]] || { echo "${component} digest mismatch: ${actual_digest} != ${expected_digest}" >&2; exit 1; }
	actual_revision="$(inspect_revision "${ref}" "${target_platform}")"
	[[ "${actual_revision}" == "${expected_revision}" ]] || { echo "${component} revision mismatch: ${actual_revision} != ${expected_revision}" >&2; exit 1; }
}

if [[ -n "${verify_lock}" ]]; then
	[[ -z "${prefix}" && -z "${version}" && -z "${output}" ]] || { echo "--verify-lock cannot be combined with publication options" >&2; exit 2; }
	read_lock "${verify_lock}"
	for component in "${components[@]}"; do
		verify_immutable_ref "${component}" "${locked_refs[${component}]}" "${lock_revision}" "${lock_platform}"
	done
	printf 'Verified six-image lock %s at revision %s\n' "${verify_lock}" "${lock_revision}"
	exit 0
fi

[[ -n "${prefix}" ]] || { echo "--prefix is required" >&2; exit 2; }
[[ -n "${version}" ]] || { echo "--version is required" >&2; exit 2; }
[[ -z "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]] || { echo "refusing to publish from a dirty worktree" >&2; exit 2; }

revision="$(git -C "${repo_root}" rev-parse HEAD)"
tag_revision="$(git -C "${repo_root}" rev-parse -q --verify "refs/tags/${version}^{commit}" 2>/dev/null || true)"
[[ "${tag_revision}" == "${revision}" ]] || { echo "version tag ${version} must point at HEAD ${revision}" >&2; exit 2; }
short_revision="$(git -C "${repo_root}" rev-parse --short=7 HEAD)"
sha_tag="sha-${short_revision}"
output="${output:-${repo_root}/dist/images-${version}.lock.tsv}"

"${repo_root}/hack/build-images.sh" \
	--prefix "${prefix}" \
	--tag "${version}" \
	--tag "${sha_tag}" \
	--platform "${platform}" \
	--push

mkdir -p "$(dirname "${output}")"
tmp_lock="$(mktemp "${output}.tmp.XXXXXX")"
trap 'rm -f "${tmp_lock:-}"' EXIT
{
	printf 'schema\tanvil-agents-image-lock/v1\n'
	printf 'source-revision\t%s\n' "${revision}"
	printf 'platform\t%s\n' "${platform}"
	for component in "${components[@]}"; do
		repository="${prefix}/$(image_name "${component}")"
		version_digest="$(inspect_digest "${repository}:${version}")"
		sha_digest="$(inspect_digest "${repository}:${sha_tag}")"
		[[ "${version_digest}" == "${sha_digest}" ]] || { echo "${component} tags resolve to different digests" >&2; exit 1; }
		immutable_ref="${repository}@${version_digest}"
		verify_immutable_ref "${component}" "${immutable_ref}" "${revision}" "${platform}"
		printf '%s\t%s\n' "${component}" "${immutable_ref}"
	done
} > "${tmp_lock}"
mv "${tmp_lock}" "${output}"
trap - EXIT
printf 'Published and verified six-image lock %s\n' "${output}"
