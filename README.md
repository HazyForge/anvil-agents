# Anvil Agents

**Product site:** [anvil-agents.hazyforge.io](https://anvil-agents.hazyforge.io)
· **Docs:** [anvil-agents.hazyforge.io/docs](https://anvil-agents.hazyforge.io/docs)
· **Console:** [agents.anvil.hazyforge.io](https://agents.anvil.hazyforge.io)

Anvil Agents is an open-source Hazy Forge project for durable, distributed
agent loops on Kubernetes. It turns independent, heavyweight agent work into
schedulable Jobs, so repository builds, test suites, security analysis,
indexing, migrations, and long research loops can use the CPU and memory of a
cluster instead of competing on one workstation. Declarative runs, composable
profiles, skill sets, tool sets, schedules, and event streams become isolated
Jobs, and each run can select Codex, OpenCode, Hermes Agent, OpenClaw, Grok
Build, Pi, or a custom harness.

```text
Run/Profile/Harness/Skills/Schedule      Kubernetes Job
                 |                            |
                 v                            v
          anvil-agents controller -> selected harness -> tools and services
                 |                            |
                 +------- status/archive <----+
                 |
                 +------- optional OIDC read API and live SSE logs
```

Kubernetes provides scheduling, isolation, and distribution. Explicit
`AgentDataVolume` resources and external services provide durable memory.
Run profiles provide reusable role and policy. Harness, skill, and tool sets
let operators change the runtime, instructions, and external integrations on
independent lifecycles. The operator does not require Anvil Primaris, Anvil
Hub, or another Hazy Forge control plane.

The branded API group `control.anvil.hazyforge.io/v1alpha1` is retained as a
stable API identity and for migration compatibility. Application and target
references are opaque scope keys, not dependencies on other CRDs.

## Test It Without Rebuilding

On Linux amd64 with Docker Engine running, install the pinned user-space tools
and run the test:

```bash
./hack/install-judge-prerequisites.sh --install \
  --bin-dir "${HOME}/.local/bin"
export PATH="${HOME}/.local/bin:${PATH}"
./hack/install-judge-prerequisites.sh --check
./hack/test-judge-kind.sh
```

The prerequisite installer downloads the certified Kind, kubectl, and Helm
versions from their official release hosts, verifies pinned SHA-256 checksums,
and never uses `sudo`. It checks Docker but does not install or reconfigure the
privileged host service. The judge script builds nothing and needs no API key.
It creates a dedicated Kind cluster, installs the public v0.1.1 OCI chart with
the controller pinned to its published digest, and proves two append-only
`AgentRun` Jobs share durable PVC state while producing structured terminal
status. [Getting started](docs/getting-started.md) covers check-only mode,
custom installation directories, expected output, inspection, troubleshooting,
and cleanup.

The validated release platform is Linux amd64, Kubernetes 1.30 or newer, and
Helm 3.14 or newer. Arm64 images and older Kubernetes releases are not part of
the certified submission contract.

## What It Owns

- `AgentRun`: one append-only execution record and one harness Job.
- `AgentRunProfile`: reusable role, scope, policy, and composition defaults.
- `AgentHarnessProfile`: reusable backend and Kubernetes execution envelope.
- `AgentSkillSet`: reusable skills and delegated personas.
- `AgentToolSet`: reusable external tool setup and verification contracts.
- `AgentSchedule`: interval and manual run creation across named templates.
- `AgentRunControl`: cluster-wide pause and concurrency policy by scope key.
- `AgentDataVolume` and `VolumeProfile`: explicit durable PVC-backed state.
- `AgentAuthSession`: operator-driven durable Codex auth reauth/logout sessions.
- `AdverseSituation`: deduplicated event buffers with optional responders.
- `AdverseSignal`: immutable, provider-neutral adverse evidence from any app.
- `anvil-agents-api`: optional OIDC-protected summaries, live SSE logs, and the
  read-only **Anvil Agents Console** SPA at `/` (see `web/console/`).
- `anvil-agentctl`: Kubernetes-authenticated creation and diagnosis of runs,
  Codex durable-home reauth, and in-pod status reporting without raw manifests.
- Collector-neutral Job and Pod labels for external log and telemetry
  pipelines.

"Multi-harness" means every run uses one adapter behind a common payload,
tool-bootstrap, and status contract. Schedules can distribute independent runs
across harnesses and nodes. It does not mean the controller creates an agent
mesh or a shared live conversation; cross-run state must be explicit.

## Use The Whole Cluster

Heavy agent work is often more than model inference. Agents compile large
repositories, run integration suites, scan dependency graphs, build images,
query local indexes, and process large evidence sets. Anvil Agents lets an
operator turn those independent workloads into a controlled cluster queue:

- Harness profiles declare CPU, memory, and ephemeral-storage requests and
  limits instead of inheriting the capacity of the machine that submitted the
  run.
- Node selectors, affinity, and tolerations place build, memory-heavy, custom
  accelerator, or storage-local harnesses on the machines prepared for them;
  accelerators also require a device plugin and extended-resource request.
- Multiple `AgentRun` objects and `AgentSchedule` with `Allow` can execute
  parallel lanes, bounded by schedule and application concurrency controls.
- Kubernetes performs placement and isolation, while run status and the OIDC
  stream let users observe work without SSH or `kubectl` access.

One `AgentRun` is one Pod on one node; the operator scales heavy work
horizontally by scheduling many independent runs across machines. A harness
that needs to split one computation across several nodes must use an explicit
distributed compute service. See
[Distributed Workloads](docs/distributed-workloads.md) for placement,
concurrency, storage, and capacity examples.

## Try It Locally

The credential-free Kind quickstart builds the controller and a minimal custom
harness, installs the chart, binds and mounts durable state, creates an
`AgentRun`, and waits for structured success:

```bash
./examples/quickstart/run.sh
```

See [Getting Started](docs/getting-started.md) for prerequisites, manual steps,
existing-cluster installation, and cleanup.

Operators with Kubernetes credentials can use `anvil-agentctl` to create one
append-only run from a profile, inspect status, stream the verified runner
container, and aggregate controller, Job, Pod, and Event evidence. See
[AgentRun CLI](docs/cli.md).

## Build Without GitHub Actions

Local scripts and Make targets are the canonical build and validation entry
points:

```bash
make verify
make images
make kind-e2e

./hack/build-images.sh --component controller --tag test
./hack/build-images.sh \
  --prefix registry.example.com/team \
  --tag v0.1.1 \
  --push

./hack/package-chart.sh --version 0.1.1

# Complete local publication from a clean checkout after docker login and
# helm registry login:
make release-local-all VERSION=vX.Y.Z REGISTRY_PREFIX=registry.example.com/team

# Update the first-party Anvil Primaris deploy overlay from the generated lock:
make release-pin-deploy VERSION=vX.Y.Z
```

`make images` builds the controller plus all six built-in runner images into
local Docker. The reusable script supports component selection, platforms,
cache import/export, multiple tags, custom registries, and fork-aware OCI
source metadata. Image pushes reject dirty worktrees unless explicitly
overridden. `make release-local-all` creates or verifies an annotated release
tag, pushes that tag, runs `publish-release.sh`, then creates the GitHub
Release page from the local chart package and image lock. The GitHub Release
step uses the GitHub API through `gh`, verifies that the remote tag already
exists, and does not use GitHub Actions minutes. If you only need the registry
and chart artifacts, run `make release-local`.

`publish-release.sh` runs `make verify` and `make kind-e2e`, publishes all
seven versioned images, verifies their immutable digests and source revision,
writes a digest lock, and pushes an OCI chart whose seven default image
references are pinned to that lock. `make release-pin-deploy` updates the
first-party Anvil Primaris overlay from that lock so the controller and built-in
runner defaults move together.

GitHub workflows use the same repository-owned build and test contracts and are
optional. The optional release workflow is manual-only for an existing `v*` tag.
It verifies the tag and Kind upgrade/install tests, then invokes
`publish-release.sh --skip-verification` so Actions and local publication
produce the same digest-locked chart. It does not run for tag pushes or
`master`, so the local GitHub Release step cannot accidentally start a second
publisher for the same version.

## Security Boundary

Run, run-profile, harness-profile, and skill-set authors can cause executable
code to run. Harness profiles can also choose a same-namespace ServiceAccount,
Secrets, resource placement, storage, and a custom command. Treat permission to
edit these resources as privileged workload execution. Use dedicated
namespaces, narrowly scoped runner identities, admission policy, and Secrets
created only for agents. `watchNamespaces` narrows the controller cache; it
does not narrow the chart's ClusterRole.

The OIDC API is a separate read-only workload and is disabled until an issuer,
audience, and explicit namespace authorization bindings are configured. See
[Security](docs/security.md) and
[Live AgentRun API](docs/live-agent-run-stream.md).

## Documentation

- [Architecture and multi-harness semantics](docs/architecture.md)
- [Distributed heavy workloads across machines](docs/distributed-workloads.md)
- [Composable profiles, harnesses, skill sets, tool sets, and overrides](docs/composition.md)
- [Getting started](docs/getting-started.md)
- [Harness contract and adapter matrix](docs/harnesses.md)
- [Knowledge bases, tools, and external services](docs/integrating-knowledge-and-tools.md)
- [AgentRun stdout, Alloy, and OpenTelemetry integration](docs/observability.md)
- [Connect any application or Kubernetes API to adverse streams](docs/integrating-adverse-sources.md)
- [Operations, upgrades, and uninstall](docs/operations.md)
- [PostgreSQL archive modes and retention](docs/archive.md)
- [Design roadmap and known alpha boundaries](docs/design-roadmap.md)
- [AgentRun API reference](docs/agent-run.md)
- [Create and diagnose runs with anvil-agentctl](docs/cli.md)
- [Migration from Anvil Primaris](docs/migration-from-anvil-primaris.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)

Hazy Forge uses this same open-source runtime for its own agent system. The
repository-local `.hazyforge/artifact-build.yaml` and `.hazyforge/tests.yaml`
files are maintainer build and test contracts. The
`.hazyforge/clusters/anvil-primaris/` tree is an optional Hazy Forge consumer
deployment with environment-specific identity, credentials, routing, storage,
placement, and image pins. None of those files is a chart default or runtime
dependency; other consumers provide their own Helm values or GitOps layer.

## Project Status

The API is `v1alpha1`. Pin chart and image versions, test upgrades against a
cluster backup, and expect compatible additions before a stable API release.
The initial v0.1 release contract targets Linux amd64. Source builds may work
on arm64, but arm64 is not yet part of the published release gate.

Licensed under Apache-2.0. See [Contributing](CONTRIBUTING.md),
[Security Policy](SECURITY.md), and [Support](SUPPORT.md).
