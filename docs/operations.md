# Operations

## Versioning

The packaged chart, controller, and six official runner tags form one release
set. The source chart defaults to locally built `:dev` images; packaging
without an image lock rewrites all seven defaults to the selected `vVERSION` and
registry prefix, while release publishing supplies a verified digest lock.
Production values should use immutable digests. Do not mix runner versions
without testing their payload and status contracts.

### Security-Hardening Upgrade Notes

Before upgrading an existing pre-1.0 installation, update every remote skill
source to a full 40- or 64-character commit object ID, move private-source
`tokenSecretRef` entries into the selected harness execution
`skillSourceCredentials`, and rewrite data-volume `extraEnv` entries as
name/value path mappings. General or Secret-backed environment variables
belong in the harness execution envelope instead. Before creating a new Job,
the controller records the immutable payload ConfigMap UID/digest, planned Job
name, and normalized execution digest in status. It records
`jobCreateAttemptedAt` immediately before the create call. Crash-gap recovery
validates those receipts without re-reading mutable profiles. If the planned
Job is missing after that marker, the run fails closed and must not be retried
under the same AgentRun name because execution may already have produced side
effects. The controller safely makes
an exact, current-UID-owned legacy payload ConfigMap immutable before launching
a missing Job; a mismatched payload remains blocked for operator review. A
receipt-less legacy Job that cannot be revalidated remains nonterminal instead
of being reported complete while its execution is still running.
Existing `AgentDataVolume` status must record the live claim UID before a new
run can mount it. A bound PVC found during an old crash gap without that UID is
blocked instead of being adopted by name; inspect and restore the intended
claim relationship before retrying.

To approve a legitimate legacy claim, first compare the `AgentDataVolume`
metadata UID with the PVC's controller owner-reference UID and verify the PVC
labels, storage class, access modes, volume mode, capacity, selector, and data
sources against the intended profile. Recording the PVC UID in status is an
explicit trust decision, not an automatic migration:

```bash
kubectl get agentdatavolume -n <namespace> <volume> -o yaml
kubectl get pvc -n <namespace> <claim> -o yaml
kubectl patch agentdatavolume -n <namespace> <volume> \
  --subresource=status --type=merge \
  --patch '{"status":{"claimUID":"<verified-pvc-uid>"}}'
```

Back up all eleven custom-resource kinds and relevant PVCs before an upgrade.
Run `helm template` and review CRD changes first. Only one controller may
reconcile the branded API group during a migration or rollback.

`make kind-upgrade-e2e` proves a portable seven-CRD, legacy-object baseline can
be upgraded to the current eleven-CRD chart while retaining profiles and runs. It
uses and removes a dedicated disposable Kind cluster. Set
`ANVIL_AGENTS_UPGRADE_FROM_REF` to a reachable historical chart ref when
testing an exact released baseline; the default does not depend on an
intermediate Git object surviving a squash merge.

## Health And Troubleshooting

The controller exposes `/healthz` and `/readyz` on port 8081 and metrics on
8080. Start with:

```bash
kubectl get agentruns,agentschedules,agentdatavolumes -A
kubectl describe agentrun -n <namespace> <name>
kubectl get agentharnessprofiles,agentskillsets,agenttoolsets -n <namespace>
kubectl get jobs,pods -n <namespace> \
  -l control.anvil.hazyforge.io/agent-run=<name>
```

Controller-owned status reasons distinguish invalid composition refs and
overrides, missing images, credential freshness, launch controls, volume
readiness, and Job outcomes. Inspect `.status.resolvedComposition` to identify
the exact reusable inputs, inherited application scope, and payload digest;
`.status.payloadUID` and `.status.jobSpecDigest` anchor restart recovery to the
same immutable payload and execution plan. `.status.plannedJobRef` is the
prepared identity; `.status.jobRef` is populated only after the Job is observed.
Treat a lost client connection as ambiguous and reattach to the same run.

## Capacity And Parallelism

Treat the cluster as a bounded agent worker fleet. Define realistic requests in
each harness profile, separate small and heavy workload classes, and use
placement rules for dedicated node pools. Schedule `maxConcurrentRuns` and
application-level `AgentRunControl` both apply; namespace quota, available
nodes, autoscaler limits, and PVC topology can impose tighter practical limits.

The controller coordinates Jobs; runner Pods on worker nodes consume the heavy
CPU, memory, storage, and accelerator capacity. Scaling controller replicas is
an availability decision, not a way to add run compute. Add eligible workers
or autoscaler capacity to increase compute after concurrency policy permits it.

Watch Pending reasons, queue duration, node utilization, evictions, ephemeral
storage, and volume attachment latency before raising concurrency. For Pending
Pods, inspect scheduler events, allocatable node resources, ResourceQuota,
taints, affinity, PVC topology, image architecture, and external provider/API
limits. Track queued, Pending, and Running runs separately from node saturation.
One AgentRun uses one node, so distribute work as independent runs or shards.
See [Distributed Workloads](distributed-workloads.md) for examples and storage
constraints.

## Archive And Retention

Postgres archive storage is optional. Configure its URL from a Secret and set
terminal retention only after verifying archives. A terminal CR is pruned only
after successful archival. Archive records can contain prompt and output data;
protect and expire them accordingly.

The chart can consume an external Postgres Secret, run a small standalone
StatefulSet, or create a Cluster through an already-installed CloudNativePG
operator. Data-bearing Secrets, PVCs, and chart-created CNPG Clusters are
retained across normal uninstall/prune paths. That protects data; it does not
provide backups, restores, HA, PostgreSQL upgrades, or SQL row expiry. See
[PostgreSQL Archive](archive.md) before selecting a mode or enabling retention.

The controller process writes archive rows; AgentRun worker Pods do not. The
database hostname, TLS policy, firewall or `pg_hba`, and network policy must
therefore allow every node eligible to host a controller replica. If database
access is topology- or source-IP-constrained, place the controller with
`nodeSelector` or required affinity on an approved node pool. Runner placement
remains independent and can still spread heavy Jobs across the rest of the
cluster. Keep terminal retention disabled until an actual archive row is
verified; an archive failure should leave the terminal custom resource present
for retry and diagnosis.

## Uninstall

A normal Helm uninstall removes the controller/API workloads and RBAC but
retains CRDs because each CRD has `helm.sh/resource-policy: keep`. Standalone
archive credentials and PVCs, and chart-created CloudNativePG Clusters, are
also retained. Remove them only after separately backing up and intentionally
decommissioning the archive database.

```bash
helm uninstall anvil-agents -n anvil-agents-system
kubectl get crd | grep control.anvil.hazyforge.io
```

Deleting CRDs is a separate destructive operation. It deletes all matching
custom resources and may garbage-collect PVCs owned by `AgentDataVolume`.
Export resources and back up data before deliberate permanent removal.

## Registry Mirrors

Set `image.reference` for the controller/API and every `runnerImages` value for
a mirror. A harness-profile or run-level backend image overrides the install
default. Use `hack/build-images.sh --prefix ... --tag ... --push` to publish the
same seven components without GitHub Actions; the script refuses dirty pushes by
default.

For a tagged release, `hack/publish-images.sh --prefix REGISTRY/PATH --version
vX.Y.Z` builds and pushes all seven images, verifies that the version and commit
tags resolve to the same registry digest, verifies the OCI source revision, and
atomically writes a deployment-ready digest lock under `dist/`. Recheck an
existing lock with `--verify-lock FILE`; neither path requires GitHub Actions.

`hack/publish-release.sh --prefix REGISTRY/PATH --version vX.Y.Z` is the
complete reusable path. It calls the image publisher, packages the chart,
pushes it to `oci://REGISTRY/PATH/charts` by default, and re-verifies the image
lock after publication. The packaged release chart consumes that lock, so its
controller and six runner defaults are immutable digest references rather
than mutable version tags. By default it first runs `make verify` and
`make kind-e2e`; `--skip-verification` records an explicit decision that those
gates ran elsewhere for the exact tag. Authenticate both Docker and Helm to the
registry first. The tag must point at a clean checkout so OCI source labels and
the lock refer to exactly the reviewed commit.

`hack/package-chart.sh` still supports a convenient version-tagged development
package. Pass `--image-lock dist/images-vX.Y.Z.lock.tsv` when assembling an
offline or manually distributed release artifact that must carry the same
digest-pinned defaults as `publish-release.sh`. When the local version tag is
not available, also pass the independently verified tag commit with
`--source-revision`; lock metadata must match it exactly.

## Optional Release Workflow

`.github/workflows/publish.yaml` is a release convenience built on the same
scripts. It runs only for a `v*` tag push or a manual rerun of an existing tag,
gates publication on `make verify` and `make kind-e2e`, publishes all seven images
with version and commit tags, and pushes the version-coupled chart to the
repository owner's GHCR `charts` namespace. It does not run for `master` and
does not publish `latest`, so an intermediate merge cannot become the default
artifact.
