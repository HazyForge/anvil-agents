#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"

cleanup() {
	rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

write_mock() {
	local name="$1"
	shift
	{
		printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
		printf '%s\n' "$@"
	} >"${tmp_dir}/${name}"
	chmod 0755 "${tmp_dir}/${name}"
}

write_mock kind 'printf "%s\n" "kind v0.27.0 go1.23.6 linux/amd64"'
write_mock kubectl 'printf "%s\n" "clientVersion:" "  gitVersion: v1.32.2"'
write_mock helm 'printf "%s\n" "v3.21.2+gmock"'
write_mock docker \
	"if [[ \"\${1:-}\" == info ]]; then" \
	'  printf "%s\n" "linux|x86_64|4|8589934592"' \
	'fi'

PATH="${tmp_dir}:${PATH}" "${root_dir}/hack/install-judge-prerequisites.sh" --check \
	>"${tmp_dir}/check.out"
grep -Fq 'All judge prerequisites are ready.' "${tmp_dir}/check.out"

PATH="${tmp_dir}:${PATH}" "${root_dir}/hack/install-judge-prerequisites.sh" \
	--check --bin-dir "${tmp_dir}" >"${tmp_dir}/directory-check.out"

write_mock kind 'printf "%s\n" "kind v0.26.0 go1.23.6 linux/amd64"'
if PATH="${tmp_dir}:${PATH}" "${root_dir}/hack/install-judge-prerequisites.sh" \
	--install --bin-dir "${tmp_dir}" >"${tmp_dir}/replace.out" 2>"${tmp_dir}/replace.err"; then
	echo "install unexpectedly replaced a mismatched binary without --force" >&2
	exit 1
fi
grep -Fq 'refusing to replace mismatched' "${tmp_dir}/replace.err"

if PATH="${tmp_dir}:${PATH}" "${root_dir}/hack/install-judge-prerequisites.sh" --check \
	>"${tmp_dir}/old.out" 2>"${tmp_dir}/old.err"; then
	echo "check unexpectedly accepted an uncertified Kind version" >&2
	exit 1
fi
grep -Fq 'supported: Kind >= v0.27.0' "${tmp_dir}/old.err"

if "${root_dir}/hack/install-judge-prerequisites.sh" --install \
	>"${tmp_dir}/install.out" 2>"${tmp_dir}/install.err"; then
	echo "install unexpectedly accepted an implicit binary directory" >&2
	exit 1
fi
grep -Fq -- '--install requires --bin-dir DIR' "${tmp_dir}/install.err"

if "${root_dir}/hack/install-judge-prerequisites.sh" --check --install \
	--bin-dir "${tmp_dir}" >"${tmp_dir}/modes.out" 2>"${tmp_dir}/modes.err"; then
	echo "installer unexpectedly accepted conflicting modes" >&2
	exit 1
fi
grep -Fq 'mutually exclusive' "${tmp_dir}/modes.err"

if "${root_dir}/hack/install-judge-prerequisites.sh" --check --bin-dir / \
	>"${tmp_dir}/root.out" 2>"${tmp_dir}/root.err"; then
	echo "check unexpectedly accepted / as the binary directory" >&2
	exit 1
fi
grep -Fq 'absolute path other than /' "${tmp_dir}/root.err"

write_mock kind 'printf "%s\n" "kind v0.27.0 go1.23.6 linux/amd64"'
write_mock docker \
	"if [[ \"\${1:-}\" == info ]]; then" \
	'  printf "%s\n" "linux|x86_64|1|2147483648"' \
	'fi'
if PATH="${tmp_dir}:${PATH}" "${root_dir}/hack/install-judge-prerequisites.sh" --check \
	>"${tmp_dir}/capacity.out" 2>"${tmp_dir}/capacity.err"; then
	echo "check unexpectedly accepted insufficient Docker capacity" >&2
	exit 1
fi
grep -Fq 'Docker needs at least 2 CPUs and 3 GiB' "${tmp_dir}/capacity.err"

printf 'judge prerequisite installer contract passed\n'
