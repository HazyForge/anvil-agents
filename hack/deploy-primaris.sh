#!/usr/bin/env bash
# Deploy the local anvil-agents chart into the Anvil Primaris consumer overlay
# using Docker-built images already pinned in deploy.yaml. No GitHub Actions.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
values_file="${repo_root}/.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml"
chart_dir="${repo_root}/charts/anvil-agents"
namespace="anvil-agents-system"
# Matches the remote-helm ApplicationSet name pattern: <namespace-dir>-chart
release_name="anvil-agents-system-chart"
fullname_override="anvil-agents"
kube_context=""
dry_run="false"
wait="true"
timeout="15m"
regenerate_manifests="false"
extra_helm_args=()

usage() {
	cat <<'EOF'
Deploy Anvil Agents to the Anvil Primaris cluster from the local chart + overlay.

Usage:
  ./hack/deploy-primaris.sh [options]

This is the fast path after a local Docker image publish. It does not use
GitHub Actions. CRDs are applied from the chart when crds.install=true in the
Primaris deploy overlay (default).

Options:
  --values FILE          Primaris values overlay. Default:
                         .hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml
  --chart DIR            Chart path. Default: charts/anvil-agents
  --namespace NS         Target namespace. Default: anvil-agents-system
  --release-name NAME    Helm release name. Default: anvil-agents-system-chart
                         (matches Argo remote-helm Application name)
  --fullname-override N  Helm fullnameOverride. Default: anvil-agents
  --context CTX          kubectl/helm --kube-context
  --timeout DURATION     Helm --timeout. Default: 15m
  --no-wait              Do not pass --wait
  --manifests            Run make manifests first (refresh embedded CRDs)
  --dry-run              Helm client dry-run
  --set KEY=VAL          Extra helm --set (repeatable)
  -h, --help             Show this help

Examples:
  # After pinning digests, apply chart+CRDs to the current kube context:
  ./hack/deploy-primaris.sh --manifests

  # Dry-run against a named context:
  ./hack/deploy-primaris.sh --context anvil-primaris --dry-run
EOF
}

require_value() {
	if [[ -z "${2:-}" || "${2:-}" == --* ]]; then
		echo "$1 requires a value" >&2
		exit 2
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--values)
			require_value "$1" "${2:-}"
			values_file="$2"
			shift 2
			;;
		--chart)
			require_value "$1" "${2:-}"
			chart_dir="$2"
			shift 2
			;;
		--namespace)
			require_value "$1" "${2:-}"
			namespace="$2"
			shift 2
			;;
		--release-name)
			require_value "$1" "${2:-}"
			release_name="$2"
			shift 2
			;;
		--fullname-override)
			require_value "$1" "${2:-}"
			fullname_override="$2"
			shift 2
			;;
		--context)
			require_value "$1" "${2:-}"
			kube_context="$2"
			shift 2
			;;
		--timeout)
			require_value "$1" "${2:-}"
			timeout="$2"
			shift 2
			;;
		--no-wait)
			wait="false"
			shift
			;;
		--manifests)
			regenerate_manifests="true"
			shift
			;;
		--dry-run)
			dry_run="true"
			shift
			;;
		--set)
			require_value "$1" "${2:-}"
			extra_helm_args+=(--set "$2")
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

command -v helm >/dev/null 2>&1 || { echo "helm is required" >&2; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 1; }
[[ -f "${values_file}" ]] || { echo "values file does not exist: ${values_file}" >&2; exit 2; }
[[ -d "${chart_dir}" ]] || { echo "chart directory does not exist: ${chart_dir}" >&2; exit 2; }
[[ -f "${chart_dir}/Chart.yaml" ]] || { echo "not a chart directory: ${chart_dir}" >&2; exit 2; }

if [[ "${regenerate_manifests}" == "true" ]]; then
	make -C "${repo_root}" manifests
fi

helm_args=(
	upgrade --install "${release_name}" "${chart_dir}"
	--namespace "${namespace}"
	--create-namespace
	--values "${chart_dir}/values.yaml"
	--values "${values_file}"
	--set "fullnameOverride=${fullname_override}"
	--timeout "${timeout}"
)
if [[ -n "${kube_context}" ]]; then
	helm_args+=(--kube-context "${kube_context}")
fi
if [[ "${wait}" == "true" ]]; then
	helm_args+=(--wait)
fi
if [[ "${dry_run}" == "true" ]]; then
	helm_args+=(--dry-run=client)
fi
if ((${#extra_helm_args[@]})); then
	helm_args+=("${extra_helm_args[@]}")
fi

# Surface image pins for operators before helm mutates the cluster.
if command -v rg >/dev/null 2>&1; then
	echo "Deploying with image pins from ${values_file}:"
	rg -n 'reference:|codex:|openCode:|hermesAgent:|openClaw:|grokBuild:|piAgent:|crds:' "${values_file}" || true
fi

printf '+ helm'
printf ' %q' "${helm_args[@]}"
printf '\n'
helm "${helm_args[@]}"

if [[ "${dry_run}" != "true" ]]; then
	kc_args=()
	if [[ -n "${kube_context}" ]]; then
		kc_args+=(--context "${kube_context}")
	fi
	echo "CRDs present after deploy:"
	kubectl "${kc_args[@]}" get crd 2>/dev/null | rg 'anvil\.hazyforge\.io|control\.anvil' || true
	echo "Controller rollout:"
	kubectl "${kc_args[@]}" -n "${namespace}" get deploy,pods -l 'app.kubernetes.io/name=anvil-agents' 2>/dev/null \
		|| kubectl "${kc_args[@]}" -n "${namespace}" get deploy,pods 2>/dev/null || true
fi

printf 'Deployed Helm release %s in namespace %s\n' "${release_name}" "${namespace}"
printf 'Source chart: %s\nValues: %s\n' "${chart_dir}" "${values_file}"
