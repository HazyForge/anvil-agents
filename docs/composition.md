# Agent Composition

Anvil Agents uses five namespaced resources for reusable configuration:

| Resource | Owns | Must not own |
| --- | --- | --- |
| `AgentRunProfile` | role, scope, policy, standing prompt, intent, notifications, and composition choices | shared capability definitions |
| `AgentHarnessProfile` | one backend adapter, image, Kubernetes execution identity, credentials, storage, placement, and limits | task instructions or skills |
| `AgentSkillSet` | backend-neutral skills and optional delegated personas | images, ServiceAccounts, Secrets, storage, or placement |
| `AgentToolSet` | reusable setup and verification contracts for external tools | the external service, credentials, ServiceAccounts, networking, storage, or placement |
| `AgentCouncil` | durable workforce inventory and optional interaction guidance | Secrets, ServiceAccounts, harnesses, tools, storage, Jobs, or automatic peer injection |

`AgentToolSet` exists because external tools commonly have an independent
owner and lifecycle from the instructions that use them. It never installs the
external service. Prompt sets and personas remain part of role or skill
composition until they demonstrate the same independent lifecycle.

`AgentCouncil` groups highest-level roles without merging their execution
authority. Membership is inventory only. Guidance materialization is opt-in
through `councilRef` on the executing profile or run.

All references must resolve in the `AgentRun` namespace. This keeps Kubernetes
RBAC and Secret ownership understandable. A platform can distribute identical
objects to multiple namespaces with GitOps when sharing is required.

## Basic Composition

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata:
  name: repository-reviewer
  namespace: agents
spec:
  harnessProfileRef:
    name: codex-standard
  skillSets:
    refs:
      - name: repository-review
      - name: organization-knowledge
  toolSets:
    refs:
      - name: organization-knowledge-http
  harness:
    intent: observe
    systemPrompt: Review current evidence and make no mutations.
```

Changing `harnessProfileRef` changes how the work executes without copying or
editing either set. Changing a skill-set ref changes instructions without
changing the provider runtime. Changing a tool-set ref changes the executable
integration without copying either the role or its knowledge-use policy.

## Skill Resolution

The controller resolves skills in this order:

1. Profile skill-set refs, in declaration order.
2. Profile skill overrides.
3. Run skill-set refs, in declaration order, when the run mode is `Append` or
   empty.
4. Run skill overrides.
5. Legacy inline `harness.skillInjections`, `tools`, and `subagents` as a final
   v1alpha1 compatibility overlay.

Tool-set resolution happens after selected skill sets and before that inline
overlay:

1. Profile tool-set refs in declaration order, unless the run mode is
   `Replace`.
2. Run tool-set refs in declaration order.
3. Legacy inline `harness.tools` as the final compatibility overlay.

`toolSets.mode: Replace` replaces inherited tool-set refs only. It does not
discard tools still embedded in a legacy `AgentSkillSet`, and it does not
remove inline profile tools. Move those tools into `AgentToolSet` before
relying on an atomic tool integration swap.

`mode: Replace` on a run discards the profile's skill-set refs and overrides
before resolving the run layer. It does not discard legacy inline profile
capabilities; remove those while migrating if a completely clean swap is
required.

Skills have stable names. A later selected set replaces an earlier skill with
the same name while retaining its position. Identical tools across skill and
tool sets and identical subagent personas are deduplicated by name. Conflicting
definitions of the same tool or persona fail the run instead of choosing one
silently. Duplicate refs and duplicate override names in one layer also fail.

Remote GitHub skill sources require a full 40- or 64-character commit object
ID. Branches, tags, and repository defaults are deliberately rejected so the
resolved payload cannot change without a spec change. An `AgentSkillSet`
cannot select its own token. For private repositories, map the exact GitHub API
host to a same-namespace token Secret in the selected
`AgentHarnessProfile.spec.execution.skillSourceCredentials`. This lets the same
skill set remain portable across public, private, and mirrored deployments
without transferring credential authority to capability-pack authors.

## Skill Overrides

Overrides change one resolved skill without modifying the shared set:

| Operation | Requirement | Result |
| --- | --- | --- |
| `Add` | name does not exist | adds a run- or profile-local skill |
| `Augment` | name exists | replaces a non-empty description, appends content and source refs, and appends unique paths |
| `Replace` | name exists | replaces the entire named skill |
| `Disable` | name exists and no content fields are supplied | removes the named skill |

```yaml
spec:
  profileRef:
    name: repository-reviewer
  skillSets:
    overrides:
      - name: organization-knowledge
        operation: Augment
        content: Limit searches to the payments runbooks for this run.
      - name: issue-triage
        operation: Disable
```

Use `Augment` for a narrow local instruction, `Replace` when the complete skill
contract changes, and `Disable` when the shared capability is inappropriate.
The explicit operation avoids ambiguous null and list-merge behavior.

## Harness Swaps And Legacy Overlays

A run-local `harnessProfileRef` is an atomic runtime swap. The controller uses
the selected harness profile plus run-local inline backend/execution fields; it
does not carry profile-inline images, ServiceAccounts, credentials, storage, or
placement into the replacement. Profile role intent, standing prompt, and
legacy capabilities remain in effect.

When the run does not select a different harness, merge order is:

1. Selected `AgentHarnessProfile`.
2. Profile-inline backend and execution fields.
3. Run-inline backend and execution fields.

That path preserves existing profiles during migration. New profiles should
put all runtime identity and credentials in `AgentHarnessProfile`. This makes a
Codex-to-Pi swap change the complete credential and storage envelope rather
than accidentally mixing provider settings.

Inline compatibility fields use merge semantics: non-empty/non-zero scalars
replace inherited values, while several lists append or deduplicate. A false or
zero inline value does not clear a true/non-zero inherited runtime setting; use
an atomic harness-profile swap for a clean runtime envelope. Restating the same
run-local `harnessProfileRef` still requests an atomic swap and therefore skips
profile-inline backend/execution overlays.

## AgentCouncil

A council names 1-32 existing same-namespace `AgentRunProfile` members:

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentCouncil
metadata:
  name: release-council
  namespace: agents
spec:
  description: Release improvement workforce
  members:
    - role: manager
      profileRef:
        name: release-manager
    - role: auditor
      profileRef:
        name: release-auditor
  councilPrompt: |
    Coordinate through durable issues and exact release evidence.
```

Association is explicit:

```yaml
# On AgentRunProfile or AgentRun
spec:
  councilRef:
    name: release-council
```

Rules:

- An omitted run `councilRef` inherits its profile association. A non-nil run
  ref overrides the profile; v1alpha1 has no run-level clear signal.
- Council and member profile references must stay in the executing namespace.
  Every member profile must exist, roles must be non-empty, and the same
  profile cannot appear twice.
- Membership alone never injects guidance or creates an `AgentRun`. A selected
  council with an empty prompt records association evidence only.
- A non-empty prompt is injected as reserved skill `council-<name>`. Resolution
  fails if another composition layer already uses that name, so recorded
  council provenance cannot silently point at shadowed guidance.
- Resolution records the council name, namespace, UID, generation, resource
  version, and spec digest. It does not copy member profiles or their harness,
  Secret, ServiceAccount, tool, or storage references.
- One `AgentRun` still creates at most one Job with one adapter. Council prose
  is guidance, not release or deployment evidence.

Upgrade order matters: install the CRD/controller/chart RBAC first, allow the
kind in GitOps policy, apply councils after their member profiles, and only then
add `councilRef` to scheduled profiles or runs. A missing council or member
profile produces `NeedsHuman` and requires a new append-only run after repair.

## Resolution Evidence

At Job materialization, `status.resolvedComposition` records each selected
object's name, namespace, UID, generation, resource version, and spec digest.
This includes separate `skillSetRefs`, `toolSetRefs`, and optional `councilRef`
evidence.
`effectiveDigest` covers the complete resolved `AgentRun` spec.
`payloadDigest` covers the exact mounted ConfigMap data, including resolved
remote skill bytes. The read-only OIDC API exposes this sanitized evidence with
run status. Resolved composition also stores only the inherited application and
target names, not the mutable profile object, so profile-owned scope survives
Job-creation crash recovery and remains visible to API clients.

Profiles and sets remain mutable before a Job is created, so queued runs resolve
the latest objects when they become launchable. Once the Job exists, the
controller follows that single execution and recovers the accepted resolution
snapshot from a Job annotation if the status patch was interrupted. Create a
new `AgentRun` to execute changed composition.
