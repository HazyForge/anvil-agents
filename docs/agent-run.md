# Agent Runtime

`anvil-agents` is the sole controller implementation for the agent resources in
`control.anvil.hazyforge.io/v1alpha1`. It can run independently of Anvil
Primaris and does not read Application, Repository, task, build, release, or Hub
resources.

## Ownership

The operator owns these resources:

| Resource | Scope | Purpose |
| --- | --- | --- |
| `AgentRun` | namespaced | Immutable request and controller-owned execution status |
| `AgentRunProfile` | namespaced | Reusable scope and harness defaults |
| `AgentSchedule` | namespaced | Interval and manual child-run creation |
| `AgentRunControl` | cluster | Pause or allow launches for an opaque application key |
| `AgentDataVolume` | namespaced | Durable PVC lifecycle and expansion-only resizing |
| `VolumeProfile` | namespaced | Reusable storage defaults |
| `AdverseSituation` | namespaced | Buffered event stream and optional run responder |

Application and target references are compatibility metadata. The operator
compares their names for scope, concurrency, and launch controls, but never
looks up another API group.

## Run lifecycle

An `AgentRun` resolves its optional `AgentRunProfile`, validates credentials and
durable volume references, writes a payload ConfigMap, and creates one Job. The
Job and ConfigMap are owned by the run. Status is derived from the Job, pod
state, logs, and the structured harness status contract.

Terminal phases are `Succeeded`, `Failed`, and `NeedsHuman`. Pending and
running resources remain resumable. A controller restart observes the existing
children rather than creating a replacement Job.

The backend adapters are `codex`, `hermesAgent`, `openClaw`, `grokBuild`,
`piAgent`, and `custom`. Backend images are selected by each profile or run.
The operator image does not bundle another control-plane CLI.

## Profiles and schedules

Profiles contain durable defaults. Run-local scalar fields override profile
values; selected list fields append run entries after profile entries. Profiles
are namespace-local.

Schedules create child runs from `runTemplate` or rotate through named
`runTemplates`. Their concurrency policies are:

- `Forbid`: do not create a new run while a prior child is active.
- `Allow`: create due runs, optionally capped by `maxConcurrentRuns`.
- `Queue`: create due runs but launch only the oldest eligible pending runs.

Set `control.anvil.hazyforge.io/run-now` to a new token for a replay-safe manual
nudge. When named templates are configured, set
`control.anvil.hazyforge.io/run-template` on the same update to choose one.

Direct runs that share `spec.scope.applicationRef.name` are also subject to the
operator-wide `--application-max-concurrent-runs` limit. The default is one.
This key is opaque and does not require an Application CRD.

## Launch controls

`AgentRunControl` is cluster-scoped. `Paused` blocks new Jobs for runs whose
application key matches; it never terminates an existing Job. An optional
`expiresAt` makes a pause inactive after the deadline. Source fields are audit
metadata and do not establish authorization. Kubernetes API authorization is
the authority boundary.

## Durable storage

`AgentDataVolume` owns or adopts a PVC and exposes its resolved claim, mount,
placement, and environment defaults in status. A `VolumeProfile` can provide
reusable values. Cross-namespace mounts are rejected.

Storage requests may grow but never shrink. Claim names and storage classes are
immutable after creation. When no storage class is selected, new claims use the
cluster default. Existing claims are adopted with their current storage class,
which preserves volumes created by the former embedded controller.

External object-store sync fields are declarative placeholders in v1alpha1;
the operator reports them as stub-only and does not move data.

## Adverse streams

An `AdverseSituation` groups repeated events, deduplicates them, retains a
bounded status buffer, and resolves after its quiet period. Agent responders
are opt-in.

The controller watches no external resource kinds by default. Add repeated or
comma-separated `--adverse-source-gvks=apiVersion/kind` values to opt in. Each
external GVK needs explicit read RBAC supplied through `extraRBACRules`; the
base chart deliberately avoids wildcard access.

## Credentials and identity

Secrets referenced by `envSecretRefs` are projected into the Job with
`envFrom`. They must be in the run namespace. An optional ExternalSecret
preflight can request and verify a fresh target Secret before Job creation.

SPIFFE Workload API mounting is opt-in and requires an exact SPIFFE ID. The
operator only mounts the CSI socket; workload registration and authorization
remain external responsibilities.

## Structured status

Harness images should emit newline-delimited JSON through
`ANVIL_AGENT_RUN_STATUS_TOOL` or the configured status file. A terminal report
should include the action, summary, remaining risk, human requirement, and pull
request URL when applicable. The controller retains recent reports in status.

An optional Postgres archive is enabled with `--archive-database-url` or
`ANVIL_AGENTS_ARCHIVE_DATABASE_URL`. `--terminal-retention` enables pruning only
after terminal status has been archived successfully. The historical table
name is retained so deployments can adopt existing archive records.

The Helm chart reads the database URL from
`archive.databaseURLSecret.name`/`key`; it is never required in chart values as
plain text. Set `archive.terminalRetention` only when archival is enabled.

## Installation

Install the CRDs and controller with Helm:

```bash
helm upgrade --install anvil-agents charts/anvil-agents \
  --namespace anvil-agents-system --create-namespace
```

Configure concrete backend image digests in profiles. The chart defaults do
not create credentials, workload service accounts, external source RBAC, or
agent profiles.

Useful operator flags:

| Flag | Default |
| --- | --- |
| `--watch-namespaces` | all namespaces |
| `--leader-elect` | chart enables it |
| `--application-max-concurrent-runs` | `1` |
| `--default-storage-class` | cluster default |
| `--platform-repository` | `HazyForge/anvil-agents` |
| `--platform-repository-url` | repository clone URL |
| `--platform-docs` | standalone runtime implementation paths |
| `--adverse-source-gvks` | none |
| `--archive-database-url` | disabled |
| `--terminal-retention` | disabled |

## Local verification

```bash
make verify
docker build -t anvil-agents:dev .
```

`make verify` regenerates deep copies and CRDs, copies CRDs into the chart,
runs all Go tests, compiles both binaries, and lints/renders the Helm chart.

## Compatibility contract

The API group, kind names, label keys, owner references, and archive table name
remain stable for takeover. Only one controller deployment may reconcile these
resources at a time. Follow
[migration-from-anvil-primaris.md](migration-from-anvil-primaris.md) for the
handoff sequence.
