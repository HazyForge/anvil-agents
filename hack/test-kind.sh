#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${ANVIL_AGENTS_KIND_CLUSTER:-anvil-agents}"
kube_context="kind-${cluster_name}"

"${root_dir}/examples/quickstart/run.sh"

phase="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.phase}')"
decision="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.decision.summary}')"
harness_profile="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.resolvedComposition.harnessProfileRef.name}')"
skill_set="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.resolvedComposition.skillSetRefs[0].name}')"
effective_digest="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.resolvedComposition.effectiveDigest}')"
payload_digest="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.resolvedComposition.payloadDigest}')"
claim_name="$(kubectl --context "${kube_context}" get agentrun demo-001 --namespace agents-quickstart --output=jsonpath='{.status.dataVolumes[0].claimName}')"
[[ "${phase}" == "Succeeded" ]] || {
	echo "quickstart AgentRun phase is ${phase}, want Succeeded" >&2
	exit 1
}
[[ -n "${decision}" ]] || {
	echo "quickstart AgentRun did not record a structured decision" >&2
	exit 1
}
[[ "${harness_profile}" == "demo-runtime" && "${skill_set}" == "demo-contract" ]] || {
	echo "quickstart composition resolved harness=${harness_profile} skillSet=${skill_set}" >&2
	exit 1
}
[[ "${effective_digest}" == sha256:* && "${payload_digest}" == sha256:* ]] || {
	echo "quickstart composition digests are incomplete" >&2
	exit 1
}
[[ "${claim_name}" == "agent-data-demo-state" ]] || {
	echo "quickstart data volume resolved claim=${claim_name}" >&2
	exit 1
}

kubectl --context "${kube_context}" create namespace agents --dry-run=client --output=yaml | \
	kubectl --context "${kube_context}" apply --filename=- >/dev/null
while IFS= read -r manifest; do
	kubectl --context "${kube_context}" apply --dry-run=server --filename "${manifest}" >/dev/null
done < <(find "${root_dir}/config/samples" "${root_dir}/examples" \
	-type f -name '*.yaml' ! -name '*-values.yaml' ! -name 'zitadel-values.yaml' | sort)

helm --kube-context "${kube_context}" uninstall anvil-agents --namespace anvil-agents-system >/dev/null
crd_count="$(kubectl --context "${kube_context}" get crd --output=name | rg -c 'control\.anvil\.hazyforge\.io')"
[[ "${crd_count}" -eq 10 ]] || {
	echo "Helm uninstall retained ${crd_count} agent CRDs, want 10" >&2
	exit 1
}
for resource in \
	volumeprofile/demo-state \
	agentdatavolume/demo-state \
	agentharnessprofile/demo-runtime \
	agentskillset/demo-contract \
	agentrunprofile/demo \
	agentrun/demo-001; do
	kubectl --context "${kube_context}" get "${resource}" --namespace agents-quickstart >/dev/null
done
kubectl --context "${kube_context}" get persistentvolumeclaim/agent-data-demo-state --namespace agents-quickstart >/dev/null

helm --kube-context "${kube_context}" upgrade --install anvil-agents "${root_dir}/charts/anvil-agents" \
	--namespace anvil-agents-system \
	--create-namespace \
	--set image.pullPolicy=Never \
	--wait \
	--timeout 2m >/dev/null
kubectl --context "${kube_context}" wait --namespace anvil-agents-system \
	--for=condition=Available=true \
	--timeout=60s \
	deployment --all >/dev/null

kubectl --context "${kube_context}" delete agentrun demo-002 \
	--namespace agents-quickstart \
	--ignore-not-found \
	--wait=true >/dev/null
kubectl --context "${kube_context}" apply \
	--filename "${root_dir}/examples/quickstart/reinstall-run.yaml" >/dev/null
kubectl --context "${kube_context}" wait --namespace agents-quickstart \
	--for=condition=Ready=true \
	--timeout=2m \
	agentrun/demo-002 >/dev/null

printf 'Kind execution and Helm uninstall-retention contract passed\n'
