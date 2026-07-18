#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cluster_name="${ANVIL_AGENTS_KIND_CLUSTER:-anvil-agents}"
kube_context="kind-${cluster_name}"
original_context="$(kubectl config current-context 2>/dev/null || true)"

for command in docker kind kubectl helm; do
	command -v "${command}" >/dev/null 2>&1 || {
		echo "${command} is required" >&2
		exit 1
	}
done

if ! kind get clusters | grep -Fxq "${cluster_name}"; then
	kind create cluster --name "${cluster_name}"
	if [[ -n "${original_context}" && "${original_context}" != "${kube_context}" ]]; then
		kubectl config use-context "${original_context}" >/dev/null
	fi
fi

ANVIL_AGENTS_IMAGE_PREFIX="" "${root_dir}/hack/build-images.sh" --component controller --tag dev
docker build --tag anvil-agents-demo:dev "${root_dir}/examples/quickstart"
kind load docker-image --name "${cluster_name}" anvil-agents:dev anvil-agents-demo:dev

helm --kube-context "${kube_context}" upgrade --install anvil-agents "${root_dir}/charts/anvil-agents" \
	--namespace anvil-agents-system \
	--create-namespace \
	--set-string image.repository=anvil-agents \
	--set-string image.tag=dev \
	--set image.pullPolicy=Never \
	--wait \
	--timeout 2m

# Reused clusters may already run an older digest behind the same local :dev
# tag. Restart after loading images so this execution proves the current build.
kubectl --context "${kube_context}" rollout restart deployment \
	--namespace anvil-agents-system \
	--selector app.kubernetes.io/name=anvil-agents
kubectl --context "${kube_context}" rollout status deployment \
	--namespace anvil-agents-system \
	--selector app.kubernetes.io/name=anvil-agents \
	--timeout=2m

if kubectl --context "${kube_context}" get namespace agents-quickstart >/dev/null 2>&1; then
	# AgentRuns are append-only records. Recreate only this deterministic demo
	# identity so a reused Kind cluster exercises the current example contract.
	kubectl --context "${kube_context}" delete agentrun demo-001 \
		--namespace agents-quickstart \
		--ignore-not-found \
		--wait=true
fi
kubectl --context "${kube_context}" apply --filename "${root_dir}/examples/quickstart/manifests.yaml"
kubectl --context "${kube_context}" wait --namespace agents-quickstart \
	--for=condition=Ready=true \
	--timeout=2m \
	agentdatavolume/demo-state
kubectl --context "${kube_context}" wait --namespace agents-quickstart \
	--for=condition=Ready=true \
	--timeout=2m \
	agentrun/demo-001
kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart \
	--output=custom-columns=NAME:.metadata.name,PHASE:.status.phase,BACKEND:.status.backend,JOB:.status.jobRef.name

echo "Quickstart completed. Remove the Kind cluster with: kind delete cluster --name ${cluster_name}"
