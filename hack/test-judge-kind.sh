#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${ANVIL_AGENTS_JUDGE_CLUSTER:-anvil-agents-judge}"
kube_context="kind-${cluster_name}"
namespace=anvil-agents-judge
kind_node_image=kindest/node:v1.32.2@sha256:f226345927d7e348497136874b6d207e0b32cc52154ad8323129352923a3142f
export KIND_EXPERIMENTAL_PROVIDER=docker
[[ "${cluster_name}" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || {
	echo "ANVIL_AGENTS_JUDGE_CLUSTER must contain only lowercase letters, digits, dots, and hyphens" >&2
	exit 2
}
judge_kubeconfig="/tmp/anvil-agents-judge-${UID}-${cluster_name}.kubeconfig"
ownership_marker="/tmp/anvil-agents-judge-${UID}-${cluster_name}.owner"
export KUBECONFIG="${judge_kubeconfig}"
chart_ref=oci://ghcr.io/hazyforge/charts/anvil-agents
chart_version=0.1.1
chart_digest=sha256:16a867c09b21287029797e43ba42cb633277ed1d3eb8d764dc3516f00a4c970c
controller_image=ghcr.io/hazyforge/anvil-agents@sha256:387b37dfa3c1940b858dc02dbd93ffd29b497cfeb24dcf35d9b9bb8421ddaffa
cluster_created=false
chart_temp_dir=""
chart_archive=""

cleanup_chart_archive() {
	if [[ -n "${chart_archive}" ]]; then
		rm -f -- "${chart_archive}"
	fi
	if [[ -n "${chart_temp_dir}" ]]; then
		rmdir -- "${chart_temp_dir}" 2>/dev/null || true
	fi
}

diagnose_failure() {
	local exit_code=$?
	if [[ "${exit_code}" -ne 0 && "${cluster_created}" == true ]]; then
		echo "Judge test failed; retaining ${cluster_name} for inspection with KUBECONFIG=${judge_kubeconfig}" >&2
		kubectl --context "${kube_context}" get pods --all-namespaces --output=wide >&2 || true
		kubectl --context "${kube_context}" get agentruns --all-namespaces --output=wide >&2 || true
	fi
	cleanup_chart_archive
	return "${exit_code}"
}

trap diagnose_failure EXIT

usage() {
	cat <<'EOF'
Usage: hack/test-judge-kind.sh [--cleanup]

With no arguments, create a Kind cluster, install the public v0.1.1 OCI chart,
and prove two immutable AgentRuns share persistent state. No source build or
model credential is required. The cluster is left running for inspection.

Options:
  --cleanup  Delete only the named judge cluster and exit.
  -h, --help Show this help.

Environment:
  ANVIL_AGENTS_JUDGE_CLUSTER  Kind cluster name (default: anvil-agents-judge)
EOF
}

case "${1:-}" in
	"") ;;
	--cleanup)
		for command in kind kubectl grep; do
			command -v "${command}" >/dev/null 2>&1 || {
				echo "${command} is required" >&2
				exit 1
			}
		done
		if kind get clusters | grep -Fxq "${cluster_name}"; then
			[[ -f "${judge_kubeconfig}" && -f "${ownership_marker}" ]] || {
				echo "refusing to delete ${cluster_name}: judge ownership files are missing" >&2
				exit 1
			}
			[[ "$(<"${ownership_marker}")" == anvil-agents-public-judge-v1 ]] || {
				echo "refusing to delete ${cluster_name}: judge ownership marker is invalid" >&2
				exit 1
			}
			fixture_label="$(kubectl --context "${kube_context}" get namespace "${namespace}" --output=jsonpath='{.metadata.labels.control\.anvil\.hazyforge\.io/judge-fixture}' 2>/dev/null || true)"
			[[ "${fixture_label}" == true ]] || {
				echo "refusing to delete ${cluster_name}: judge namespace ownership label is missing" >&2
				exit 1
			}
			kind delete cluster --name "${cluster_name}" --kubeconfig "${judge_kubeconfig}"
		else
			printf 'Kind cluster %s does not exist\n' "${cluster_name}"
		fi
		rm -f -- "${judge_kubeconfig}" "${ownership_marker}"
		exit 0
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
esac

"${root_dir}/hack/install-judge-prerequisites.sh" --check

if kind get clusters | grep -Fxq "${cluster_name}"; then
	echo "Kind cluster ${cluster_name} already exists; choose a fresh name with ANVIL_AGENTS_JUDGE_CLUSTER" >&2
	exit 1
fi

kind create cluster --name "${cluster_name}" --kubeconfig "${judge_kubeconfig}" \
	--image "${kind_node_image}" --wait 120s
cluster_created=true
printf '%s\n' anvil-agents-public-judge-v1 >"${ownership_marker}"
actual_node_image="$(docker inspect --format '{{.Config.Image}}' "${cluster_name}-control-plane")"
[[ "${actual_node_image}" == "${kind_node_image}" ]] || {
	echo "Kind node image is ${actual_node_image}, want ${kind_node_image}" >&2
	exit 1
}
kubelet_version="$(kubectl --context "${kube_context}" get nodes --output=jsonpath='{.items[0].status.nodeInfo.kubeletVersion}')"
[[ "${kubelet_version}" == v1.32.2 ]] || {
	echo "Kind kubelet is ${kubelet_version}, want v1.32.2" >&2
	exit 1
}
kubectl --context "${kube_context}" create namespace "${namespace}" --save-config >/dev/null
kubectl --context "${kube_context}" label namespace "${namespace}" \
	control.anvil.hazyforge.io/judge-fixture=true --overwrite >/dev/null

chart_temp_dir="$(mktemp -d /tmp/anvil-agents-judge-chart.XXXXXX)"
chart_archive="${chart_temp_dir}/anvil-agents-${chart_version}.tgz"
pulled_chart="$(helm pull "${chart_ref}" --version "${chart_version}" --destination "${chart_temp_dir}" 2>&1)"
grep -Fq "Digest: ${chart_digest}" <<<"${pulled_chart}" || {
	echo "public chart digest did not match ${chart_digest}" >&2
	exit 1
}
[[ -f "${chart_archive}" ]] || {
	echo "Helm did not write the expected chart archive ${chart_archive}" >&2
	exit 1
}

helm --kube-context "${kube_context}" upgrade --install anvil-agents "${chart_archive}" \
	--namespace anvil-agents-system \
	--create-namespace \
	--set-string "image.reference=${controller_image}" \
	--wait \
	--timeout 3m

kubectl --context "${kube_context}" apply \
	--filename "${root_dir}/examples/judge-kind/manifests.yaml"
kubectl --context "${kube_context}" wait --namespace "${namespace}" \
	--for=condition=Ready=true --timeout=3m agentrun/judge-write-001
kubectl --context "${kube_context}" wait --namespace "${namespace}" \
	--for=condition=Ready=true --timeout=2m agentdatavolume/judge-state

crd_count="$(kubectl --context "${kube_context}" get crd --output=name | grep -c 'control\.anvil\.hazyforge\.io')"
[[ "${crd_count}" -eq 9 ]] || {
	echo "public v0.1.1 chart installed ${crd_count} Anvil Agents CRDs, want 9" >&2
	exit 1
}

first_phase="$(kubectl --context "${kube_context}" get agentrun judge-write-001 --namespace "${namespace}" --output=jsonpath='{.status.phase}')"
first_decision="$(kubectl --context "${kube_context}" get agentrun judge-write-001 --namespace "${namespace}" --output=jsonpath='{.status.decision.summary}')"
[[ "${first_phase}" == Succeeded && "${first_decision}" == *storage=created* ]] || {
	echo "first AgentRun did not report successful marker creation: phase=${first_phase} decision=${first_decision}" >&2
	exit 1
}

kubectl --context "${kube_context}" apply \
	--filename "${root_dir}/examples/judge-kind/verify-run.yaml"
kubectl --context "${kube_context}" wait --namespace "${namespace}" \
	--for=condition=Ready=true --timeout=3m agentrun/judge-read-002

phase="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.phase}')"
backend="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.backend}')"
decision="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.decision.summary}')"
harness_profile="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.resolvedComposition.harnessProfileRef.name}')"
skill_set="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.resolvedComposition.skillSetRefs[0].name}')"
effective_digest="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.resolvedComposition.effectiveDigest}')"
payload_digest="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.resolvedComposition.payloadDigest}')"
claim_name="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.dataVolumes[0].claimName}')"
job_name="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.jobRef.name}')"
pod_name="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.runnerPodRef.name}')"
runner_image="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.status.image}')"
controller_actual="$(kubectl --context "${kube_context}" get deployment --namespace anvil-agents-system --selector app.kubernetes.io/name=anvil-agents --output=jsonpath='{.items[0].spec.template.spec.containers[0].image}')"
pvc_phase="$(kubectl --context "${kube_context}" get pvc agent-data-judge-state --namespace "${namespace}" --output=jsonpath='{.status.phase}')"
pvc_capacity="$(kubectl --context "${kube_context}" get pvc agent-data-judge-state --namespace "${namespace}" --output=jsonpath='{.status.capacity.storage}')"

[[ "${phase}" == Succeeded && "${backend}" == custom ]] || {
	echo "second AgentRun phase/backend is ${phase}/${backend}, want Succeeded/custom" >&2
	exit 1
}
[[ "${decision}" == *storage=retained* ]] || {
	echo "second AgentRun did not prove PVC persistence: ${decision}" >&2
	exit 1
}
[[ "${harness_profile}" == judge-runtime && "${skill_set}" == judge-contract ]] || {
	echo "resolved composition is harness=${harness_profile} skillSet=${skill_set}" >&2
	exit 1
}
[[ "${effective_digest}" == sha256:* && "${payload_digest}" == sha256:* ]] || {
	echo "resolved composition digests are incomplete" >&2
	exit 1
}
[[ "${claim_name}" == agent-data-judge-state ]] || {
	echo "resolved PVC is ${claim_name}, want agent-data-judge-state" >&2
	exit 1
}
[[ -n "${job_name}" && -n "${pod_name}" ]] || {
	echo "AgentRun did not record both Job and Pod references" >&2
	exit 1
}
[[ "${runner_image}" == docker.io/library/busybox@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028 ]] || {
	echo "resolved runner image is ${runner_image}" >&2
	exit 1
}
[[ "${controller_actual}" == "${controller_image}" ]] || {
	echo "deployed controller image is ${controller_actual}, want ${controller_image}" >&2
	exit 1
}
[[ "${pvc_phase}" == Bound && "${pvc_capacity}" == 64Mi ]] || {
	echo "PVC state is ${pvc_phase}/${pvc_capacity}, want Bound/64Mi" >&2
	exit 1
}
kubectl --context "${kube_context}" wait --namespace "${namespace}" \
	--for=condition=Complete --timeout=60s "job/${job_name}" >/dev/null
job_logs="$(kubectl --context "${kube_context}" logs "job/${job_name}" --namespace "${namespace}" --container agent)"
grep -Fq 'judge-harness payload=valid storage=retained' <<<"${job_logs}" || {
	echo "second Job logs did not contain the retained-storage proof" >&2
	exit 1
}
grep -Fq 'ANVIL_AGENT_RUN_STATUS_JSON=' <<<"${job_logs}" || {
	echo "second Job logs did not contain structured status" >&2
	exit 1
}
if patch_error="$(kubectl --context "${kube_context}" patch agentrun judge-read-002 --namespace "${namespace}" \
	--type=merge --patch '{"spec":{"prompt":"mutation must be rejected"}}' 2>&1)"; then
	echo "append-only AgentRun unexpectedly accepted a spec mutation" >&2
	exit 1
fi
grep -Fq 'AgentRun spec is immutable; create a new run instead' <<<"${patch_error}" || {
	echo "AgentRun patch failed without the expected immutability validation: ${patch_error}" >&2
	exit 1
}
unchanged_prompt="$(kubectl --context "${kube_context}" get agentrun judge-read-002 --namespace "${namespace}" --output=jsonpath='{.spec.prompt}')"
[[ "${unchanged_prompt}" == 'Create a new append-only run and prove it reads state written by the first run.' ]] || {
	echo "AgentRun prompt changed despite immutable-spec validation: ${unchanged_prompt}" >&2
	exit 1
}

printf '\nPublic Kind judge test passed\n'
printf '  chart: %s:%s (%s)\n' "${chart_ref}" "${chart_version}" "${chart_digest}"
printf '  controller: %s\n' "${controller_image}"
printf '  runs: judge-write-001 -> judge-read-002\n'
printf '  proof: phase=%s backend=%s storage=retained\n' "${phase}" "${backend}"
printf '  kubernetes: %s (%s)\n' "${kubelet_version}" "${actual_node_image}"
printf '  kubeconfig: %s\n' "${judge_kubeconfig}"
printf '  inspect: KUBECONFIG=%s kubectl --context %s --namespace %s get agentruns,jobs,pods,pvc\n' "${judge_kubeconfig}" "${kube_context}" "${namespace}"
printf '  cleanup: ANVIL_AGENTS_JUDGE_CLUSTER=%s %s/hack/test-judge-kind.sh --cleanup\n' "${cluster_name}" "${root_dir}"
