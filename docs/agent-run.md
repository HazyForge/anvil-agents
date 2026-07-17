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
| `AgentRunProfile` | namespaced | Reusable role, scope, policy, and composition defaults |
| `AgentHarnessProfile` | namespaced | Reusable backend and Kubernetes execution envelope |
| `AgentSkillSet` | namespaced | Reusable backend-neutral capability pack |
| `AgentSchedule` | namespaced | Interval and manual child-run creation |
| `AgentRunControl` | cluster | Pause or allow launches for an opaque application key |
| `AgentDataVolume` | namespaced | Durable PVC lifecycle and expansion-only resizing |
| `VolumeProfile` | namespaced | Reusable storage defaults |
| `AdverseSituation` | namespaced | Buffered event stream and optional run responder |

Application and target references are compatibility metadata. The operator
compares their names for scope, concurrency, and launch controls, but never
looks up another API group.

## Run lifecycle

An `AgentRun` resolves its optional `AgentRunProfile`, selected
`AgentHarnessProfile`, and ordered `AgentSkillSet` references, validates
credentials and durable volume references, writes a payload ConfigMap, and
creates one Job. The Job and ConfigMap are owned by the run. Status is derived
from the Job, pod state, logs, and the structured harness status contract.

Terminal phases are `Succeeded`, `Failed`, and `NeedsHuman`. Pending and
running resources remain resumable. A controller restart observes the existing
children rather than creating a replacement Job.

The backend adapters are `codex`, `hermesAgent`, `openClaw`, `grokBuild`,
`piAgent`, and `custom`. Backend images are selected by each harness profile or
run.
The five built-in adapters share the repository checkout, injected tool setup,
tool verification, and prompt-context contract. The operator image does not
bundle another control-plane CLI.

One run selects exactly one adapter. Named schedule templates can rotate or
queue independent runs across adapters, but the controller does not create a
shared multi-agent conversation. `subagents` are instructions to the selected
harness, not controller-created child Jobs.

## Profiles and schedules

Profiles contain durable role, scope, and policy defaults plus references to a
runtime and capability packs. Run-local non-empty and non-zero compatibility
fields override profile values; lists use field-specific append/deduplication
rules. Use a harness-profile swap when inherited false/zero runtime values must
be cleared. All profile, harness, and skill-set references are namespace-local.
See
[Agent Composition](composition.md) for precedence, atomic harness swaps, skill
collision rules, and the four explicit override operations.

At Job materialization, `status.resolvedComposition` records the exact object
versions and digests used. `effectiveDigest` covers the complete resolved
`AgentRun` spec and `payloadDigest` covers the mounted ConfigMap data, including
fetched remote skill content. This evidence is also available through the
sanitized read API; it does not copy Secret values or skill contents into
status.
The sanitized snapshot also records inherited opaque application and target
names, allowing the read API to return profile-owned scope without returning
the profile itself.

Schedules create child runs from `runTemplate` or rotate through named
`runTemplates`. Their concurrency policies are:

- `Forbid`: do not create a new run while a prior child is active.
- `Allow`: create due runs, optionally capped by `maxConcurrentRuns`.
- `Queue`: create due runs but launch only the oldest eligible pending runs.

Set `control.anvil.hazyforge.io/run-now` to a new token for a replay-safe manual
nudge. When named templates are configured, set
`control.anvil.hazyforge.io/run-template` on the same update to choose one.

Runs that share `spec.scope.applicationRef.name` are subject to the matching
active `AgentRunControl.spec.maxConcurrentRuns` values. The strictest matching
positive value wins; the operator-wide
`--application-max-concurrent-runs` value is the fallback when no control sets
a limit. The default fallback is one. This key is opaque and does not require
an Application CRD.

## Launch controls

`AgentRunControl` is cluster-scoped. `Paused` blocks new Jobs for runs whose
application key matches; it never terminates an existing Job. An optional
`expiresAt` makes a pause inactive after the deadline. Source fields are audit
metadata and do not establish authorization. Kubernetes API authorization is
the authority boundary.

## Durable storage

`AgentDataVolume` creates a PVC or accepts a compatible existing claim already
controller-owned by the same `AgentDataVolume` identity. It exposes the
resolved claim, mount, placement, and environment defaults in status. A
`VolumeProfile` can provide reusable values. Cross-namespace mounts are
rejected.

Storage requests may grow but never shrink. Claim names and storage classes are
immutable after creation. When no storage class is selected, new claims use the
cluster default. Compatible claims from the former embedded controller retain
their current storage class when the same resource identity takes over.
For `WaitForFirstConsumer` storage classes, a current Pending claim with the
controller-owned `ClaimPending` status is allowed into the Job so that Job can
be the binding consumer. Other Pending claims continue to block launch.

External object-store sync fields are declarative placeholders in v1alpha1;
the operator reports them as stub-only and does not move data.

When a harness requests `ttlSecondsAfterFinished`, the controller records the
request on the Job but leaves Kubernetes TTL cleanup disabled until terminal
`AgentRun` status is durable. A crash can therefore delay Job cleanup, but it
cannot delete the only execution evidence before the controller records the
result and then launch a replacement.

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
The chart grants ExternalSecret mutation only when
`externalSecrets.enabled=true`.

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

The CRDs carry `helm.sh/resource-policy: keep`; uninstalling the Helm release
retains them. Deliberately deleting CRDs deletes their custom resources and may
garbage-collect PVCs owned by `AgentDataVolume`.

The optional `anvil-agents-api` serves sanitized run summaries and bounded live
SSE logs to OIDC-authorized clients without Kubernetes credentials. It is
disabled by default and runs with a separate read-only ServiceAccount. See
[live-agent-run-stream.md](live-agent-run-stream.md) for its security boundary,
provider-neutral OIDC configuration, and client workflow.

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
| `--runner-image-{codex,hermes-agent,openclaw,grok-build,pi-agent}` | local `:dev` image; packaged charts use matching `vVERSION` |

## Local verification

```bash
make verify
make images
```

`make verify` regenerates deep copies and CRDs, copies CRDs into the chart,
runs all Go tests, compiles both binaries, and lints/renders the Helm chart.
`make images` calls `hack/build-images.sh`, which builds the controller and all
five built-in runner images into local Docker by default. It can select
individual components or push the same image set to any authenticated registry,
so GitHub Actions is optional.

## Compatibility contract

The API group, kind names, label keys, owner references, and archive table name
remain stable for takeover. Only one controller deployment may reconcile these
resources at a time. Follow
[migration-from-anvil-primaris.md](migration-from-anvil-primaris.md) for the
handoff sequence.
