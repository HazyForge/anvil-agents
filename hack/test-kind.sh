#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

"${root_dir}/examples/quickstart/run.sh"

phase="$(kubectl get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.phase}')"
decision="$(kubectl get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.decision.summary}')"
[[ "${phase}" == "Succeeded" ]] || {
	echo "quickstart AgentRun phase is ${phase}, want Succeeded" >&2
	exit 1
}
[[ -n "${decision}" ]] || {
	echo "quickstart AgentRun did not record a structured decision" >&2
	exit 1
}

helm uninstall anvil-agents --namespace anvil-agents-system >/dev/null
crd_count="$(kubectl get crd --output=name | rg -c 'control\.anvil\.hazyforge\.io')"
[[ "${crd_count}" -eq 9 ]] || {
	echo "Helm uninstall retained ${crd_count} agent CRDs, want 9" >&2
	exit 1
}
kubectl get agentrun demo-001 --namespace agents-quickstart >/dev/null

helm upgrade --install anvil-agents "${root_dir}/charts/anvil-agents" \
	--namespace anvil-agents-system \
	--create-namespace \
	--set image.pullPolicy=Never \
	--wait \
	--timeout 2m >/dev/null
kubectl wait --namespace anvil-agents-system \
	--for=condition=Available=true \
	--timeout=60s \
	deployment --all >/dev/null

printf 'Kind execution and Helm uninstall-retention contract passed\n'
