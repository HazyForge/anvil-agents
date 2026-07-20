#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=""
repo=""
output_dir="${repo_root}/dist"
chart_registry=""
title=""
notes_file=""
latest="true"
prerelease="false"
update_existing="false"

usage() {
	cat <<'EOF'
Create a GitHub release from locally published Anvil Agents artifacts.

Usage:
  ./hack/create-github-release.sh --version vX.Y.Z [options]

Options:
  --version VERSION       Git release tag, for example v0.1.2.
  --repo OWNER/REPO       GitHub repository. Default: inferred from origin.
  --output DIR            Directory containing local release artifacts.
                          Default: ./dist.
  --chart-registry URL    OCI chart registry used in release notes, for example
                          oci://ghcr.io/hazyforge/charts.
  --title TITLE           Release title. Default: anvil-agents VERSION.
  --notes-file FILE       Use a custom release notes file.
  --latest=false          Do not mark this release as latest.
  --prerelease            Mark the release as a prerelease.
  --update-existing       Edit notes and clobber matching assets if the release
                          already exists. This deletes matching remote assets
                          before re-uploading them.
  -h, --help              Show this help.

This uses the GitHub API through gh, but does not use GitHub Actions minutes.
Run hack/publish-release.sh first so the chart package and image lock exist.
The release tag must already exist on GitHub; use make release-tag-push or
make release-local-all before this helper.
EOF
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

infer_repo() {
	local origin
	origin="$(git -C "${repo_root}" remote get-url origin 2>/dev/null || true)"
	case "${origin}" in
		git@github.com:*)
			origin="${origin#git@github.com:}"
			printf '%s\n' "${origin%.git}"
			;;
		https://github.com/*)
			origin="${origin#https://github.com/}"
			printf '%s\n' "${origin%.git}"
			;;
		*)
			return 1
			;;
	esac
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--version)
			require_value "$1" "${2:-}"
			version="$2"
			shift 2
			;;
		--repo)
			require_value "$1" "${2:-}"
			repo="$2"
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
		--title)
			require_value "$1" "${2:-}"
			title="$2"
			shift 2
			;;
		--notes-file)
			require_value "$1" "${2:-}"
			notes_file="$2"
			shift 2
			;;
		--latest=false)
			latest="false"
			shift
			;;
		--prerelease)
			prerelease="true"
			shift
			;;
		--update-existing)
			update_existing="true"
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

[[ -n "${version}" ]] || { echo "--version is required" >&2; exit 2; }
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || {
	echo "--version must be a v-prefixed SemVer value" >&2
	exit 2
}
command -v gh >/dev/null 2>&1 || { echo "gh is required" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "sha256sum is required" >&2; exit 1; }

repo="${repo:-$(infer_repo || true)}"
[[ -n "${repo}" ]] || { echo "--repo is required when origin is not a GitHub repository" >&2; exit 2; }
title="${title:-anvil-agents ${version}}"
chart_version="${version#v}"
chart="${output_dir}/anvil-agents-${chart_version}.tgz"
lock="${output_dir}/images-${version}.lock.tsv"
[[ -f "${chart}" ]] || { echo "chart package does not exist: ${chart}" >&2; exit 2; }
[[ -f "${lock}" ]] || { echo "image lock does not exist: ${lock}" >&2; exit 2; }

lock_schema=""
lock_revision=""
lock_platform=""
declare -A locked_refs=()
while IFS=$'\t' read -r key value extra; do
	[[ -z "${extra:-}" ]] || { echo "invalid image lock row for ${key}" >&2; exit 2; }
	case "${key}" in
		schema) lock_schema="${value}" ;;
		source-revision) lock_revision="${value}" ;;
		platform) lock_platform="${value}" ;;
		controller|codex|opencode|grok-build|hermes|openclaw|pi) locked_refs["${key}"]="${value}" ;;
	esac
done < "${lock}"
[[ "${lock_schema}" == "anvil-agents-image-lock/v1" ]] || { echo "image lock has an unsupported schema" >&2; exit 2; }
[[ "${lock_revision}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || { echo "image lock has an invalid source revision" >&2; exit 2; }
[[ -n "${lock_platform}" ]] || { echo "image lock is missing platform" >&2; exit 2; }
for component in controller codex opencode grok-build hermes openclaw pi; do
	[[ -n "${locked_refs[${component}]:-}" ]] || { echo "image lock is missing component: ${component}" >&2; exit 2; }
	[[ "${locked_refs[${component}]}" == *@sha256:* ]] || { echo "${component} is not digest-pinned" >&2; exit 2; }
done

tag_revision="$(git -C "${repo_root}" rev-parse -q --verify "refs/tags/${version}^{commit}" 2>/dev/null || true)"
if [[ -n "${tag_revision}" && "${tag_revision}" != "${lock_revision}" ]]; then
	echo "tag ${version} points at ${tag_revision}, not lock revision ${lock_revision}" >&2
	exit 2
fi

chart_sha="$(sha256sum "${chart}" | awk '{print $1}')"
lock_sha="$(sha256sum "${lock}" | awk '{print $1}')"
generated_notes=""
if [[ -z "${notes_file}" ]]; then
	generated_notes="$(mktemp)"
	trap 'rm -f "${generated_notes}"' EXIT
	{
		printf 'Release %s from commit `%s`.\n\n' "${version}" "${lock_revision}"
		if [[ -n "${chart_registry}" ]]; then
			printf 'Published OCI chart:\n`%s/anvil-agents:%s`\n\n' "${chart_registry}" "${chart_version}"
		fi
		printf 'Assets:\n\n'
		printf -- '- `%s` sha256:%s\n' "$(basename "${chart}")" "${chart_sha}"
		printf -- '- `%s` sha256:%s\n\n' "$(basename "${lock}")" "${lock_sha}"
		printf 'Verification expected before creating this release:\n\n'
		printf -- '- `make verify`\n'
		printf -- '- `make kind-e2e`\n'
		printf -- '- `hack/publish-release.sh --prefix ... --version %s`\n' "${version}"
		printf -- '- `hack/publish-images.sh --verify-lock %s`\n' "${lock}"
	} > "${generated_notes}"
	notes_file="${generated_notes}"
fi
[[ -f "${notes_file}" ]] || { echo "release notes file does not exist: ${notes_file}" >&2; exit 2; }

release_flags=(--repo "${repo}" --title "${title}" --notes-file "${notes_file}" --target "${lock_revision}" --verify-tag)
if [[ "${latest}" == "true" ]]; then
	release_flags+=(--latest)
else
	release_flags+=(--latest=false)
fi
if [[ "${prerelease}" == "true" ]]; then
	release_flags+=(--prerelease)
fi

if gh release view "${version}" --repo "${repo}" >/dev/null 2>&1; then
	[[ "${update_existing}" == "true" ]] || {
		echo "release ${version} already exists; pass --update-existing to replace matching assets" >&2
		exit 2
	}
	edit_flags=(--repo "${repo}" --title "${title}" --notes-file "${notes_file}" --target "${lock_revision}" --verify-tag)
	if [[ "${latest}" == "true" ]]; then
		edit_flags+=(--latest)
	fi
	if [[ "${prerelease}" == "true" ]]; then
		edit_flags+=(--prerelease)
	fi
	gh release edit "${version}" "${edit_flags[@]}"
	gh release upload "${version}" "${chart}" "${lock}" --repo "${repo}" --clobber
else
	gh release create "${version}" "${chart}" "${lock}" "${release_flags[@]}"
fi

printf 'GitHub release %s published for %s\n' "${version}" "${repo}"
