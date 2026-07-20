#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=""
push_tag="false"
remote="origin"

usage() {
	cat <<'EOF'
Create or verify the local release tag used by local publication.

Usage:
  ./hack/ensure-release-tag.sh --version vX.Y.Z [options]

Options:
  --version VERSION  Git release tag, for example v0.1.2.
  --push             Push the tag to the selected remote after verifying it.
  --remote REMOTE    Git remote used by --push. Default: origin.
  -h, --help         Show this help.

The script refuses dirty worktrees, accepts an existing tag only when it points
at HEAD, and creates an annotated tag otherwise. Use --push only when a remote
tag is needed, for example before creating a public GitHub release.
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
		--version)
			require_value "$1" "${2:-}"
			version="$2"
			shift 2
			;;
		--push)
			push_tag="true"
			shift
			;;
		--remote)
			require_value "$1" "${2:-}"
			remote="$2"
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

[[ -n "${version}" ]] || { echo "--version is required" >&2; exit 2; }
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]] || {
	echo "--version must be a v-prefixed SemVer value" >&2
	exit 2
}
command -v git >/dev/null 2>&1 || { echo "git is required" >&2; exit 1; }
[[ -z "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]] || {
	echo "refusing to tag a dirty worktree" >&2
	exit 2
}

head_revision="$(git -C "${repo_root}" rev-parse HEAD)"
tag_revision="$(git -C "${repo_root}" rev-parse -q --verify "refs/tags/${version}^{commit}" 2>/dev/null || true)"
if [[ -n "${tag_revision}" ]]; then
	[[ "${tag_revision}" == "${head_revision}" ]] || {
		echo "tag ${version} points at ${tag_revision}, not HEAD ${head_revision}" >&2
		exit 2
	}
else
	git -C "${repo_root}" tag -a "${version}" -m "anvil-agents ${version}"
fi

if [[ "${push_tag}" == "true" ]]; then
	git -C "${repo_root}" push "${remote}" "${version}"
fi

printf 'Release tag %s verified at %s\n' "${version}" "${head_revision}"
