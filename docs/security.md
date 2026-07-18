# Security

Anvil Agents is a privileged workload orchestrator. It is suitable for trusted
platform operators and controlled agent namespaces; v0.1 is not a hard
multi-tenant sandbox.

## Authority Model

An `AgentRun`, `AgentRunProfile`, or `AgentHarnessProfile` author can select an
image, custom command, ServiceAccount, same-namespace Secrets, pull
credentials, security context, node placement, and tolerations. An
`AgentSkillSet` author can supply setup scripts that execute in consuming Jobs.
Granting write access to any of these resources is therefore equivalent to
granting substantial code-execution authority in that namespace. An admission
controller should enforce allowed registries, ServiceAccounts, Secret and PVC
refs, security contexts, resources, and placement rules.

Skill sets cannot directly select images, identities, Secrets, storage, or
placement. Keep that boundary in admission policy too. A run-local harness
swap replaces the profile-inline runtime envelope so provider credentials do
not leak between harnesses; migrate runtime fields into dedicated harness
profiles before relying on this behavior.

Use a dedicated namespace per trust domain. Put only agent-consumable Secrets
there. Disable unnecessary token mounting on runner ServiceAccounts, grant
least-privilege RBAC, apply Pod Security Admission, and restrict egress with
NetworkPolicy. Never place broad production or cluster-admin credentials in an
agent namespace.

Creating an `AdverseSignal` is incident-trigger authority for enabled
`AdverseSituation` responders in that namespace. A reporter role should grant
only `create`, `get`, `list`, and `watch` on signals. Kubernetes RBAC cannot
restrict create permission by destination name, so separate trust domains by
namespace or use admission policy. Signal messages, links, source URLs, and
external status fields are untrusted evidence, never instructions or implicit
fetch requests.

The chart controller ClusterRole is cluster-wide because the operator can
watch multiple namespaces. `watchNamespaces` narrows the controller cache, not
RBAC. For a namespace-limited installation, render and maintain a matching
Role/RoleBinding policy for the exact namespaces rather than assuming the flag
changes authorization.

`externalSecrets.enabled` is false by default. Enable it only when runs use
`externalSecretRefreshRefs`; it grants the controller mutation access to those
ExternalSecret objects. The normal Secret read grant remains necessary for
private skill tokens and run credential preflight.

## Public Read API

The optional API has a separate read-only ServiceAccount. It validates signed
JWT access tokens using exact OIDC issuer and audience values, then applies
explicit permissions and namespace grants. Provider-neutral defaults read
top-level `roles`, `groups`, `scope`/`scp`, and
`anvil_agents_namespaces`. Provider-specific object claims, such as ZITADEL
project-role maps, must be explicitly configured.

The API still has cluster-wide read access to AgentRuns, Jobs, Pods, and logs,
so compromise can expose workload output. Its resolved-composition view
contains object identities, opaque application/target names, and digests, not
Secret values or skill contents.
Terminate TLS at a trusted Gateway, apply rate limits and NetworkPolicy, and
avoid logging secrets in agent output.

## Data Lifecycle

Prompts, source context, tool metadata, logs, status, and archives may contain
sensitive material. Set retention deliberately. `AgentDataVolume` creates or
adopts only its own compatible PVC identity and sets a controller owner
reference; deleting the custom resource can delete the PVC. The chart marks
CRDs with `helm.sh/resource-policy: keep`, so uninstalling the release does not
delete all custom resources or their volumes.

The mounted `source.json` contains the effective run spec and therefore may
include same-namespace Secret reference names, but never Secret values. When a
run originates from a schedule or adverse situation, the controller removes
sibling run templates and responder configuration before mounting that source
object so unrelated runtime and credential references are not disclosed to the
harness. Internal signal delivery receipts are also removed from mounted
adverse context.

Report vulnerabilities privately using [SECURITY.md](../SECURITY.md).
