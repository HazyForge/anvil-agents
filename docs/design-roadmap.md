# Design Roadmap

The extracted v1alpha1 controller is useful today, but an open-source stable
release needs several contracts stronger than the compatibility API it began
with. These are product boundaries, not branding substitutions.

## Before Stable API

- **Execution policy**: a namespaced policy or admission contract for allowed
  harness classes, image digests, ServiceAccounts, Secret/PVC refs, security
  contexts, placement, custom commands, and network classes.
- **Immutable execution envelope**: snapshot the resolved profile UID and
  generation, prompt, tools, source revision, image digest, and credential refs
  when a run is accepted so queued work cannot drift.
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
  checksummed tool downloads, SBOMs, signatures, digest manifests, and OCI
  chart publication.

## Workflow Boundary

`AgentSchedule.runTemplates` rotates independent runs; `subagents` is harness
prompt metadata. A future workflow resource should own fan-out, dependencies,
routing, fallback, approval, and child-run status. That should not be hidden in
an increasingly overloaded AgentRun.

The current system distributes Jobs inside one Kubernetes cluster. Cross-cluster
dispatch is not implemented and should use an explicit remote-executor or
federation contract when added.
