#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

bash -n "${root}/hack/package-anvil-desktop.sh"
bash -n "${root}/hack/install-anvil-desktop.sh"
bash -n "${root}/hack/run-anvil-desktop.sh"

"${root}/hack/package-anvil-desktop.sh" --help >/dev/null
"${root}/hack/install-anvil-desktop.sh" --help >/dev/null

# Windows setup source must stay ignored so go test ./... does not need payload.zip.
if ! grep -q '^//go:build ignore$' "${root}/hack/anvil-desktop-windows-setup/main.go"; then
	echo "windows setup must be //go:build ignore" >&2
	exit 1
fi
if ! grep -q 'anvil-agents-desktop' "${root}/hack/package-anvil-desktop.sh"; then
	echo "package README should mention desktop PKCE client" >&2
	exit 1
fi
if ! grep -q 'Operate on WSL' "${root}/hack/install-anvil-desktop.ps1"; then
	echo "Windows install.ps1 must mention Operate on WSL" >&2
	exit 1
fi

echo "package-anvil-desktop.sh syntax ok"
