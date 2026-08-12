# Design Roadmap

The extracted v1alpha1 controller is useful today, but an open-source stable
release needs several contracts stronger than the compatibility API it began
with. These are product boundaries, not branding substitutions.

## Before Stable API

- **Execution policy**: a namespaced policy or admission contract for allowed
  harness classes, image digests, ServiceAccounts, Secret/PVC refs, security
  contexts, placement, custom commands, and network classes.
- **Earlier immutable acceptance**: Job materialization now snapshots profile,
  harness-profile, and skill-set object versions plus effective-spec and payload
  digests. A future acceptance phase should freeze queued work before it becomes
  launchable and should record resolved image and source artifact digests.
- **Workspace source**: provider-neutral Git HTTPS/SSH source with immutable
  revision, subdirectory, auth ref, resolved commit status, and fail-closed
  checkout behavior.
- **Harness registration**: evolve the closed built-in enum toward a
  `HarnessClass` contract with protocol version, capabilities, image digest,
  auth modes, and schema-validated provider configuration.
- **Context sources**: pinned Git, OCI, HTTP, ConfigMap, and protocol-specific
  adapters with checksums and strict payload size limits. GitHub skill files
  remain one adapter rather than the universal knowledge abstraction.
- **Storage reclaim policy**: make Retain/Delete explicit for controller-owned
  PVCs and test restore and uninstall flows.
- **Mandatory quotas**: namespace, scope, backend, provider, queue size, and
  fairness limits. Optional application metadata must not be the only spend
  boundary.
- **Launch diagnosis**: bounded startup time and explicit status for missing
  ServiceAccounts/Secrets, unschedulable Pods, PVC failures, and container
  configuration errors.
- **Release provenance**: multi-architecture conformance where supported,
  checksummed tool downloads, SBOMs, signatures, and digest manifests. The
  existing version-gated workflow publishes images and an OCI chart but does
  not yet supply those stronger provenance artifacts.

## Workflow Boundary

`AgentSchedule.runTemplates` rotates independent runs; `subagents` is harness
prompt metadata. **`AgentChain` owns linear completion-driven sequencing**:
GitOps steps create append-only `AgentRun`s when the prior step reaches an
allowed terminal phase, with status-only handoff and no peer credential
sharing. See `docs/agent-chain.md`.

Still out of scope for the first chain cut (and for overloaded AgentRun):
fan-out, join, routing, approval gates, and external event sources. Those
belong on later workflow extensions, not schedule or council.

Horizontal distribution of independent Jobs across worker nodes inside one
Kubernetes cluster is already implemented and is a primary workload model.
What is not implemented is cross-cluster dispatch or workflow-level fan-out
and dependencies. Cross-cluster execution should use an explicit
remote-executor or federation contract when added.
