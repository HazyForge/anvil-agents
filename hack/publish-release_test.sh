#!/usr/bin/env bash
set -euo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
repo_root="${tmp_dir}/repo"
mkdir -p "${repo_root}/hack" "${tmp_dir}/bin"
cp "${source_root}/hack/publish-release.sh" "${repo_root}/hack/publish-release.sh"
export FAKE_LOG="${tmp_dir}/commands.log"

cat > "${repo_root}/hack/publish-images.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'publish-images %s\n' "$*" >> "${FAKE_LOG}"
if [[ "$1" == "--verify-lock" ]]; then
	[[ -f "$2" ]]
	exit 0
fi
while [[ $# -gt 0 ]]; do
	case "$1" in
		--output) output="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "$(dirname "${output}")"
printf 'schema\tanvil-agents-image-lock/v1\n' > "${output}"
EOF

cat > "${repo_root}/hack/package-chart.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'package-chart %s\n' "$*" >> "${FAKE_LOG}"
while [[ $# -gt 0 ]]; do
	case "$1" in
		--version) version="${2#v}"; shift 2 ;;
		--output) output="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "${output}"
: > "${output}/anvil-agents-${version}.tgz"
EOF

cat > "${tmp_dir}/bin/helm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'helm %s\n' "$*" >> "${FAKE_LOG}"
[[ "$1" == "push" && -f "$2" ]]
EOF
chmod 0755 "${repo_root}/hack/"*.sh "${tmp_dir}/bin/helm"
export PATH="${tmp_dir}/bin:${PATH}"

fail() {
	echo "publish-release test failed: $*" >&2
	exit 1
}

output="${tmp_dir}/dist"
"${repo_root}/hack/publish-release.sh" \
	--prefix registry.example.com/team \
	--version v0.2.3 \
	--platform linux/amd64 \
	--output "${output}" >/dev/null

rg -q "^publish-images --prefix registry.example.com/team --version v0.2.3 --platform linux/amd64 --output ${output}/images-v0.2.3.lock.tsv$" "${FAKE_LOG}" ||
	fail "image publication arguments were not forwarded"
rg -q "^package-chart --version v0.2.3 --output ${output} --image-prefix registry.example.com/team$" "${FAKE_LOG}" ||
	fail "chart packaging arguments were not forwarded"
rg -q "^helm push ${output}/anvil-agents-0.2.3.tgz oci://registry.example.com/team/charts$" "${FAKE_LOG}" ||
	fail "chart was not pushed to the default OCI destination"
rg -q "^publish-images --verify-lock ${output}/images-v0.2.3.lock.tsv$" "${FAKE_LOG}" ||
	fail "image lock was not verified after chart publication"

if "${repo_root}/hack/publish-release.sh" \
	--prefix registry.example.com/team --version 0.2.3 >"${tmp_dir}/version.out" 2>&1; then
	fail "accepted a release version without the v prefix"
fi
rg -q 'v-prefixed SemVer' "${tmp_dir}/version.out" || fail "version error is unclear"

echo "publish-release contract tests passed"
