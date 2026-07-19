#!/usr/bin/env bash
set -euo pipefail

kind_version=v0.27.0
kind_sha256=a6875aaea358acf0ac07786b1a6755d08fd640f4c79b7a2e46681cc13f49a04b
kubectl_version=v1.32.2
kubectl_sha256=4f6a959dcc5b702135f8354cc7109b542a2933c46b808b248a214c1f69f817ea
helm_version=v3.21.2
helm_sha256=0a745198de24545d0055cd8414bc8d2ba10363ef5f5d38369ea1b399671cc083
helm_binary_sha256=d9ae10babb2d90558f411daf4ecae818c32adef9d33b12f67e81e8a489947003

mode=check
mode_seen=""
bin_dir="${ANVIL_AGENTS_JUDGE_BIN_DIR:-}"
bin_dir_explicit=false
force=false
tmp_dir=""
staged_target=""

usage() {
	cat <<'EOF'
Usage: hack/install-judge-prerequisites.sh [--check] [--bin-dir DIR]
       hack/install-judge-prerequisites.sh --install --bin-dir DIR [--force]

With no arguments, make no changes and verify supported judge prerequisites on
PATH. Installation is an explicit operation: it downloads pinned Kind, kubectl,
and Helm binaries from their official release hosts and validates repository-
pinned SHA-256 checksums before atomically writing them to DIR.

Docker Engine is checked but never installed because it requires privileged,
host-specific daemon and service configuration. This script never uses sudo,
a package manager, or a shell-profile edit.

Options:
  --check         Make no changes; verify binaries on PATH or in --bin-dir.
  --install       Install the pinned user-space tools; requires --bin-dir or
                  ANVIL_AGENTS_JUDGE_BIN_DIR.
  --bin-dir DIR   Absolute installation/check directory.
  --force         During --install, replace mismatched existing tool binaries.
  -h, --help      Show this help.

Environment:
  ANVIL_AGENTS_JUDGE_BIN_DIR  Absolute installation/check directory.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--check)
			[[ -z "${mode_seen}" || "${mode_seen}" == check ]] || {
				echo "--check and --install are mutually exclusive" >&2
				exit 2
			}
			mode=check
			mode_seen=check
			shift
			;;
		--install)
			[[ -z "${mode_seen}" || "${mode_seen}" == install ]] || {
				echo "--check and --install are mutually exclusive" >&2
				exit 2
			}
			mode=install
			mode_seen=install
			shift
			;;
		--bin-dir|--install-dir)
			[[ $# -ge 2 && -n "$2" ]] || {
				echo "$1 requires a non-empty directory" >&2
				exit 2
			}
			bin_dir="$2"
			bin_dir_explicit=true
			shift 2
			;;
		--force)
			force=true
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			usage >&2
			exit 2
			;;
	esac
done

if [[ -n "${ANVIL_AGENTS_JUDGE_BIN_DIR:-}" ]]; then
	bin_dir_explicit=true
fi
if [[ "${force}" == true && "${mode}" != install ]]; then
	echo "--force is valid only with --install" >&2
	exit 2
fi
if [[ "${mode}" == install && "${bin_dir_explicit}" != true ]]; then
	echo "--install requires --bin-dir DIR or ANVIL_AGENTS_JUDGE_BIN_DIR" >&2
	exit 2
fi
if [[ "${bin_dir_explicit}" == true ]]; then
	[[ "${bin_dir}" == /* && "${bin_dir}" != / ]] || {
		echo "binary directory must be an absolute path other than /: ${bin_dir}" >&2
		exit 2
	}
fi

((BASH_VERSINFO[0] >= 4)) || {
	echo "Bash 4 or newer is required" >&2
	exit 1
}
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || {
	echo "the public judge path is certified only on Linux amd64 (x86_64)" >&2
	exit 1
}

cleanup() {
	if [[ -n "${staged_target}" && -e "${staged_target}" ]]; then
		case "${staged_target}" in
			"${bin_dir}"/.anvil-agents-*) rm -f -- "${staged_target}" ;;
		esac
	fi
	if [[ -n "${tmp_dir}" && -d "${tmp_dir}" ]]; then
		rm -rf -- "${tmp_dir}"
	fi
}
trap cleanup EXIT

binary_path() {
	local name="$1"
	if [[ "${bin_dir_explicit}" == true ]]; then
		printf '%s/%s\n' "${bin_dir}" "${name}"
	else
		command -v "${name}" 2>/dev/null || true
	fi
}

version_output() {
	local name="$1"
	local path="$2"
	case "${name}" in
		kind) "${path}" version 2>/dev/null || true ;;
		kubectl) "${path}" version --client=true --output=yaml 2>/dev/null || true ;;
		helm) "${path}" version --short 2>/dev/null || true ;;
	esac
}

extract_version() {
	local output="$1"
	if [[ "${output}" =~ v([0-9]+)\.([0-9]+)\.([0-9]+) ]]; then
		printf '%s %s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}"
		return 0
	fi
	return 1
}

has_pinned_content() {
	local name="$1"
	local path="$2"
	local expected actual
	[[ -f "${path}" ]] || return 1
	case "${name}" in
		kind) expected="${kind_sha256}" ;;
		kubectl) expected="${kubectl_sha256}" ;;
		helm) expected="${helm_binary_sha256}" ;;
	esac
	read -r actual _ < <(sha256sum "${path}")
	[[ "${actual}" == "${expected}" ]]
}

has_compatible_version() {
	local name="$1"
	local output="$2"
	local major minor version_patch
	read -r major minor version_patch < <(extract_version "${output}") || return 1
	case "${name}" in
		kind)
			((major > 0 || (major == 0 && (minor > 27 || (minor == 27 && version_patch >= 0)))))
			;;
		kubectl)
			((major == 1 && minor >= 31 && minor <= 33))
			;;
		helm)
			((major == 3 && minor >= 14))
			;;
	esac
}

download() {
	local url="$1"
	local destination="$2"
	curl --fail --location --silent --show-error \
		--proto '=https' --tlsv1.2 --retry 3 \
		--output "${destination}" "${url}"
}

verify_sha256() {
	local path="$1"
	local expected="$2"
	local actual
	read -r actual _ < <(sha256sum "${path}")
	[[ "${actual}" == "${expected}" ]] || {
		echo "checksum mismatch for ${path}: got ${actual}, want ${expected}" >&2
		exit 1
	}
}

preflight_target() {
	local name="$1"
	local target="${bin_dir}/${name}"
	if [[ -e "${target}" || -L "${target}" ]]; then
		if has_pinned_content "${name}" "${target}"; then
			return 0
		fi
		[[ "${force}" == true && ! -d "${target}" ]] || {
			echo "refusing to replace mismatched ${target}; inspect it or rerun with --force" >&2
			exit 1
		}
	fi
}

atomic_install() {
	local name="$1"
	local source="$2"
	local target="${bin_dir}/${name}"
	if has_pinned_content "${name}" "${target}"; then
		printf '%s is already the pinned version at %s\n' "${name}" "${target}"
		return 0
	fi
	staged_target="${bin_dir}/.anvil-agents-${name}.$$"
	install -m 0755 "${source}" "${staged_target}"
	mv -f -- "${staged_target}" "${target}"
	staged_target=""
	printf 'Installed %s at %s\n' "${name}" "${target}"
}

install_tools() {
	local command need_kind=true need_kubectl=true need_helm=true

	for command in mkdir sha256sum; do
		command -v "${command}" >/dev/null 2>&1 || {
			echo "${command} is required to install judge prerequisites" >&2
			exit 1
		}
	done
	mkdir -p -- "${bin_dir}"
	bin_dir="$(cd "${bin_dir}" && pwd -P)"
	[[ "${bin_dir}" != / && -w "${bin_dir}" ]] || {
		echo "binary directory is unsafe or not writable: ${bin_dir}" >&2
		exit 1
	}
	preflight_target kind
	preflight_target kubectl
	preflight_target helm
	has_pinned_content kind "${bin_dir}/kind" && need_kind=false
	has_pinned_content kubectl "${bin_dir}/kubectl" && need_kubectl=false
	has_pinned_content helm "${bin_dir}/helm" && need_helm=false
	if [[ "${need_kind}" == false && "${need_kubectl}" == false && "${need_helm}" == false ]]; then
		printf 'Pinned judge tools are already installed in %s; no changes made.\n' "${bin_dir}"
		return 0
	fi

	for command in curl sha256sum tar install mktemp mv rm; do
		command -v "${command}" >/dev/null 2>&1 || {
			echo "${command} is required to install judge prerequisites" >&2
			exit 1
		}
	done

	tmp_dir="$(mktemp -d /tmp/anvil-agents-judge-prereqs.XXXXXX)"
	if [[ "${need_kind}" == true ]]; then
		download "https://github.com/kubernetes-sigs/kind/releases/download/${kind_version}/kind-linux-amd64" "${tmp_dir}/kind"
		verify_sha256 "${tmp_dir}/kind" "${kind_sha256}"
		atomic_install kind "${tmp_dir}/kind"
	fi
	if [[ "${need_kubectl}" == true ]]; then
		download "https://dl.k8s.io/release/${kubectl_version}/bin/linux/amd64/kubectl" "${tmp_dir}/kubectl"
		verify_sha256 "${tmp_dir}/kubectl" "${kubectl_sha256}"
		atomic_install kubectl "${tmp_dir}/kubectl"
	fi
	if [[ "${need_helm}" == true ]]; then
		download "https://get.helm.sh/helm-${helm_version}-linux-amd64.tar.gz" "${tmp_dir}/helm.tar.gz"
		verify_sha256 "${tmp_dir}/helm.tar.gz" "${helm_sha256}"
		tar -xzf "${tmp_dir}/helm.tar.gz" -C "${tmp_dir}"
		verify_sha256 "${tmp_dir}/linux-amd64/helm" "${helm_binary_sha256}"
		atomic_install helm "${tmp_dir}/linux-amd64/helm"
	fi
}

check_tool() {
	local name="$1"
	local supported="$2"
	local path output
	path="$(binary_path "${name}")"
	[[ -n "${path}" && -x "${path}" ]] || {
		echo "${name} was not found; supported: ${supported}" >&2
		return 1
	}
	output="$(version_output "${name}" "${path}")"
	has_compatible_version "${name}" "${output}" || {
		echo "${name} at ${path} is unsupported (${output:-unknown}); supported: ${supported}" >&2
		return 1
	}
	printf 'Ready: %s at %s (%s)\n' "${name}" "${path}" "${output//$'\n'/ }"
}

check_docker() {
	local data os architecture cpus memory
	command -v docker >/dev/null 2>&1 || {
		echo "Docker CLI is required by the public judge test" >&2
		return 1
	}
	data="$(docker info --format '{{.OSType}}|{{.Architecture}}|{{.NCPU}}|{{.MemTotal}}' 2>/dev/null)" || {
		echo "Docker is installed but its daemon is unavailable or unauthorized" >&2
		return 1
	}
	IFS='|' read -r os architecture cpus memory <<<"${data}"
	[[ "${os}" == linux && ("${architecture}" == x86_64 || "${architecture}" == amd64) ]] || {
		echo "Docker daemon must be Linux amd64; detected ${os:-unknown}/${architecture:-unknown}" >&2
		return 1
	}
	[[ "${cpus}" =~ ^[0-9]+$ && "${memory}" =~ ^[0-9]+$ ]] || {
		echo "Docker did not report numeric CPU and memory capacity" >&2
		return 1
	}
	((cpus >= 2 && memory >= 3221225472)) || {
		echo "Docker needs at least 2 CPUs and 3 GiB; detected ${cpus} CPUs and ${memory} bytes" >&2
		return 1
	}
	printf 'Ready: Docker daemon (%s/%s, %s CPUs, %s bytes memory)\n' \
		"${os}" "${architecture}" "${cpus}" "${memory}"
}

check_runtime() {
	local command failed=false docker_failed=false
	check_tool kind 'Kind >= v0.27.0' || failed=true
	check_tool kubectl 'kubectl v1.31 through v1.33' || failed=true
	check_tool helm 'Helm >= v3.14.0 and < v4.0.0' || failed=true
	for command in cat dirname grep mktemp rm rmdir; do
		command -v "${command}" >/dev/null 2>&1 || {
			echo "${command} is required by the public judge test" >&2
			failed=true
		}
	done
	check_docker || {
		failed=true
		docker_failed=true
	}
	[[ "${failed}" == false ]] || {
		if [[ "${docker_failed}" == true ]]; then
			echo "Install Docker Engine separately using https://docs.docker.com/engine/install/" >&2
		fi
		return 1
	}
	printf 'All judge prerequisites are ready.\n'
}

if [[ "${mode}" == install ]]; then
	install_tools
	check_runtime
	printf '\nAdd the installed tools to this shell before running the judge test:\n'
	printf '  export PATH=%q:%s%s%s\n' "${bin_dir}" '"' "\${PATH}" '"'
	printf 'Then run: ./hack/test-judge-kind.sh\n'
else
	check_runtime
fi
