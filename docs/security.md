# Security

Anvil Agents is a privileged workload orchestrator. It is suitable for trusted
platform operators and controlled agent namespaces; v0.1 is not a hard
multi-tenant sandbox.

## Authority Model

An `AgentRun`, `AgentRunProfile`, or `AgentHarnessProfile` author can select an
image, custom command, ServiceAccount, same-namespace Secrets, pull
credentials, security context, node placement, and tolerations. An
`AgentToolSet` author, or an author using the legacy `AgentSkillSet.spec.tools`
field, can supply setup scripts that execute in consuming Jobs.
Granting write access to any of these resources is therefore equivalent to
granting substantial code-execution authority in that namespace. An admission
controller should enforce allowed registries, ServiceAccounts, Secret and PVC
refs, security contexts, resources, and placement rules.

Skill and tool sets cannot directly select images, identities, Secrets,
storage, or placement. Remote skill sources must identify a full immutable Git
commit, and private-source tokens are selected by exact API host only from the
trusted harness execution envelope. Keep those boundaries in admission policy too. A
run-local harness swap replaces the profile-inline runtime envelope so
provider credentials do not leak between harnesses; migrate runtime fields
into dedicated harness profiles before relying on this behavior.

Use a dedicated namespace per trust domain. Put only agent-consumable Secrets
there. Disable unnecessary token mounting on runner ServiceAccounts, grant
least-privilege RBAC, apply Pod Security Admission, and restrict egress with
NetworkPolicy. Never place broad production or cluster-admin credentials in an
agent namespace.

`anvil-agentctl` uses the caller's kubeconfig and Kubernetes RBAC. It has no
embedded service identity or authorization bypass. `run create` requires the
caller to have `create` access to AgentRuns and never updates an existing run.
The log command reads only the fixed `agent` container after verifying the
controller-owned AgentRun-to-Job-to-Pod chain and the Job and Pod UID receipts
recorded in run status. It confirms the Pod UID again after opening the stream.
The debug command applies the same namespace, owner, and recorded-UID checks
before marking child evidence as verified. Legacy status without UID receipts
is backfilled by the controller; new executions record both immediately.
Structured CLI views escape terminal control characters; the explicit
`run logs` stream remains raw.

Provider auth maintenance uses append-only `AgentAuthSession` objects (Codex,
Grok Build, and OpenClaw). The CLI creates a short-lived staging Secret and
session for `reauth` only; `verify` and `logout` never stage credentials. The
controller creates one fixed maintenance Job with
`automountServiceAccountToken: false`, no caller-selected image/command/mount,
and credential env that is never logged. Reauth projects only the provider's
exact staging key, never the entire Secret. OpenClaw sessions bind the selected
agent, model provider, and auth mode; a strict, symlink-safe parser resolves the
registered agent directory without launching the OpenClaw CLI or volume-owned
plugins while credentials are present. OpenClaw Jobs use the OpenClaw runner
image with UID/GID/fsGroup `1000` and `fsGroupChangePolicy=OnRootMismatch`;
Codex/Grok Jobs retain `10001`. OpenClaw reauth writes only the selected
agent's canonical auth profile store via the OpenClaw SDK and never replaces
SQLite databases or prints secrets. Bootstrap Secret updates refuse
ExternalSecret-managed targets. Untrusted AgentRuns may call `self report` only
and never create auth sessions or mutate Secrets.

Creating an `AdverseSignal` is incident-trigger authority for enabled
`AdverseSituation` responders in that namespace. A write-only reporter role
should grant only `create` on signals. Grant `get`, `list`, and `watch` through
a separate observer role only when that subject may read every signal in the
namespace. Kubernetes RBAC cannot restrict create permission by destination
name, so separate trust domains by namespace or use admission policy. Signal
messages, links, source URLs, and external status fields are untrusted
evidence, never instructions or implicit fetch requests.

An established adverse responder is pinned in `AdverseSituation` status by its
AgentRun UID and immutable spec digest. Later parent status or responder-policy
changes do not rewrite that append-only run. Before a responder is established,
the deterministic child must still match the complete current creation snapshot;
same-name precreation is not accepted on provenance labels alone. A legacy
ref-only responder is upgraded to the UID/digest receipt only when its complete
spec still exactly matches the current creation snapshot; otherwise migration
fails closed for operator review.

The chart controller ClusterRole is cluster-wide because the operator can
watch multiple namespaces. `watchNamespaces` narrows the controller cache, not
RBAC. For a namespace-limited installation, render and maintain a matching
Role/RoleBinding policy for the exact namespaces rather than assuming the flag
changes authorization.

`externalSecrets.enabled` is false by default. Enable it only when runs use
`externalSecretRefreshRefs`; it grants the controller mutation access to those
ExternalSecret objects. The normal Secret read grant remains necessary for
private skill-source tokens selected by harness profiles and run credential
preflight.

`AgentDataVolume` and `VolumeProfile` path environment entries accept only
absolute home, state, cache, config, or data directory variables under the
declared mount. They cannot select Secrets or inject general process
configuration. Supply credentials through the harness execution envelope.

Built-in repository checkout removes HTTP(S) URL userinfo, query strings, and
fragments from the persisted Git remote. Credential-bearing URLs for other URI
schemes are rejected because Git has no provider-neutral mechanism to separate
their authentication material from the persisted origin. Normal SSH usernames
remain supported through `ssh://git@host/path` and SCP-style `git@host:path`
forms. Prefer a credential helper or token Secret over inline URL credentials
anyway, because the transport still has to receive the credential for the
clone. HTTP(S) inline credentials are passed through process-local Git
environment configuration while the clone argument and persisted origin use
only the sanitized URL. The local image builder also sanitizes OCI source
labels and excludes common local credential files from its Docker context.
Review custom Dockerfiles and any explicit cache export before using a shared
or remote builder.

## Public API

The optional API has a separate ServiceAccount from the controller. It validates
signed JWT access tokens using exact OIDC issuer and audience values, then
applies explicit permissions and namespace grants. Provider-neutral defaults read
top-level `roles`, `groups`, `scope`/`scp`, and
`anvil_agents_namespaces`. Provider-specific object claims, such as ZITADEL
project-role maps, must be explicitly configured.

Permissions:

| Permission | Capability |
| --- | --- |
| `anvil-agents:runs:read` | List/get sanitized AgentRun views |
| `anvil-agents:runs:stream` | Live log/status streams (with read) |
| `anvil-agents:runs:purge` | Purge terminal live AgentRun CRs already archived to PostgreSQL |
| `anvil-agents:composition:read` | List/get composition CRs (when `composition.readEnabled`) |
| `anvil-agents:composition:write` | Create/update/delete **console-managed** composition CRs (when `composition.writeEnabled`) |

Composition kinds: `AgentRunProfile`, `AgentHarnessProfile`, `AgentSkillSet`,
`AgentToolSet`, `VolumeProfile`, `AgentDataVolume`.

**GitOps is source of truth.** Even with composition write enabled, the API
refuses to update or delete objects that:

1. carry well-known GitOps ownership markers (Argo CD, Flux, Helm), or
2. lack the label `control.anvil.hazyforge.io/managed-by=anvil-agents-console`.

Console creates always stamp that label and strip client-supplied GitOps
markers. Granting `composition:write` is still code-execution authority for
console-managed objects (tool setup scripts, harness image/SA/Secret *refs*).
The API never grants Secret data access and never mutates AgentRuns.

The API still has cluster-wide read access to AgentRuns, Jobs, Pods, and logs,
so compromise can expose workload output. Its resolved-composition view
contains object identities, opaque application/target names, and digests, not
Secret values. Composition list/get responses include full specs for authorized
namespaces so operators can edit console-managed objects.
Terminate TLS at a trusted Gateway, apply rate limits and NetworkPolicy, and
avoid logging secrets in agent output.

List responses are paginated internally and fail closed when a namespace
exceeds the configured object cap. Streaming limits are separate from that cap.

## Data Lifecycle

Prompts, source context, tool metadata, logs, status, and archives may contain
sensitive material. Set retention deliberately. `AgentDataVolume` accepts only
a current-UID-owned compatible PVC, records the claim UID, and every new run
rechecks both owner and UID before mounting it. A bound claim cannot be adopted
through a status-persistence gap. Deleting the custom resource can delete its
PVC. The chart marks CRDs with `helm.sh/resource-policy: keep`, so uninstalling
the release does not delete all custom resources or their volumes.

The mounted `source.json` contains the effective run spec and therefore may
include same-namespace Secret reference names, but never Secret values. When a
run originates from a schedule or adverse situation, the controller removes
sibling run templates and responder configuration before mounting that source
object so unrelated runtime and credential references are not disclosed to the
harness. Internal signal delivery receipts are also removed from mounted
adverse context.

PostgreSQL archive credentials stay in Secret references, but archived rows
can contain prompts, source context, decisions, reports, and bounded output.
Restrict database and Secret access, use encrypted transport and storage where
appropriate, and define backup and row-retention policy. The standalone chart
mode is a single-instance convenience database without TLS or backups; use an
externally managed PostgreSQL design for stronger production controls. Retained
standalone credentials/PVCs and CloudNativePG Clusters must be decommissioned
as explicit data-destruction operations after backup verification.

Report vulnerabilities privately using [SECURITY.md](../SECURITY.md).
