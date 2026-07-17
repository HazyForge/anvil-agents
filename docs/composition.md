# Agent Composition

Anvil Agents uses three namespaced resources for reusable configuration. This
is intentionally the full high-level split for v1alpha1:

| Resource | Owns | Must not own |
| --- | --- | --- |
| `AgentRunProfile` | role, scope, policy, standing prompt, intent, notifications, and composition choices | shared capability definitions |
| `AgentHarnessProfile` | one backend adapter, image, Kubernetes execution identity, credentials, storage, placement, and limits | task instructions or skills |
| `AgentSkillSet` | backend-neutral skills, their setup/verification tools, and optional delegated personas | images, ServiceAccounts, Secrets, storage, or placement |

There are no separate `ToolSet`, `PromptSet`, or persona CRDs. Those concepts
stay with the capability pack that needs them until independent lifecycle or
ownership is proven necessary.

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
  harness:
    intent: observe
    systemPrompt: Review current evidence and make no mutations.
```

Changing `harnessProfileRef` changes how the work executes without copying or
editing either skill set. Changing a skill-set ref changes capabilities without
changing the provider runtime.

## Skill Resolution

The controller resolves skills in this order:

1. Profile skill-set refs, in declaration order.
2. Profile skill overrides.
3. Run skill-set refs, in declaration order, when the run mode is `Append` or
   empty.
4. Run skill overrides.
5. Legacy inline `harness.skillInjections`, `tools`, and `subagents` as a final
   v1alpha1 compatibility overlay.

`mode: Replace` on a run discards the profile's skill-set refs and overrides
before resolving the run layer. It does not discard legacy inline profile
capabilities; remove those while migrating if a completely clean swap is
required.

Skills have stable names. A later selected set replaces an earlier skill with
the same name while retaining its position. Identical tools and subagent
personas are deduplicated by name. Conflicting definitions of the same tool or
persona fail the run instead of choosing one silently. Duplicate refs and
duplicate override names in one layer also fail.

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

## Resolution Evidence

At Job materialization, `status.resolvedComposition` records each selected
object's name, namespace, UID, generation, resource version, and spec digest.
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
