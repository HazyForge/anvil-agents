#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
mkdir -p "${tmp_dir}/bin"
export FAKE_LOG="${tmp_dir}/commands.log"
export FAKE_REVISION="0123456789abcdef0123456789abcdef01234567"

cat > "${tmp_dir}/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
	*"status --porcelain"*) exit 0 ;;
	*"rev-parse --short=7 HEAD"*) printf '%s\n' "${FAKE_REVISION:0:7}" ;;
	*"rev-parse -q --verify refs/tags/v0.1.0^{commit}"*) printf '%s\n' "${FAKE_REVISION}" ;;
	*"rev-parse HEAD"*) printf '%s\n' "${FAKE_REVISION}" ;;
	*"remote get-url origin"*) printf '%s\n' https://github.com/HazyForge/anvil-agents.git ;;
	*) echo "unexpected fake git command: $*" >&2; exit 1 ;;
esac
EOF

cat > "${tmp_dir}/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FAKE_LOG}"
if [[ "$*" == "buildx version" ]]; then
	exit 0
fi
if [[ "$1 $2 $3" == "buildx imagetools inspect" ]]; then
	ref="$4"
	format="${6:-}"
	if [[ "${format}" == *Manifest.Digest* ]]; then
		if [[ "${FAKE_MISMATCH_TAG:-}" == "true" && "${ref}" == *":sha-"* ]]; then
			printf '%s\n' sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
		else
			printf '%s\n' sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
		fi
	elif [[ "${format}" == *Manifest.Manifests* ]]; then
		printf '%s\n' sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
	else
		printf '%s\n' "${FAKE_BAD_REVISION:-${FAKE_REVISION}}"
	fi
fi
EOF
chmod 0755 "${tmp_dir}/bin/git" "${tmp_dir}/bin/docker"
export PATH="${tmp_dir}/bin:${PATH}"

fail() {
	echo "publish-images test failed: $*" >&2
	exit 1
}

lock="${tmp_dir}/images.lock.tsv"
"${repo_root}/hack/publish-images.sh" \
	--prefix registry.example.com/team \
	--version v0.1.0 \
	--output "${lock}" >/dev/null
[[ -f "${lock}" ]] || fail "publication did not create a lock"
[[ "$(rg -c $'^(controller|codex|opencode|grok-build|hermes|openclaw|pi)\t' "${lock}")" -eq 7 ]] || fail "lock does not contain seven components"
[[ "$(rg -c '^buildx build --push ' "${FAKE_LOG}")" -eq 7 ]] || fail "publication did not build seven images"
"${repo_root}/hack/publish-images.sh" --verify-lock "${lock}" >/dev/null
cp "${lock}" "${tmp_dir}/valid.lock"

if FAKE_BAD_REVISION=wrong "${repo_root}/hack/publish-images.sh" \
	--verify-lock "${tmp_dir}/valid.lock" >"${tmp_dir}/revision.out" 2>&1; then
	fail "verification accepted the wrong source revision"
fi
rg -q 'revision mismatch' "${tmp_dir}/revision.out" || fail "revision mismatch error is unclear"

rm -f "${lock}"
if FAKE_MISMATCH_TAG=true "${repo_root}/hack/publish-images.sh" \
	--prefix registry.example.com/team --version v0.1.0 --output "${lock}" >"${tmp_dir}/mismatch.out" 2>&1; then
	fail "publication accepted mismatched tag digests"
fi
[[ ! -e "${lock}" ]] || fail "failed publication left a final lock"
rg -q 'tags resolve to different digests' "${tmp_dir}/mismatch.out" || fail "tag mismatch error is unclear"

awk -F '\t' '$1 != "opencode"' <(
	printf 'schema\tanvil-agents-image-lock/v1\nsource-revision\t%s\nplatform\tlinux/amd64\n' "${FAKE_REVISION}"
	for component in controller codex grok-build hermes openclaw pi; do
		printf '%s\tregistry.example.com/team/%s@sha256:%064d\n' "${component}" "${component}" 0
	done
) > "${tmp_dir}/missing.lock"
if "${repo_root}/hack/publish-images.sh" --verify-lock "${tmp_dir}/missing.lock" >"${tmp_dir}/missing.out" 2>&1; then
	fail "verification accepted a lock missing opencode"
fi
rg -q 'missing component: opencode' "${tmp_dir}/missing.out" || fail "missing-component error is unclear"

echo "publish-images contract tests passed"
