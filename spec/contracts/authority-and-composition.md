# Composition and authority: candidate contract

Status: candidate product contract. Evidence E2 and E4 in the
[implementation index](../implementation-notes/reconnaissance-evidence.md)
supports the observed sections. Existing [repository rules](../../AGENTS.md)
continue to govern implementation.

## 1. Independent configuration lifecycles

**Observed:** A run profile selects role, scope, prompt and composition. A harness
profile selects backend and execution envelope, including credentials, identity,
storage and placement. Skill sets supply instructions/personas; tool sets supply
setup and verification contracts. A council supplies workforce inventory and
optional guidance. Composition references resolve in the run's namespace.

**Observed:** Composition has ordered override rules, stable capability names,
and conflict rejection. Legacy inline fields still affect replacement semantics;
replacing set references does not necessarily remove every inherited inline tool.
Pinned remote skill sources and digest-pinned tool initializers provide specific
reproducibility boundaries, not a universal immutable-source abstraction.

**Candidate invariant — Proposed:** Swapping instructions, tool installation,
model/harness, or execution location should identify exactly which inputs change.
It should not silently migrate credentials, erase history, or expand authority.

**Proposed:** User-defined strength tags and preference notes describe a configured
harness/model/tool setup for manager selection. They have a distinct role from
tool installation, adapter capabilities, and authority. For example, an
`image-generation` tag can express suitability, while an installed image tool and
its selected generation model establish how that setup would perform the task.
See [shared work and harness strengths](shared-work-and-harness-strengths.md).

## 2. Authority boundaries

**Observed:** Resource authors with control over images, commands, identities,
Secret references, and mounts can cause privileged workload execution. The
operator is not itself a hard multi-tenant sandbox. Kubernetes RBAC, admission,
network policy, and narrowly scoped runner identities supply external controls.

**Observed:** Application and target names are opaque scope metadata; the product
does not import consumer API types to resolve them. Manager authorization,
repository mutation policy, and delivery/release policy remain external policy
concerns. Council membership and prompt declarations do not merge credentials.

**Observed:** The controller's resource authority, API authorization, native local
user permissions, and provider credentials are separate. Desktop discovery of an
installed executable does not authenticate its provider or grant it cluster access.

**Proposed:** Stronger namespaced execution admission should constrain allowed
images, classes, ServiceAccounts, Secret/PVC references, commands and placement.
This is a roadmap contract, not a claim that all such constraints are enforced now.

## 3. Optional remote API

**Observed:** The OIDC API is a separate process and ServiceAccount. Authentication
requires exact issuer/audience and explicit claim/namespace authorization; CORS
origins are explicit. Tokens are not accepted in query strings. Authorized log
access verifies run, Job and Pod identity and selects the fixed agent container,
rather than accepting arbitrary client-selected workloads.

| Capability | Observed boundary |
| --- | --- |
| Read runs and execution evidence | Requires scoped permissions; verified child identities protect log selection. |
| Create runs | Explicit run-create gate and authorization; creates append-only intent. |
| Manage composition | Explicit composition-write gate; GitOps-owned resources are protected and writable objects require console ownership. |
| Change launch controls | Separate capability and application authorization; does not imply permission to stop a running generation. |
| Remote chat and coordination | Chat, run-create, and relevant read permissions plus feature/database configuration; coordination further restricts allowed peers. |
| Signed webhook verification | Narrow opt-in same-namespace Secret-get exception for configured HMAC/path-token verification; no secret bytes in responses, status, or logs. |

**Observed:** Chat consumes a mounted database URI and grants no additional Secret
access. The webhook exception does not authorize a general Secret browser or
policy broker. The Console exposes capabilities according to API gates; it is no
longer accurately described as universally read-only.

## 4. Configuration ownership and publication

**Observed:** Git-managed configuration remains protected from Console writes.
The standalone operator retains the existing API identity and does not depend on
Anvil Primaris or Hub installation. Consumer deployment configuration and external
policy remain separate from the runtime repository's responsibility.

**Candidate invariant — Proposed:** A product action should expose its execution
scope and ownership consequences before using authority beyond the established
task. Routine authorized actions need no repeated confirmation. Missing authority
or a materially ambiguous target requires resolution before dispatch.

**Unknown:** Unified owner/principal mapping and policy enforcement across local
operating-system identities, shared remote chat, private manager subjects, and
future cross-location work. Current stores should retain their documented access
semantics until a migration contract is explicit.
