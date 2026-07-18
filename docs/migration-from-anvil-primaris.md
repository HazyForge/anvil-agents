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

## Client and stream migration

The read-only `anvil-agents-api` can be enabled before the reconciler handoff.
It has a separate ServiceAccount and cannot mutate runs, Jobs, or other cluster
objects. This lets browsers, mobile clients, and operator tools move away from
Hub AgentRun projections and direct `kubectl logs` independently of the
controller cutover.

1. Register a distinct `anvil-agents` API audience with the existing OIDC
   issuer. Keep the existing login client if it can request that audience, but
   ensure the audience is not the login client's own client ID.
2. Add explicit authorization bindings for the existing read role. A temporary
   `anvil_primaris_admin` binding can preserve administrative access, but it
   must still list permissions and namespaces explicitly.
3. Enable the API while the standalone controller remains at zero replicas if
   the embedded reconciler is still active.
4. Expose the API on its own HTTPS hostname. The optional HTTPRoute expects TLS
   at its parent Gateway; never send access tokens over plaintext HTTP.
5. Obtain a newly minted signed JWT access token. For ZITADEL, configure Access
   Token Type as JWT first; opaque and JWE tokens are unsupported. Verify list,
   detail, and live SSE access with a read-only user and confirm a user outside
   the configured namespace binding receives a denial.
6. Move `anvilctl`, browser, and mobile consumers to the standalone endpoint.
   Token acquisition remains in the existing OIDC client; the API only
   validates access tokens.
7. Retire the old AgentRun projection after clients are migrated. Leave
   unrelated Hub build, release, and mutation-policy surfaces intact.

The streaming API does not authorize run creation, approval, cancellation,
repository mutation, or delivery. Those operations remain behind the existing
policy broker. See [live-agent-run-stream.md](live-agent-run-stream.md) for the
full provider-neutral configuration and access-token contract.

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
