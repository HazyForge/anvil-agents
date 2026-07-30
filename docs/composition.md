# Agent Composition

Anvil Agents separates five authorities. Singular skills, tools, and MCP
servers are first-class namespaced resources; ordered sets collect only one
atomic kind; profiles select capabilities; harnesses grant runtime authority.

| Resource | Owns | Must not own |
| --- | --- | --- |
| `AgentSkill` | one Markdown-only `SKILL.md` package plus optional Markdown references | scripts, binaries, credentials, identity, or placement |
| `AgentTool` | one executable acquisition and argv-form verification contract | instructions, Secrets, identity, volumes, or placement |
| `AgentMCPServer` | one secret-free stdio or Streamable HTTP connection contract | tokens, Secret refs, identity, tools, or placement |
| `AgentSkillSet`, `AgentToolSet`, `AgentMCPSet` | ordered refs to one corresponding atomic resource kind | cross-type bundles or runtime grants |
| `AgentRunProfile` | role, scope, policy, standing prompt, intent, notifications, and explicit capability selections | capability definitions or runtime credentials |
| `AgentHarnessProfile` | one backend adapter, image, Kubernetes execution identity, credentials, storage, optional tool cache, placement, and limits | task instructions or capability selection |

Skills contain Markdown only. A skill package script or non-Markdown asset must
be authored separately as an `AgentTool` and selected explicitly. Tool/skill
pairing is not persisted and selecting one never auto-selects the other.
`setupScript` remains an unrestricted migration escape hatch and therefore
composition-write code-execution authority; structured digest-pinned
acquisition is preferred.

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
  capabilities:
    skills:
      selections:
        - skillSetRef: {name: repository-review}
        - skillRef: {name: organization-knowledge}
    tools:
      selections:
        - toolSetRef: {name: organization-knowledge-http}
    mcpServers:
      selections:
        - mcpSetRef: {name: organization-context}
  harness:
    intent: observe
    systemPrompt: Review current evidence and make no mutations.
```

Changing `harnessProfileRef` changes how the work executes without copying or
editing either set. Changing a skill-set ref changes instructions without
changing the provider runtime. Changing a tool-set ref changes the executable
integration without copying either the role or its knowledge-use policy.

## Canonical Resolution And Compatibility

The controller freezes this precedence:

1. Deprecated profile/run `skillSets` and `toolSets` resolve with their current
   v1alpha1 behavior.
2. Canonical profile `capabilities` resolve in declared atomic-or-set order.
3. Canonical run `capabilities` resolve next.
4. Legacy inline `harness.skillInjections`, `tools`, `subagents`, and MCP
   servers remain the final compatibility overlay.

Each canonical kind has independent `Append` and `Replace` semantics. A
canonical run-level `Replace` clears inherited deprecated and canonical
selections for that kind, but never silently removes the final inline overlay.
Sets expand atomics in place, so `[atomic, set, atomic]` retains exact order.
Selecting the same atomic directly and through a set fails as a duplicate.

Set `skillRefs`, `toolRefs`, and `serverRefs` are canonical. Existing embedded
`skills`, `tools`, personas, and inline harness fields remain deprecated inputs
so existing objects execute unchanged during migration.

Legacy `toolSets.mode: Replace` replaces inherited tool-set refs only. It does not
discard tools still embedded in a legacy `AgentSkillSet`, and it does not
remove inline profile tools. Move those tools into `AgentToolSet` before
relying on an atomic tool integration swap.

Legacy `skillSets.mode: Replace` on a run discards the profile's skill-set refs and overrides
before resolving the run layer. It does not discard legacy inline profile
capabilities; remove those while migrating if a completely clean swap is
required.

Skills have stable names. A later selected set replaces an earlier skill with
the same name while retaining its position. Identical tools across skill and
tool sets and identical subagent personas are deduplicated by name. Conflicting
definitions of the same tool or persona fail the run instead of choosing one
silently. Duplicate refs and duplicate override names in one layer also fail.

Canonical GitHub skill packages require a safe relative path ending in
`SKILL.md`, Markdown-only reference paths, and a full 40- or 64-character commit object
ID. Branches, tags, and repository defaults are deliberately rejected so the
resolved payload cannot change without a spec change. An `AgentSkillSet`
cannot select its own token. For private repositories, map the exact GitHub API
host to a same-namespace token Secret in the selected
`AgentHarnessProfile.spec.execution.skillSourceCredentials`. This lets the same
skill set remain portable across public, private, and mirrored deployments
without transferring credential authority to capability-pack authors.

The console can inspect a public github.com tree at the pinned commit, populate
Markdown reference paths, and report ignored scripts/assets. This browser
preview is unauthenticated and restricted by CSP to `https://api.github.com`;
private and GitHub Enterprise packages are authored by reference and fetched
only by the controller through the harness-owned credential mapping.

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
This includes separate `skillRefs`, `skillSetRefs`, `toolRefs`, `toolSetRefs`,
`mcpServerRefs`, and `mcpSetRefs` evidence.
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

## Deferred Catalog Work

This capability model preserves names, immutable source refs, digests, and
provenance so a catalog can be layered on later. Marketplace, popularity
search, cross-type bundles, auto-pairing, external registry APIs, and a
separate validation-receipt CRD are explicitly out of scope. The first real
consuming `AgentRun` is the fail-closed dynamic canary.
