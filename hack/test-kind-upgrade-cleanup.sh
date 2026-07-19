#!/usr/bin/env bash
set -euo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT
repo_root="${tmp_dir}/repo"
bin_dir="${tmp_dir}/bin"
log="${tmp_dir}/kind.log"
mkdir -p "${repo_root}/hack" "${bin_dir}"
cp "${source_root}/hack/test-kind-upgrade.sh" "${repo_root}/hack/test-kind-upgrade.sh"

cat > "${bin_dir}/kind" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\t%s\n' "$*" "${KUBECONFIG:-}" >> "${FAKE_KIND_LOG}"
case "${1:-} ${2:-}" in
	"get clusters") exit 0 ;;
	"create cluster") exit 37 ;;
	"delete cluster") exit 0 ;;
	*) exit 2 ;;
esac
EOF
for command in kubectl helm rg; do
	printf '#!/usr/bin/env bash\nexit 0\n' > "${bin_dir}/${command}"
done
chmod 0755 "${bin_dir}"/* "${repo_root}/hack/test-kind-upgrade.sh"

set +e
FAKE_KIND_LOG="${log}" PATH="${bin_dir}:${PATH}" \
	ANVIL_AGENTS_UPGRADE_KIND_CLUSTER=partial-create-test \
	"${repo_root}/hack/test-kind-upgrade.sh" >/dev/null 2>&1
status=$?
set -e
[[ "${status}" -eq 37 ]] || { echo "partial Kind failure status = ${status}, want 37" >&2; exit 1; }
grep -q '^create cluster --name partial-create-test' "${log}" || { echo "Kind create was not attempted" >&2; exit 1; }
grep -q '^delete cluster --name partial-create-test' "${log}" || { echo "partial Kind cluster was not deleted" >&2; exit 1; }
kubeconfig="$(awk -F '\t' '$1 ~ /^create cluster / { print $2; exit }' "${log}")"
[[ -n "${kubeconfig}" && ! -e "$(dirname "${kubeconfig}")" ]] || { echo "upgrade temporary directory was not removed" >&2; exit 1; }

echo "Kind partial-create cleanup contract passed"
