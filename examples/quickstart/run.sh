#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cluster_name="${ANVIL_AGENTS_KIND_CLUSTER:-anvil-agents}"

for command in docker kind kubectl helm; do
	command -v "${command}" >/dev/null 2>&1 || {
		echo "${command} is required" >&2
		exit 1
	}
done

if ! kind get clusters | grep -Fxq "${cluster_name}"; then
	kind create cluster --name "${cluster_name}"
fi

docker build --tag anvil-agents:dev --file "${root_dir}/Dockerfile" "${root_dir}"
docker build --tag anvil-agents-demo:dev "${root_dir}/examples/quickstart"
kind load docker-image --name "${cluster_name}" anvil-agents:dev anvil-agents-demo:dev

helm upgrade --install anvil-agents "${root_dir}/charts/anvil-agents" \
	--namespace anvil-agents-system \
	--create-namespace \
	--set-string image.repository=anvil-agents \
	--set-string image.tag=dev \
	--set image.pullPolicy=Never \
	--wait \
	--timeout 2m

kubectl apply --filename "${root_dir}/examples/quickstart/manifests.yaml"
kubectl wait --namespace agents-quickstart \
	--for=condition=Ready=true \
	--timeout=2m \
	agentrun/demo-001
kubectl get agentrun demo-001 --namespace agents-quickstart \
	--output=custom-columns=NAME:.metadata.name,PHASE:.status.phase,BACKEND:.status.backend,JOB:.status.jobRef.name

echo "Quickstart completed. Remove the Kind cluster with: kind delete cluster --name ${cluster_name}"
