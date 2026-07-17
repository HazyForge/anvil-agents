# Getting Started

The quickest reliable test is the credential-free Kind example. It proves the
controller, CRDs, Job creation, mounted payload, custom harness, logs, and
structured terminal status before a model provider is introduced.

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
Ready. It leaves the cluster available for inspection.

`make kind-e2e` runs the same execution, uninstalls the chart, proves all seven
CRDs and the completed run were retained, and reinstalls the controller.

```bash
kubectl get agentrun -n agents-quickstart demo-001 -o yaml
kubectl logs -n agents-quickstart \
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
./hack/package-chart.sh --version 0.1.0 --output dist
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

## Add A Real Harness

1. Create a dedicated agent namespace.
2. Create a runner ServiceAccount with only the APIs the task needs.
3. Create one provider credential Secret in that namespace.
4. Create an `AgentRunProfile` selecting the harness, digest-pinned image,
   ServiceAccount, Secret, timeout, and resource limits.
5. Create a run with a non-empty `sourceRef.kind` and `sourceRef.name`.
6. Observe status and logs, then enable schedules only after the manual path is
   bounded and repeatable.

The manifests under `examples/multi-harness` are an illustrative next step.
Provider Secret key names and authentication semantics belong to the selected
harness; review [Harnesses](harnesses.md) and its image documentation.

## Before Production

Read [Security](security.md). AgentRun writers can select executable code,
same-namespace identities and credentials, and placement. Use dedicated trust
domains, admission policy, resource quotas, network policy, immutable images,
backups, and deliberate retention. Enable the OIDC API only after configuring
a dedicated resource audience and explicit namespace bindings.
