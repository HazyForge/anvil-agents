# Migration from Anvil Primaris

The standalone controller preserves
`control.anvil.hazyforge.io/v1alpha1`. Existing resources, status, Jobs,
ConfigMaps, PVCs, labels, and owner references do not require conversion.

## Safety rule

Never run the embedded and standalone agent reconcilers at the same time. They
use the same watches and child-object names. Leader election does not protect
against two deployments that use different election IDs.

## Handoff

1. Back up the seven agent CRDs and all agent custom resources.
2. Record the embedded controller replica count and confirm there are no
   unexpected terminating runs.
3. Stage the standalone release with `crds.install=false` and zero replicas
   while the old chart still owns the CRDs.
4. In one reviewed cutover, stop the embedded reconcilers, switch CRD ownership
   to the standalone chart, and start the standalone deployment. Do not delete
   the CRDs first.
5. Scale the embedded controller version containing agent reconcilers to zero,
   or deploy the Anvil Primaris version where those registrations are removed.
6. Start `anvil-agents` with one replica and leader election enabled.
7. Verify existing runs, schedules, controls, profiles, adverse streams, and
   data volumes reconcile without child duplication.
8. Remove transitional CRD templates from the old chart only after the new
   controller is healthy so GitOps pruning cannot race the initial install.

The repository-owned Anvil Primaris deployment encodes the initial safe stage:
`replicaCount: 0` and `crds.install: false`. The cutover change must also pin
`image.reference` to a verified digest before scaling up.

## Storage compatibility

Do not delete `AgentDataVolume` resources or their PVCs during migration. The
standalone controller adopts existing claims. When a legacy resource did not
declare `storageClassName`, its existing PVC class remains authoritative.

## Archive compatibility

Point `ANVIL_AGENTS_ARCHIVE_DATABASE_URL` at the existing Postgres database to
continue using historical terminal archives. The table name remains
`anvilhub_agent_run_archives` for data compatibility; no Hub package or service
is required. New archive status also retains the historical
`store: anvilhub-postgres` identifier for client compatibility; the identifier
does not imply a runtime dependency on Anvil Hub.

## Rollback

Stop `anvil-agents` before restoring an embedded controller version. Keep the
CRDs and custom resources installed. A controller rollback must never be
implemented by deleting CRDs, runs, or PVCs.
