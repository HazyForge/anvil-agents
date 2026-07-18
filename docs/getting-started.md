# Getting Started

The quickest reliable test is the credential-free Kind example. It proves the
controller, CRDs, PVC-backed state, Job creation, mounted payload, custom
harness, logs, and structured terminal status before a model provider is
introduced.

## Prerequisites

- Linux amd64 for the currently validated image set
- Kubernetes 1.30 or newer
- Helm 3.14 or newer
- Docker with BuildKit
- `kubectl`
- `ripgrep` (`rg`) for the full `make kind-e2e` retention check
- Kind for the local path
- Go 1.25 only when building the controller from source

The current release gate does not yet certify older Kubernetes versions or
published arm64 images.

## Credential-Free Kind Run

Install Kind, then run:

```bash
./examples/quickstart/run.sh
```

The source chart deliberately names local `:dev` images rather than
unpublished public artifacts. The script creates or reuses a cluster named
`anvil-agents`, builds
`anvil-agents:dev` and `anvil-agents-demo:dev`, loads both images, installs the
local chart, applies the example, and waits for `AgentRun/demo-001` to report
Ready. It also binds a small `AgentDataVolume`. Every Helm and kubectl operation
is pinned to `kind-anvil-agents` (or the selected
`ANVIL_AGENTS_KIND_CLUSTER`) rather than the ambient context. It leaves the
cluster available for inspection.

`make kind-e2e` first uses a disposable cluster to upgrade a portable seven-CRD
legacy-object baseline to the nine-CRD composition API without losing existing
objects. It then runs the current execution, validates every sample with
server-side dry-run, uninstalls the chart, proves all nine CRDs plus the run,
profiles, skill set, data volume, and PVC were retained, and reinstalls the
controller. A second run then executes through the retained composition and
storage objects. The quickstart also asserts the resolved harness/skill-set
refs, data-volume claim, and both composition digests.

```bash
kubectl --context kind-anvil-agents get agentrun -n agents-quickstart demo-001 -o yaml
kubectl --context kind-anvil-agents logs -n agents-quickstart \
  -l control.anvil.hazyforge.io/agent-run=demo-001
kind delete cluster --name anvil-agents
```

## Existing Cluster

For a source checkout, build and push all images to a registry reachable by
the cluster:

```bash
./hack/build-images.sh \
  --prefix registry.example.com/platform \
  --tag v0.1.0 \
  --push
```

Package the version-coupled chart locally when distributing a release bundle:

```bash
./hack/package-chart.sh \
  --version 0.1.0 \
  --output dist \
  --image-prefix registry.example.com/platform
```

Create a values file that sets `image.reference` and all five `runnerImages`.
Prefer digest references. Install the chart:

```bash
helm upgrade --install anvil-agents charts/anvil-agents \
  --namespace anvil-agents-system \
  --create-namespace \
  --values production-values.yaml \
  --wait
```

Until the first public GHCR/chart release exists, a checkout and locally built
images are the supported installation path. Repository visibility and artifact
publication are release-owner actions, not runtime configuration.

The optional GitHub release workflow accepts a `v*` tag push or a manual rerun
of an existing `v*` tag. It gates all six versioned images and the OCI chart on
`make verify` and `make kind-e2e`; pushes to `master` do not publish artifacts
or move a `latest` tag. These are distribution conveniences, not prerequisites
for the local scripts above.

## Add A Real Harness

1. Create a dedicated agent namespace.
2. Create a runner ServiceAccount with only the APIs the task needs.
3. Create one provider credential Secret in that namespace.
4. Create an `AgentHarnessProfile` selecting the adapter, digest-pinned image,
   ServiceAccount, provider Secret, timeout, and resource limits.
5. Create any backend-neutral `AgentSkillSet` capability packs.
6. Create an `AgentRunProfile` that composes the role, harness, and skill sets.
7. Create a run with a non-empty `sourceRef.kind` and `sourceRef.name`.
8. Observe status and logs, then enable schedules only after the manual path is
   bounded and repeatable.

The manifests under `examples/multi-harness` are an illustrative next step.
Provider Secret key names and authentication semantics belong to the selected
harness; review [Harnesses](harnesses.md) and its image documentation.

## Scale Across Workers

1. Make the selected runner images pullable by every eligible worker node.
2. Give each harness profile realistic CPU, memory, and ephemeral-storage
   requests plus placement compatible with its intended node pool.
3. Create at least two independent runs, or use `AgentSchedule` with `Allow`
   and an explicit concurrency cap above one.
4. When runs share an application key, have a cluster administrator raise the
   cluster-scoped `AgentRunControl` cap, or raise the Helm
   `applicationMaxConcurrentRuns` fallback from its default of one. The lowest
   positive value across matching controls wins.
5. Confirm placement with `kubectl get pods -n <namespace> -o wide`, then add
   worker capacity or configure cluster autoscaling as demand grows.

Each run stays on one node, but many runs can consume different machines at the
same time. Restrictive affinity, unavailable image architectures, namespace
quota, and local or shared `ReadWriteOnce` volumes can leave Jobs Pending or
serialize them. See [Distributed Workloads](distributed-workloads.md) before
enabling high concurrency.

## Before Production

Read [Security](security.md). Run, profile, harness-profile, and skill-set
writers can cause executable code to run; harness profiles additionally select
same-namespace identities, credentials, storage, and placement. Use dedicated
trust domains, admission policy, resource quotas, network policy, immutable
images, backups, and deliberate retention. Enable the OIDC API only after
configuring a dedicated resource audience and explicit namespace bindings.
