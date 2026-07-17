# Architecture

Anvil Agents separates desired run policy from harness implementation. The
controller resolves a run, materializes immutable payload files in a ConfigMap,
creates one Kubernetes Job, and projects Job, Pod, log, and structured report
state back onto the `AgentRun`.

## Control Loop

1. A user or `AgentSchedule` creates an `AgentRun`.
2. The controller resolves its namespace-local `AgentRunProfile`,
   `AgentHarnessProfile`, ordered `AgentSkillSet` refs, and local overrides.
3. It validates backend, Secret, storage, and optional ExternalSecret refs.
4. It writes `prompt.md`, `source.json`, skill files, and tool setup scripts,
   then records composition and payload digests.
5. It creates exactly one Job using the selected backend image and runner
   ServiceAccount.
6. The selected built-in adapter prepares configured source and bootstraps
   tools; a custom harness implements the same parts it needs. The harness then
   performs the prompt and emits structured status records.
7. The controller records terminal status and optionally archives it.

A controller restart observes existing children. It does not replace a Job
because a connection was lost. Composition evidence and resolved data volumes
are recoverable from the Job if a crash occurs before the status patch.
Requested Job TTL is enabled only after terminal run status is durable.
Terminal runs are append-only execution records; a new attempt requires a new
`AgentRun`.

## Distributed And Multi-Harness

Each `AgentRun` selects one of `codex`, `hermesAgent`, `openClaw`, `grokBuild`,
`piAgent`, or `custom`. The built-in adapters share the same mounted payload,
tool setup, tool verification, environment, and structured-status contract.
Provider-native model and authentication fields remain adapter-specific.

Kubernetes can schedule runs across nodes. `AgentSchedule.runTemplates` can
rotate independent work through different profiles and harnesses. `Allow` and
`Queue` can make multiple runs active, subject to schedule and application-scope
limits.

These runs do not share process memory or a live conversation. `subagents`
describes personas or delegated passes to the selected harness; it does not
cause the controller to create child Jobs. Durable coordination requires an
explicit API, Git repository, message bus, database, or `AgentDataVolume`.

## Composition Boundaries

An `AgentRunProfile` owns why and where a role operates. An
`AgentHarnessProfile` owns how one provider runtime executes, including its
image, workload identity, credentials, storage, placement, and limits. An
`AgentSkillSet` owns reusable backend-neutral capabilities: named instruction
packs, setup/verification tools, and optional delegated personas.

The split allows the same knowledge or review capability to run through Codex,
Pi, or a custom harness without copying it. A skill set cannot select a Secret,
ServiceAccount, image, or volume, so choosing a capability never silently
grants runtime authority. See [Agent Composition](composition.md) for the exact
merge and override rules.

## Policy Boundaries

- Kubernetes RBAC decides who may create and edit control-plane resources.
- The run's ServiceAccount and credentials decide what its Job may access.
- `AgentRunControl` pauses launches or narrows concurrency; it is not an
  authorization system.
- A harness image turns prompt intent into actions. The controller does not
  reinterpret provider-native tool calls.
- The optional public API is read-only. It never creates or approves runs.

`applicationRef.name` and `applicationTargetRef.name` are opaque strings used
for grouping, controls, and context. They do not cause another Kubernetes API
lookup. Application names used by `AgentRunControl` must be cluster-globally
unique between administrative tenants in v1alpha1.

## Extension Boundary

Use a built-in runner when its provider contract fits. Use `custom` for an
existing agent image or an internal harness. Add external capabilities through
an `AgentSkillSet`, provide their identities through an `AgentHarnessProfile`,
and expose narrowly authorized service APIs. Keep provider-native semantics in
harness profiles and product policy in run profiles rather than adding either
to the controller core.
