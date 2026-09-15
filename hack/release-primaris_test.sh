#!/usr/bin/env bash
set -euo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fail() {
	echo "release-primaris test failed: $*" >&2
	exit 1
}

bash -n "${source_root}/hack/deploy-primaris.sh" || fail "deploy-primaris bash -n"
bash -n "${source_root}/hack/release-primaris.sh" || fail "release-primaris bash -n"

"${source_root}/hack/deploy-primaris.sh" --help >/dev/null || fail "deploy help"
"${source_root}/hack/release-primaris.sh" --help >/dev/null || fail "release help"

# pin_component_ref behavior via a tiny deploy yaml and release-primaris hot pin path
# exercised by extracting the awk logic through a dry component pin helper.
deploy="${tmp_dir}/deploy.yaml"
cat > "${deploy}" <<'EOF'
helmChartPath: charts/anvil-agents
image:
  reference: ghcr.io/hazyforge/anvil-agents@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  pullPolicy: IfNotPresent
runnerImages:
  codex: ghcr.io/hazyforge/anvil-agent-run-codex@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  openCode: ghcr.io/hazyforge/anvil-agent-run-opencode@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
  hermesAgent: ghcr.io/hazyforge/anvil-agent-run-hermes@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
  openClaw: ghcr.io/hazyforge/anvil-agent-run-openclaw@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
  grokBuild: ghcr.io/hazyforge/anvil-agent-run-grok-build@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
  piAgent: ghcr.io/hazyforge/anvil-agent-run-pi@sha256:1111111111111111111111111111111111111111111111111111111111111111
  agy: ghcr.io/hazyforge/anvil-agent-run-agy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  primeAgent: ghcr.io/hazyforge/anvil-agent-run-prime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
crds:
  install: true
EOF

# Reuse pin-deploy for full lock coverage (already tested elsewhere).
lock="${tmp_dir}/images-v9.9.9.lock.tsv"
cat > "${lock}" <<'EOF'
schema	anvil-agents-image-lock/v1
source-revision	0123456789abcdef0123456789abcdef01234567
platform	linux/amd64
controller	ghcr.io/hazyforge/anvil-agents@sha256:2222222222222222222222222222222222222222222222222222222222222222
codex	ghcr.io/hazyforge/anvil-agent-run-codex@sha256:3333333333333333333333333333333333333333333333333333333333333333
opencode	ghcr.io/hazyforge/anvil-agent-run-opencode@sha256:4444444444444444444444444444444444444444444444444444444444444444
grok-build	ghcr.io/hazyforge/anvil-agent-run-grok-build@sha256:5555555555555555555555555555555555555555555555555555555555555555
hermes	ghcr.io/hazyforge/anvil-agent-run-hermes@sha256:6666666666666666666666666666666666666666666666666666666666666666
openclaw	ghcr.io/hazyforge/anvil-agent-run-openclaw@sha256:7777777777777777777777777777777777777777777777777777777777777777
pi	ghcr.io/hazyforge/anvil-agent-run-pi@sha256:8888888888888888888888888888888888888888888888888888888888888888
agy	ghcr.io/hazyforge/anvil-agent-run-agy@sha256:9999999999999999999999999999999999999999999999999999999999999999
prime	ghcr.io/hazyforge/anvil-agent-run-prime@sha256:8888888888888888888888888888888888888888888888888888888888888888
EOF

"${source_root}/hack/pin-deploy-values-from-lock.sh" \
	--image-lock "${lock}" \
	--values "${deploy}" >/dev/null
rg -q 'sha256:2222222222222222222222222222222222222222222222222222222222222222' "${deploy}" ||
	fail "controller pin missing"
rg -q 'sha256:3333333333333333333333333333333333333333333333333333333333333333' "${deploy}" ||
	fail "codex pin missing"
rg -q 'install: true' "${deploy}" || fail "crds.install lost"

rg -q '^  primeAgent: ghcr.io/hazyforge/anvil-agent-run-prime@sha256:8888888888888888888888888888888888888888888888888888888888888888$' "${deploy}" || fail "Prime image digest was not pinned"

# Existing API overrides follow full and hot controller releases. An empty
# override continues to inherit the shared controller image.
cat >> "${deploy}" <<'EOF'
api:
  enabled: true
  image:
    reference: registry.example/api:old
EOF
"${source_root}/hack/pin-deploy-values-from-lock.sh" --image-lock "${lock}" --values "${deploy}" >/dev/null
rg -q '^    reference: ghcr.io/hazyforge/anvil-agents@sha256:2222222222222222222222222222222222222222222222222222222222222222$' "${deploy}" || fail "full API pin stale"
source <(sed -n '/^pin_component_ref() {/,/^}/p' "${source_root}/hack/release-primaris.sh")
deploy_yaml_key_for_component() { echo unused; }
values_file="${deploy}"
pin_component_ref controller 'registry.example/controller@sha256:hot' >/dev/null
rg -q '^    reference: registry.example/controller@sha256:hot$' "${deploy}" || fail "hot API pin stale"
sed -i 's|^    reference:.*|    reference: ""|' "${deploy}"
pin_component_ref controller 'registry.example/controller@sha256:next' >/dev/null
rg -q '^    reference: ""$' "${deploy}" || fail "empty API override changed"

# Fake helm for deploy dry-run path
mkdir -p "${tmp_dir}/bin"
cat > "${tmp_dir}/bin/helm" <<'EOF'
#!/usr/bin/env bash
echo "helm $*"
exit 0
EOF
cat > "${tmp_dir}/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${tmp_dir}/bin/helm" "${tmp_dir}/bin/kubectl"
export PATH="${tmp_dir}/bin:${PATH}"

out="$("${source_root}/hack/deploy-primaris.sh" --values "${deploy}" --chart "${source_root}/charts/anvil-agents" --dry-run 2>&1)" ||
	fail "deploy dry-run failed: ${out}"
printf '%s\n' "${out}" | rg -q 'upgrade --install anvil-agents-system-chart' ||
	fail "deploy did not invoke helm upgrade: ${out}"
printf '%s\n' "${out}" | rg -q -- '--create-namespace' || fail "missing create-namespace"
printf '%s\n' "${out}" | rg -q -- '--dry-run=client' || fail "missing dry-run"

echo "release-primaris contract tests passed"
