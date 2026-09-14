#!/usr/bin/env bash
# Install Anvil Agents Desktop into ~/.local for the current user.
set -euo pipefail

product_title="Anvil Agents Desktop"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
prefix="${PREFIX:-${HOME}/.local}"
bindir="${prefix}/bin"
appdir="${prefix}/share/applications"
uninstall=0

usage() {
	cat <<EOF
Install ${product_title} for the current user.

Usage:
  ./hack/install-anvil-desktop.sh
  PREFIX=\$HOME/.local ./hack/install-anvil-desktop.sh
  ./hack/install-anvil-desktop.sh --uninstall

Looks for dist/desktop/bin/anvil-desktop or builds it with make desktop-embed.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--uninstall) uninstall=1 ;;
	-h|--help) usage; exit 0 ;;
	*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

desktop_file="${appdir}/anvil-agents-desktop.desktop"
bin_path="${bindir}/anvil-desktop"

if [[ "${uninstall}" -eq 1 ]]; then
	rm -f "${bin_path}" "${desktop_file}"
	echo "Removed ${product_title}"
	exit 0
fi

bin=""
for candidate in \
	"${root}/dist/desktop/bin/anvil-desktop" \
	"${root}/bin/anvil-desktop"; do
	if [[ -x "${candidate}" ]]; then
		bin="${candidate}"
		break
	fi
done
if [[ -z "${bin}" ]]; then
	make -C "${root}" desktop-embed
	mkdir -p "${root}/bin"
	go -C "${root}" build -trimpath -o "${root}/bin/anvil-desktop" ./cmd/anvil-desktop
	make -C "${root}" desktop-embed-restore
	bin="${root}/bin/anvil-desktop"
fi

mkdir -p "${bindir}" "${appdir}"
cp -f "${bin}" "${bin_path}"
chmod 0755 "${bin_path}"
cat > "${desktop_file}" <<EOF
[Desktop Entry]
Type=Application
Name=${product_title}
Comment=OIDC wrapper agent for anvil-agents with local and WSL harnesses
Exec=${bin_path} --open
Icon=utilities-terminal
Terminal=false
Categories=Development;Utility;
StartupNotify=true
EOF
echo "Installed ${product_title} to ${bin_path}"
echo "Desktop entry: ${desktop_file}"
echo "Loopback UI: http://127.0.0.1:1738"
echo "Choose Operate on WSL when running on Windows with WSL; on Linux this host already uses the local PATH."
