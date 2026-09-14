#!/bin/sh
# Fake wsl.exe for Anvil Agents Desktop tests. It never forwards OIDC tokens.
set -eu

if [ -n "${ANVIL_WSL_RECORD:-}" ]; then
	{
		printf 'argv:'
		for arg in "$@"; do
			printf ' %s' "$arg"
		done
		printf '\n'
	} >> "${ANVIL_WSL_RECORD}"
fi

list=0
distro=""
while [ $# -gt 0 ]; do
	case "$1" in
	-l|--list)
		list=1
		shift
		;;
	-q|--quiet|-v|--verbose)
		shift
		;;
	-d|--distribution)
		distro="${2:-}"
		shift 2
		;;
	--exec|-e)
		shift
		break
		;;
	--)
		shift
		break
		;;
	*)
		break
		;;
	esac
done

if [ "$list" -eq 1 ]; then
	printf '%s\n' "${ANVIL_WSL_DISTROS:-Ubuntu-24.04}"
	exit 0
fi

export WSL_DISTRO_NAME="${distro:-${ANVIL_WSL_DEFAULT:-Ubuntu-24.04}}"
if [ -n "${ANVIL_WSL_DUMP_ENV:-}" ]; then
	printf 'AUTH=%s\n' "${AUTHORIZATION-unset}"
	printf 'KUBE=%s\n' "${KUBECONFIG-unset}"
fi
exec "$@"
