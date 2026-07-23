# Public Kind release test

This path validates the released Anvil Agents operator without building source,
providing credentials, or consuming a model API. It installs the public v0.1.1
OCI chart with its controller image pinned to the published linux/amd64 digest,
then creates two immutable `AgentRun` records. The first writes a marker to an
`AgentDataVolume`; the second proves that a different Job can read it.

From the repository root:

```bash
./hack/install-judge-prerequisites.sh --install \
  --bin-dir "${HOME}/.local/bin"
export PATH="${HOME}/.local/bin:${PATH}"
./hack/install-judge-prerequisites.sh --check
./hack/test-judge-kind.sh
```

Prerequisites are a running Docker Engine and a Linux amd64 environment. The
non-root installer supplies checksum-pinned Kind, kubectl, and Helm binaries;
use its `--check` mode to validate an existing installation without changing
anything. The test creates `anvil-agents-judge` by default, leaves it available
for inspection, and uses the isolated kubeconfig path printed at the end
instead of changing the caller's kubectl configuration. Override the name with
`ANVIL_AGENTS_JUDGE_CLUSTER`.

Inspect the result:

```bash
KUBECONFIG=/tmp/anvil-agents-judge-${UID}-anvil-agents-judge.kubeconfig \
kubectl --context kind-anvil-agents-judge \
  --namespace anvil-agents-judge get agentruns,jobs,pods,pvc
KUBECONFIG=/tmp/anvil-agents-judge-${UID}-anvil-agents-judge.kubeconfig \
kubectl --context kind-anvil-agents-judge \
  --namespace anvil-agents-judge get agentrun judge-read-002 -o yaml
```

Use the explicit cleanup option when finished:

```bash
./hack/test-judge-kind.sh --cleanup
```

This test proves installation, CRD reconciliation, composition resolution,
isolated Job execution, structured terminal status, append-only runs, and PVC
persistence. It intentionally does **not** make an authenticated model call or
claim to prove GPT-5.6 use.

Successful output ends with `Public Kind judge test passed` and identifies the
two runs plus `proof: phase=Succeeded backend=custom storage=retained`. If the
test fails, leave the cluster running and inspect the printed kubeconfig,
AgentRun, Job, Pod, and PVC. Common causes are an unavailable Docker daemon,
blocked GHCR or Docker Hub access, or a conflicting cluster name; select a
different name with `ANVIL_AGENTS_JUDGE_CLUSTER` before retrying.
