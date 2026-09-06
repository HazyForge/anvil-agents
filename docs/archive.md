# PostgreSQL Archive

`anvil-agents` uses PostgreSQL as its only durable AgentRun archive. Every
installation mode resolves to one Kubernetes Secret key containing a PostgreSQL
connection URI; the controller contract and archive schema do not change.

The historical table name `anvilhub_agent_run_archives` and archive status
store value `anvilhub-postgres` remain stable for data compatibility. They do
not create a dependency on Anvil Hub or Anvil Primaris.

## Chart modes

Set one explicit `archive.mode`:

| Mode | Database ownership | Controller credential |
| --- | --- | --- |
| `disabled` | none | none |
| `external` | user or platform | referenced Secret key |
| `standalone` | chart renders one PostgreSQL StatefulSet | existing or chart-generated Secret |
| `cloudnativepg` | chart renders one CloudNativePG `Cluster` | CNPG-generated `<cluster>-app` Secret key `uri` |

The chart never installs the CloudNativePG operator or its CRDs. An existing
CNPG cluster that should remain independently managed is an `external`
database; reference its application Secret instead of selecting
`cloudnativepg`.

### External PostgreSQL

Create or synchronize a same-namespace Secret whose selected key contains a
complete PostgreSQL URI, then configure only its identity:

```yaml
archive:
  mode: external
  external:
    databaseURLSecret:
      name: agent-archive-database
      key: url
```

This works with a manually created Secret, External Secrets, a managed database,
or an existing CNPG application Secret. For the latter, set `key: uri`.

`archive.databaseURLSecret.name` and `key` remain as a deprecated compatibility
alias. A non-empty legacy name selects external mode while `archive.mode` is
left at its default. Do not configure the legacy and new external paths
together.

### Standalone PostgreSQL

The standalone mode creates a Service, a single-replica StatefulSet, and a
persistent volume claim:

```yaml
archive:
  mode: standalone
  standalone:
    auth:
      generate: true
    storage:
      size: 10Gi
```

For an ordinary live Helm installation, explicitly setting `auth.generate=true`
generates an alphanumeric password on first install and reuses the retained
Secret through Helm `lookup` on later upgrades. The Secret contains `password`
and the full `url`; the URI is never a plain chart value.

Offline Helm rendering, including Argo CD's normal GitOps rendering, cannot
reliably reuse `lookup` results. GitOps installations must provide
`archive.standalone.auth.existingSecret`. That same Secret must contain the
configured `passwordKey` and `databaseURLKey`, and both PostgreSQL and the
controller consume it:

```yaml
archive:
  mode: standalone
  standalone:
    auth:
      existingSecret: agent-archive-standalone
      passwordKey: password
      databaseURLKey: url
```

The database and username seed PostgreSQL only when an empty data directory is
initialized. Changing those values does not migrate an existing volume. The
official image makes `POSTGRES_USER` the bootstrap PostgreSQL superuser, so the
standalone controller credential has database-superuser authority; this is
another reason to use an externally managed database for production.

The default Alpine image runs as its `postgres` UID/GID 70 under a restricted
container security profile. Adjust both standalone security contexts when
selecting an image with a different UID.

Standalone mode is intended for development and small installations: it has no
HA, TLS, backup, restore, or automated PostgreSQL upgrade workflow. Pin an
appropriate image and own tested backups before relying on it for durable data.

The generated credential Secret and standalone PVC are marked for retention.
A Helm uninstall removes the running StatefulSet and Service but leaves those
data-bearing objects. Reinstall with the same release/name and settings to
adopt them. Delete retained Secrets and PVCs only as a separate, deliberate
data-destruction operation.

The storage class and access modes are immutable after PVC creation. Increasing
the requested size depends on the StorageClass supporting expansion; decreases
are not supported.

### CloudNativePG

Select this mode only after the CloudNativePG operator and
`postgresql.cnpg.io/v1/Cluster` CRD are installed:

```yaml
archive:
  mode: cloudnativepg
  cloudnativePG:
    instances: 1
    storage:
      size: 10Gi
```

The installed operator must generate the application Secret key `uri`, as
documented by CloudNativePG 1.19 and later. CRD discovery alone cannot prove
that the operator is healthy or compatible.

The chart creates a small `Cluster`, bootstraps the `anvil_agents` database and
application owner, disables superuser access, and reads the generated
`<cluster-name>-app` Secret key `uri`. The application Secret is created
asynchronously, so the controller Pod can remain pending briefly while CNPG
initializes the cluster.

The `Cluster` is marked `helm.sh/resource-policy: keep` and Argo
`Prune=false,Delete=false`; deleting it can cascade to CNPG-owned credentials
and storage.

This convenience mode does not configure backups or HA by default. For a
production CNPG design with its own lifecycle, backups, replication, or
advanced PostgreSQL settings, manage the Cluster separately and use external
mode.

## Rotation and connectivity

The connection URI is loaded into the controller process at Pod start and its
PostgreSQL pool is cached. After rotating an external or CNPG-managed Secret,
change `archive.restartToken` to trigger a controller rollout.

For standalone mode, changing `POSTGRES_PASSWORD` in the Secret does not alter
an already initialized database role. Credential rotation is manual database
administration: change the role password in PostgreSQL, update both the Secret
password and URI consistently, and then roll the controller. Plan recovery if
those steps cannot be applied atomically.

External TLS parameters belong in the PostgreSQL URI. Mount required CA or
client files with `extraVolumes` and `extraVolumeMounts`, and reference their
container paths from the URI. The controller namespace and eligible nodes must
be able to reach the database. Runner Pods do not connect to the archive.
Standalone mode uses `sslmode=disable` and does not create a NetworkPolicy;
namespace and network isolation remain the cluster operator's responsibility.

Run the real PostgreSQL migration/upsert integration check with Docker:

```bash
make archive-postgres-integration
```

## Retention and verification

`archive.terminalRetention` controls deletion of terminal Kubernetes AgentRun
objects; it is not a PostgreSQL row TTL. The chart rejects terminal retention
when archive mode is disabled. The controller deletes a terminal AgentRun only
after status records a successful archive result, timestamp, and digest. A
database failure leaves the AgentRun present, records the archive error, and is
retried.

Before enabling terminal retention, complete at least one run and verify its
row directly:

```sql
SELECT namespace, name, phase, archived_at, digest
FROM anvilhub_agent_run_archives
ORDER BY archived_at DESC
LIMIT 10;
```

Archive rows have no automatic expiry in this release. Define database backup,
monitoring, capacity, and row-retention policy outside the controller. The
standalone API and CLI currently read live Kubernetes AgentRuns, not archived
rows.

Standing chat can share this same Secret and URI. Enable
`api.config.chat.enabled` and the API mounts `ANVIL_AGENTS_CHAT_DATABASE_URL`
from the archive Secret by default, writing to schema `anvil_agents_chat`
instead of `anvilhub_agent_run_archives`. See
[Standing Chat Storage](standing-chat.md).

Switching modes does not copy data. Back up, restore or replicate the existing
database, point the new mode at the migrated data, verify a real archive row,
and only then enable terminal retention.
