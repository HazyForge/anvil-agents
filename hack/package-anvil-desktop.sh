#!/usr/bin/env bash
# Build installable Anvil Agents Desktop artifacts (Windows zip + setup.exe, Linux tarball).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${VERSION:-0.1.0}"
output="${OUTPUT:-${root}/dist/desktop}"
skip_embed=0
skip_electron=1

usage() {
	cat <<EOF
Package Anvil Agents Desktop for workstation install.

Usage:
  ./hack/package-anvil-desktop.sh
  VERSION=0.1.0 OUTPUT=dist/desktop ./hack/package-anvil-desktop.sh

Artifacts:
  dist/desktop/Anvil-Agents-Desktop-\${version}-windows-amd64.zip
  dist/desktop/Anvil-Agents-Desktop-Setup-\${version}-windows-amd64.exe
  dist/desktop/anvil-desktop-\${version}-linux-amd64.tar.gz
  dist/desktop/SHA256SUMS
  dist/desktop/README.txt

Options:
  --skip-embed      Assume internal/desktop/uifs/dist already has a built SPA
  --electron        Also run electron-builder --win zip (optional; NSIS needs wine)
  -h, --help        Show this help
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--skip-embed) skip_embed=1 ;;
	--skip-electron) skip_electron=1 ;;
	--electron) skip_electron=0 ;;
	-h|--help) usage; exit 0 ;;
	*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

product="Anvil Agents Desktop"
mkdir -p "${output}/bin" "${output}/sidecar"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

zip_files() {
	local dest="$1"
	shift
	if command -v zip >/dev/null; then
		zip -q -9 "${dest}" "$@"
		return
	fi
	python3 - "${dest}" "$@" <<'PY'
import sys, zipfile
dest = sys.argv[1]
with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED) as zf:
    for name in sys.argv[2:]:
        zf.write(name, arcname=name)
PY
}

if [[ "${skip_embed}" -eq 0 ]]; then
	make -C "${root}" desktop-embed
	restore_embed=1
else
	restore_embed=0
fi
restore_embed() {
	if [[ "${restore_embed}" -eq 1 ]]; then
		make -C "${root}" desktop-embed-restore
		restore_embed=0
	fi
}
trap 'restore_embed; rm -rf "${tmp}"' EXIT

echo "building anvil-desktop (linux/amd64)"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "${root}" build -trimpath -ldflags "-s -w" \
	-o "${output}/bin/anvil-desktop" ./cmd/anvil-desktop
cp -f "${output}/bin/anvil-desktop" "${output}/sidecar/anvil-desktop"

echo "building anvil-desktop (windows/amd64)"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go -C "${root}" build -trimpath -ldflags "-s -w" \
	-o "${output}/bin/anvil-desktop.exe" ./cmd/anvil-desktop
cp -f "${output}/bin/anvil-desktop.exe" "${output}/sidecar/anvil-desktop.exe"

restore_embed

readme="${output}/README.txt"
cat > "${readme}" <<EOF
${product} ${version}

User-visible name: ${product}
Process binary: anvil-desktop
Main function: this process signs in to the anvil-agents OIDC API and talks to
cluster agents on Anvil Primaris (default apiOrigin
https://agents.anvil.hazyforge.io). Chat and Wrapper are the product.
Second function: Local page activates already-installed grok/Codex/OpenCode
(native PATH or WSL). Local does not replace Chat/Wrapper.
OIDC: Authorization Code + PKCE, provider-neutral. Prefer desktop.oidcClientId
from {apiOrigin}/ui-config.json (Zitadel Native). Console oidc.clientId is a
visible fallback only when Native is absent. Kind pattern anvil-agents-desktop
(--oidc-client-id). Redirect http://127.0.0.1:1738/auth/callback.

Windows install
---------------
Preferred: run Anvil-Agents-Desktop-Setup-${version}-windows-amd64.exe
(per-user, no Administrator). It extracts to
%LOCALAPPDATA%\\Programs\\AnvilAgentsDesktop and adds a Start Menu shortcut.

Or unzip Anvil-Agents-Desktop-${version}-windows-amd64.zip and run:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\\install.ps1

Then launch "Anvil Agents Desktop" (anvil-desktop.exe --open). The host listens
on http://127.0.0.1:1738 only and calls Primaris unless --api-origin is set.

Linux / WSL-side binary
-----------------------
tar -xzf anvil-desktop-${version}-linux-amd64.tar.gz
./hack/install-anvil-desktop.sh
# or: PREFIX=\$HOME/.local ./install.sh

Local harness (second function)
-------------------------------
On the Local page, choose "Operate on WSL" or native PATH, then Run locally.
Windows-hosted processes use wsl.exe and the default distro PATH. The OIDC
token stays in the desktop session; it is never copied into CLI argv, env, or
prompt files.

Sign-in (main)
--------------
1. Default OIDC API origin is https://agents.anvil.hazyforge.io
2. Sign in with OIDC. Redirect: http://127.0.0.1:1738/auth/callback
3. Register that redirect on the Native PKCE client (anvil-agents-desktop /
   Zitadel Native). Prefer desktop.oidcClientId from ui-config.json.
4. Chat and Wrapper talk to Primaris agents. There is no kube UI.

Uninstall (Windows): Anvil-Agents-Desktop-Setup.exe --uninstall
  or: powershell -File install.ps1 -Uninstall
EOF

win_dir="${tmp}/windows"
mkdir -p "${win_dir}"
cp -f "${output}/bin/anvil-desktop.exe" "${win_dir}/anvil-desktop.exe"
cp -f "${root}/hack/install-anvil-desktop.ps1" "${win_dir}/install.ps1"
cp -f "${readme}" "${win_dir}/README.txt"
win_zip="${output}/Anvil-Agents-Desktop-${version}-windows-amd64.zip"
rm -f "${win_zip}"
(
	cd "${win_dir}"
	zip_files "${win_zip}" anvil-desktop.exe install.ps1 README.txt
)

echo "building Windows setup.exe"
payload_zip="${tmp}/setup/payload.zip"
mkdir -p "${tmp}/setup"
(
	cd "${win_dir}"
	zip_files "${payload_zip}" anvil-desktop.exe install.ps1 README.txt
)
# Drop the ignore build tag so this isolated module builds.
grep -v '^//go:build ignore$' "${root}/hack/anvil-desktop-windows-setup/main.go" > "${tmp}/setup/main.go"
(
	cd "${tmp}/setup"
	go mod init anvil-desktop-setup >/dev/null
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
		-o "${output}/Anvil-Agents-Desktop-Setup-${version}-windows-amd64.exe" .
)

linux_dir="${tmp}/linux"
mkdir -p "${linux_dir}"
cp -f "${output}/bin/anvil-desktop" "${linux_dir}/anvil-desktop"
cp -f "${root}/hack/install-anvil-desktop.sh" "${linux_dir}/install.sh"
cp -f "${readme}" "${linux_dir}/README.txt"
chmod 0755 "${linux_dir}/anvil-desktop" "${linux_dir}/install.sh"
linux_tar="${output}/anvil-desktop-${version}-linux-amd64.tar.gz"
rm -f "${linux_tar}"
tar -C "${linux_dir}" -czf "${linux_tar}" anvil-desktop install.sh README.txt

if [[ "${skip_electron}" -eq 0 ]] && command -v npx >/dev/null; then
	echo "optional electron-builder (windows zip); NSIS needs wine and may be skipped"
	if npx --yes electron-builder --version >/dev/null 2>&1; then
		(
			cd "${root}/web/desktop"
			if [[ ! -d node_modules ]]; then
				npm ci
			fi
			npx --yes electron-builder --win zip --x64 || echo "electron-builder windows zip skipped"
		) || true
		if [[ -d "${root}/web/desktop/release" ]]; then
			mkdir -p "${output}/electron"
			cp -a "${root}/web/desktop/release/." "${output}/electron/" || true
		fi
	fi
fi

(
	cd "${output}"
	sha256sum \
		"Anvil-Agents-Desktop-${version}-windows-amd64.zip" \
		"Anvil-Agents-Desktop-Setup-${version}-windows-amd64.exe" \
		"anvil-desktop-${version}-linux-amd64.tar.gz" \
		README.txt \
		> SHA256SUMS
)

echo "packaged ${product} ${version} -> ${output}"
ls -l "${output}"
