#!/usr/bin/env bash
# Install free OSS security tools into $HOME/.local/bin for GitHub Actions.
# Usage: source this file or run with: install_all | install_trivy | ...
set -euo pipefail

BIN_DIR="${SECURITY_TOOLS_BIN:-${HOME}/.local/bin}"
mkdir -p "${BIN_DIR}"
export PATH="${BIN_DIR}:${PATH}"

TRIVY_VERSION="${TRIVY_VERSION:-0.72.0}"
GRYPE_VERSION="${GRYPE_VERSION:-0.116.0}"
OSV_VERSION="${OSV_VERSION:-2.4.0}"
GITLEAKS_VERSION="${GITLEAKS_VERSION:-8.30.1}"
ZIZMOR_VERSION="${ZIZMOR_VERSION:-1.28.0}"
HADOLINT_VERSION="${HADOLINT_VERSION:-2.12.0}"

install_trivy() {
	if command -v trivy >/dev/null 2>&1; then
		trivy version | head -1
		return 0
	fi
	local url="https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-64bit.tar.gz"
	local tmp
	tmp="$(mktemp -d)"
	curl -fsSL -o "${tmp}/trivy.tgz" "${url}"
	tar -xzf "${tmp}/trivy.tgz" -C "${tmp}" trivy
	install -m 0755 "${tmp}/trivy" "${BIN_DIR}/trivy"
	rm -rf "${tmp}"
	trivy version | head -1
}

install_grype() {
	if command -v grype >/dev/null 2>&1; then
		grype version | head -1
		return 0
	fi
	local url="https://github.com/anchore/grype/releases/download/v${GRYPE_VERSION}/grype_${GRYPE_VERSION}_linux_amd64.tar.gz"
	local tmp
	tmp="$(mktemp -d)"
	curl -fsSL -o "${tmp}/grype.tgz" "${url}"
	tar -xzf "${tmp}/grype.tgz" -C "${tmp}" grype
	install -m 0755 "${tmp}/grype" "${BIN_DIR}/grype"
	rm -rf "${tmp}"
	grype version | head -1
}

install_osv() {
	if command -v osv-scanner >/dev/null 2>&1; then
		osv-scanner --version || true
		return 0
	fi
	local url="https://github.com/google/osv-scanner/releases/download/v${OSV_VERSION}/osv-scanner_linux_amd64"
	curl -fsSL -o "${BIN_DIR}/osv-scanner" "${url}"
	chmod +x "${BIN_DIR}/osv-scanner"
	osv-scanner --version || true
}

install_gitleaks() {
	if command -v gitleaks >/dev/null 2>&1; then
		gitleaks version || true
		return 0
	fi
	local url="https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_x64.tar.gz"
	local tmp
	tmp="$(mktemp -d)"
	curl -fsSL -o "${tmp}/gitleaks.tgz" "${url}"
	tar -xzf "${tmp}/gitleaks.tgz" -C "${tmp}" gitleaks
	install -m 0755 "${tmp}/gitleaks" "${BIN_DIR}/gitleaks"
	rm -rf "${tmp}"
	gitleaks version || true
}

install_zizmor() {
	if command -v zizmor >/dev/null 2>&1; then
		zizmor --version || true
		return 0
	fi
	local url="https://github.com/zizmorcore/zizmor/releases/download/v${ZIZMOR_VERSION}/zizmor-x86_64-unknown-linux-gnu.tar.gz"
	local tmp
	tmp="$(mktemp -d)"
	curl -fsSL -o "${tmp}/zizmor.tgz" "${url}"
	tar -xzf "${tmp}/zizmor.tgz" -C "${tmp}"
	# tarball may contain nested path
	local bin
	bin="$(find "${tmp}" -type f -name zizmor | head -1)"
	install -m 0755 "${bin}" "${BIN_DIR}/zizmor"
	rm -rf "${tmp}"
	zizmor --version || true
}

install_hadolint() {
	if command -v hadolint >/dev/null 2>&1; then
		hadolint --version || true
		return 0
	fi
	local url="https://github.com/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-Linux-x86_64"
	curl -fsSL -o "${BIN_DIR}/hadolint" "${url}"
	chmod +x "${BIN_DIR}/hadolint"
	hadolint --version || true
}

install_all() {
	install_trivy
	install_grype
	install_osv
	install_gitleaks
	install_zizmor
	install_hadolint
}

case "${1:-all}" in
	all) install_all ;;
	trivy) install_trivy ;;
	grype) install_grype ;;
	osv) install_osv ;;
	gitleaks) install_gitleaks ;;
	zizmor) install_zizmor ;;
	hadolint) install_hadolint ;;
	*)
		echo "usage: $0 [all|trivy|grype|osv|gitleaks|zizmor|hadolint]" >&2
		exit 2
		;;
esac
