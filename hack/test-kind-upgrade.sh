#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline_ref="${ANVIL_AGENTS_UPGRADE_FROM_REF:-}"
cluster_name="${ANVIL_AGENTS_UPGRADE_KIND_CLUSTER:-anvil-agents-upgrade-$$}"
kube_context="kind-${cluster_name}"
tmp_dir="$(mktemp -d)"
created_cluster="false"
export KUBECONFIG="${tmp_dir}/kubeconfig"

cleanup() {
	local status=$?
	trap - EXIT
	set +e
	if [[ "${created_cluster}" == "true" ]]; then
		if ! kind delete cluster --name "${cluster_name}" >/dev/null; then
			echo "failed to delete kind cluster ${cluster_name}" >&2
			if [[ "${status}" -eq 0 ]]; then
				status=1
			fi
		fi
	fi
	if ! rm -rf -- "${tmp_dir}"; then
		echo "failed to remove upgrade-test temporary directory ${tmp_dir}" >&2
		if [[ "${status}" -eq 0 ]]; then
			status=1
		fi
	fi
	exit "${status}"
}
trap cleanup EXIT

for command in kind kubectl helm rg; do
	command -v "${command}" >/dev/null 2>&1 || {
		echo "${command} is required" >&2
		exit 1
	}
done
if [[ -n "${baseline_ref}" ]]; then
	for command in git tar; do
		command -v "${command}" >/dev/null 2>&1 || {
			echo "${command} is required when ANVIL_AGENTS_UPGRADE_FROM_REF is set" >&2
			exit 1
		}
	done
	git -C "${root_dir}" cat-file -e "${baseline_ref}^{commit}" 2>/dev/null || {
		echo "upgrade baseline ${baseline_ref} is not available; fetch it or clear ANVIL_AGENTS_UPGRADE_FROM_REF" >&2
		exit 1
	}
fi
if kind get clusters | grep -Fxq "${cluster_name}"; then
	echo "refusing to reuse upgrade-test cluster ${cluster_name}" >&2
	exit 1
fi

created_cluster="true"
kind create cluster --name "${cluster_name}" >/dev/null

if [[ -n "${baseline_ref}" ]]; then
	mkdir -p "${tmp_dir}/baseline"
	git -C "${root_dir}" archive "${baseline_ref}" charts/anvil-agents | tar -xf - -C "${tmp_dir}/baseline"
	helm --kube-context "${kube_context}" install anvil-agents "${tmp_dir}/baseline/charts/anvil-agents" \
		--namespace anvil-agents-system \
		--create-namespace \
		--set-string image.repository=upgrade-test-image \
		--set image.pullPolicy=Never >/dev/null
else
	# The portable default does not depend on an intermediate commit surviving a
	# squash merge. Install the current release without CRDs, then establish the
	# seven pre-composition and pre-AdverseSignal kinds using legacy-shaped
	# fixtures.
	helm --kube-context "${kube_context}" install anvil-agents "${root_dir}/charts/anvil-agents" \
		--namespace anvil-agents-system \
		--create-namespace \
		--set crds.install=false \
		--set-string image.repository=upgrade-test-image \
		--set image.pullPolicy=Never >/dev/null
	for crd in "${root_dir}"/config/crd/bases/*.yaml; do
		case "${crd}" in
			# Keep the pre-composition / pre-AdverseSignal / pre-AuthSession baseline
			# at seven kinds so the upgrade path still exercises CRD growth.
			*_agentharnessprofiles.yaml|*_agentskillsets.yaml|*_agenttoolsets.yaml|*_adversesignals.yaml|*_agentauthsessions.yaml) continue ;;
		esac
		resource="$(kubectl --context "${kube_context}" apply --filename "${crd}" --output=name)"
		kubectl --context "${kube_context}" label "${resource}" \
			app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
		kubectl --context "${kube_context}" annotate "${resource}" \
			meta.helm.sh/release-name=anvil-agents \
			meta.helm.sh/release-namespace=anvil-agents-system \
			--overwrite >/dev/null
	done
fi

baseline_count="$(kubectl --context "${kube_context}" get crd --output=name | rg -c 'control\.anvil\.hazyforge\.io')"
[[ "${baseline_count}" -eq 7 ]] || {
	echo "baseline rendered ${baseline_count} agent CRDs, want 7" >&2
	exit 1
}
kubectl --context "${kube_context}" apply --filename "${root_dir}/hack/fixtures/upgrade-v0.1.yaml" >/dev/null

helm --kube-context "${kube_context}" upgrade anvil-agents "${root_dir}/charts/anvil-agents" \
	--namespace anvil-agents-system \
	--set-string image.repository=upgrade-test-image \
	--set image.pullPolicy=Never >/dev/null

upgraded_count="$(kubectl --context "${kube_context}" get crd --output=name | rg -c 'control\.anvil\.hazyforge\.io')"
[[ "${upgraded_count}" -eq 12 ]] || {
	echo "upgrade rendered ${upgraded_count} agent CRDs, want 12" >&2
	exit 1
}
kubectl --context "${kube_context}" get agentrunprofile legacy-review --namespace agents-upgrade >/dev/null
kubectl --context "${kube_context}" get agentrun legacy-run --namespace agents-upgrade >/dev/null
kubectl --context "${kube_context}" apply --filename "${root_dir}/hack/fixtures/upgrade-composition.yaml" >/dev/null

helm --kube-context "${kube_context}" uninstall anvil-agents --namespace anvil-agents-system >/dev/null
retained_count="$(kubectl --context "${kube_context}" get crd --output=name | rg -c 'control\.anvil\.hazyforge\.io')"
[[ "${retained_count}" -eq 12 ]] || {
	echo "upgrade uninstall retained ${retained_count} agent CRDs, want 12" >&2
	exit 1
}
for resource in \
	agentrunprofile/legacy-review \
	agentrun/legacy-run \
	agentharnessprofile/custom-runtime \
	agentskillset/repository-review \
	agenttoolset/repository-tools; do
	kubectl --context "${kube_context}" get "${resource}" --namespace agents-upgrade >/dev/null
done

printf 'Seven-to-twelve CRD Helm upgrade contract passed\n'
