#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image_prefix="${ANVIL_AGENTS_IMAGE_PREFIX:-}"
platform="${ANVIL_AGENTS_IMAGE_PLATFORM:-}"
source_url="${ANVIL_AGENTS_IMAGE_SOURCE:-}"
mode="load"
no_cache="false"
pull="false"
allow_dirty="false"
components=()
tags=()
cache_from=()
cache_to=()

all_components=(controller codex grok-build hermes openclaw pi)

usage() {
	cat <<'EOF'
Build the anvil-agents controller and runner images without GitHub Actions.

Usage:
  ./hack/build-images.sh [options]

Options:
  --component NAME       Build one component; repeatable. Default: all.
                         Names: controller, codex, grok-build, hermes,
                         openclaw, pi, all.
  --prefix PREFIX        Image repository prefix, for example
                         ghcr.io/hazyforge. Default: local Docker names.
  --tag TAG              Image tag; repeatable. Default: dev.
  --platform PLATFORM    Docker platform, for example linux/amd64.
  --source-url URL       OCI source label. Default: the Git origin URL.
  --load                 Build into the local Docker image store (default).
  --push                 Push with docker buildx; requires --prefix.
  --allow-dirty          Permit --push from a dirty Git worktree.
  --check                Validate Dockerfiles without building images.
  --cache-from VALUE     Pass a buildx cache source; repeatable.
  --cache-to VALUE       Pass a buildx cache destination; repeatable.
  --no-cache             Disable build cache.
  --pull                 Always attempt to pull newer base images.
  --list                 List component, image, and Dockerfile mappings.
  -h, --help             Show this help.

Environment defaults:
  ANVIL_AGENTS_IMAGE_PREFIX
  ANVIL_AGENTS_IMAGE_TAG
  ANVIL_AGENTS_IMAGE_PLATFORM
  ANVIL_AGENTS_IMAGE_SOURCE

Examples:
  ./hack/build-images.sh
  ./hack/build-images.sh --component controller --tag test
  ./hack/build-images.sh --prefix registry.example.com/team --tag dev
  ./hack/build-images.sh --prefix ghcr.io/hazyforge --tag sha-abc1234 --push
EOF
}

image_name() {
	case "$1" in
		controller) printf '%s\n' "anvil-agents" ;;
		codex) printf '%s\n' "anvil-agent-run-codex" ;;
		grok-build) printf '%s\n' "anvil-agent-run-grok-build" ;;
		hermes) printf '%s\n' "anvil-agent-run-hermes" ;;
		openclaw) printf '%s\n' "anvil-agent-run-openclaw" ;;
		pi) printf '%s\n' "anvil-agent-run-pi" ;;
		*) return 1 ;;
	esac
}

dockerfile_path() {
	case "$1" in
		controller) printf '%s\n' "Dockerfile" ;;
		codex) printf '%s\n' "docker/agent-run-codex/Dockerfile" ;;
		grok-build) printf '%s\n' "docker/agent-run-grok-build/Dockerfile" ;;
		hermes) printf '%s\n' "docker/agent-run-hermes/Dockerfile" ;;
		openclaw) printf '%s\n' "docker/agent-run-openclaw/Dockerfile" ;;
		pi) printf '%s\n' "docker/agent-run-pi/Dockerfile" ;;
		*) return 1 ;;
	esac
}

list_components() {
	local component
	for component in "${all_components[@]}"; do
		printf '%-12s %-30s %s\n' \
			"${component}" "$(image_name "${component}")" "$(dockerfile_path "${component}")"
	done
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
		--component)
			require_value "$1" "${2:-}"
			components+=("$2")
			shift 2
			;;
		--prefix)
			require_value "$1" "${2:-}"
			image_prefix="${2%/}"
			shift 2
			;;
		--tag)
			require_value "$1" "${2:-}"
			tags+=("$2")
			shift 2
			;;
		--platform)
			require_value "$1" "${2:-}"
			platform="$2"
			shift 2
			;;
		--source-url)
			require_value "$1" "${2:-}"
			source_url="$2"
			shift 2
			;;
		--load)
			mode="load"
			shift
			;;
		--push)
			mode="push"
			shift
			;;
		--allow-dirty)
			allow_dirty="true"
			shift
			;;
		--check)
			mode="check"
			shift
			;;
		--cache-from)
			require_value "$1" "${2:-}"
			cache_from+=("$2")
			shift 2
			;;
		--cache-to)
			require_value "$1" "${2:-}"
			cache_to+=("$2")
			shift 2
			;;
		--no-cache)
			no_cache="true"
			shift
			;;
		--pull)
			pull="true"
			shift
			;;
		--list)
			list_components
			exit 0
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

if [[ ${#components[@]} -eq 0 ]]; then
	components=("${all_components[@]}")
fi
if [[ ${#tags[@]} -eq 0 ]]; then
	tags=("${ANVIL_AGENTS_IMAGE_TAG:-dev}")
fi
if [[ "${mode}" == "push" && -z "${image_prefix}" ]]; then
	echo "--push requires --prefix or ANVIL_AGENTS_IMAGE_PREFIX" >&2
	exit 2
fi
if [[ "${mode}" == "push" && "${allow_dirty}" != "true" ]] &&
	[[ -n "$(git -C "${repo_root}" status --porcelain --untracked-files=normal)" ]]; then
	echo "refusing to push images from a dirty worktree; commit changes or pass --allow-dirty" >&2
	exit 2
fi
if [[ "${mode}" != "push" && ${#cache_to[@]} -gt 0 ]]; then
	echo "--cache-to requires --push" >&2
	exit 2
fi

selected_components=()
for component in "${components[@]}"; do
	if [[ "${component}" == "all" ]]; then
		selected_components=("${all_components[@]}")
		break
	fi
	if ! image_name "${component}" >/dev/null; then
		echo "unknown component: ${component}" >&2
		list_components >&2
		exit 2
	fi
	selected_components+=("${component}")
done

command -v docker >/dev/null 2>&1 || {
	echo "docker is required" >&2
	exit 1
}
if [[ "${mode}" == "push" ]]; then
	docker buildx version >/dev/null
fi

revision="$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null || printf 'unknown')"
if [[ -z "${source_url}" ]]; then
	source_url="$(git -C "${repo_root}" remote get-url origin 2>/dev/null || true)"
fi
source_url="${source_url:-https://github.com/HazyForge/anvil-agents}"
if [[ "${source_url}" =~ ^git@([^:]+):(.+)$ ]]; then
	source_url="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]%.git}"
fi
built_refs=()
component_index=0

for component in "${selected_components[@]}"; do
	component_index=$((component_index + 1))
	name="$(image_name "${component}")"
	dockerfile="$(dockerfile_path "${component}")"

	if [[ "${mode}" == "check" ]]; then
		printf '[%d/%d] Checking %s (%s)\n' \
			"${component_index}" "${#selected_components[@]}" "${component}" "${dockerfile}"
		docker build --check --file "${repo_root}/${dockerfile}" "${repo_root}"
		continue
	fi

	if [[ "${mode}" == "push" ]]; then
		build_command=(docker buildx build --push)
	else
		build_command=(docker build)
	fi
	build_command+=(--file "${repo_root}/${dockerfile}")
	if [[ -n "${platform}" ]]; then
		build_command+=(--platform "${platform}")
	fi
	if [[ "${no_cache}" == "true" ]]; then
		build_command+=(--no-cache)
	fi
	if [[ "${pull}" == "true" ]]; then
		build_command+=(--pull)
	fi
	for value in "${cache_from[@]}"; do
		build_command+=(--cache-from "${value}")
	done
	for value in "${cache_to[@]}"; do
		build_command+=(--cache-to "${value}")
	done
	build_command+=(
		--label "org.opencontainers.image.source=${source_url}"
		--label "org.opencontainers.image.revision=${revision}"
	)

	component_refs=()
	for tag in "${tags[@]}"; do
		if [[ -n "${image_prefix}" ]]; then
			ref="${image_prefix}/${name}:${tag}"
		else
			ref="${name}:${tag}"
		fi
		build_command+=(--tag "${ref}")
		component_refs+=("${ref}")
		built_refs+=("${ref}")
	done

	printf '[%d/%d] Building %s as %s\n' \
		"${component_index}" "${#selected_components[@]}" "${component}" "${component_refs[*]}"
	"${build_command[@]}" "${repo_root}"
done

if [[ "${mode}" != "check" ]]; then
	printf '\n%s images:\n' "${mode}ed"
	printf '  %s\n' "${built_refs[@]}"
fi
