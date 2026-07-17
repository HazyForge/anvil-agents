# Anvil Agents

Anvil Agents is an open-source Hazy Forge project for durable, distributed
agent loops on Kubernetes. Declarative runs, composable profiles and skill
sets, schedules, and event streams become isolated Jobs, and each run can
select Codex, Hermes Agent, OpenClaw, Grok Build, Pi, or a custom harness.

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
Run profiles provide reusable role and policy. Harness profiles and skill sets
let operators change the runtime independently from backend-neutral
capabilities. The operator does not require Anvil Primaris, Anvil Hub, or
another Hazy Forge control plane.

The branded API group `control.anvil.hazyforge.io/v1alpha1` is retained as a
stable API identity and for migration compatibility. Application and target
references are opaque scope keys, not dependencies on other CRDs.

## What It Owns

- `AgentRun`: one append-only execution record and one harness Job.
- `AgentRunProfile`: reusable role, scope, policy, and composition defaults.
- `AgentHarnessProfile`: reusable backend and Kubernetes execution envelope.
- `AgentSkillSet`: reusable skills, tool contracts, and delegated personas.
- `AgentSchedule`: interval and manual run creation across named templates.
- `AgentRunControl`: cluster-wide pause and concurrency policy by scope key.
- `AgentDataVolume` and `VolumeProfile`: explicit durable PVC-backed state.
- `AdverseSituation`: deduplicated event buffers with optional responders.
- `anvil-agents-api`: optional OIDC-protected summaries and live SSE logs.

"Multi-harness" means every run uses one adapter behind a common payload,
tool-bootstrap, and status contract. Schedules can distribute independent runs
across harnesses and nodes. It does not mean the controller creates an agent
mesh or a shared live conversation; cross-run state must be explicit.

## Try It Locally

The credential-free Kind quickstart builds the controller and a minimal custom
harness, installs the chart, binds and mounts durable state, creates an
`AgentRun`, and waits for structured success:

```bash
./examples/quickstart/run.sh
```

See [Getting Started](docs/getting-started.md) for prerequisites, manual steps,
existing-cluster installation, and cleanup.

## Build Without GitHub Actions

Local scripts are the canonical build and validation entry points:

```bash
make verify
make images
make kind-e2e

./hack/build-images.sh --component controller --tag test
./hack/build-images.sh \
  --prefix registry.example.com/team \
  --tag v0.1.0 \
  --push

./hack/package-chart.sh --version 0.1.0
```

`make images` builds the controller plus all five built-in runner images into
local Docker. The reusable script supports component selection, platforms,
cache import/export, multiple tags, custom registries, and fork-aware OCI
source metadata. Image pushes reject dirty worktrees unless explicitly
overridden. GitHub workflows call the same repository-owned contract.
The optional release workflow runs only for a `v*` tag push or a manual rerun
of an existing tag. It runs the same verification and Kind upgrade/install
tests before publishing versioned images and an OCI chart; it never publishes
`latest` from `master`.

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
- [Composable profiles, harnesses, skill sets, and overrides](docs/composition.md)
- [Getting started](docs/getting-started.md)
- [Harness contract and adapter matrix](docs/harnesses.md)
- [Knowledge bases, tools, and external services](docs/integrating-knowledge-and-tools.md)
- [Operations, upgrades, and uninstall](docs/operations.md)
- [Design roadmap and known alpha boundaries](docs/design-roadmap.md)
- [AgentRun API reference](docs/agent-run.md)
- [Migration from Anvil Primaris](docs/migration-from-anvil-primaris.md)

Hazy Forge uses this same open-source repository for its own agent system. Its
deployment overlay remains under `.hazyforge/` as a real integration example,
not as a prerequisite for other installations.

## Project Status

The API is `v1alpha1`. Pin chart and image versions, test upgrades against a
cluster backup, and expect compatible additions before a stable API release.
The initial v0.1 release contract targets Linux amd64. Source builds may work
on arm64, but arm64 is not yet part of the published release gate.

Licensed under Apache-2.0. See [Contributing](CONTRIBUTING.md),
[Security Policy](SECURITY.md), and [Support](SUPPORT.md).
