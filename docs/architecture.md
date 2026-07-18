# Architecture

Anvil Agents separates desired run policy from harness implementation. The
controller resolves a run, materializes immutable payload files in a ConfigMap,
creates one Kubernetes Job, and projects Job, Pod, log, and structured report
state back onto the `AgentRun`.

## Control Loop

1. A user or `AgentSchedule` creates an `AgentRun`.
2. The controller merges its namespace-local `AgentRunProfile` defaults.
3. It validates backend, Secret, storage, and optional ExternalSecret refs.
4. It writes `prompt.md`, `source.json`, skill files, and tool setup scripts.
5. It creates exactly one Job using the selected backend image and runner
   ServiceAccount.
6. The harness checks out configured source, bootstraps tools, performs the
   prompt, and emits structured status records.
7. The controller records terminal status and optionally archives it.

A controller restart observes existing children. It does not replace a Job
because a connection was lost. Terminal runs are append-only execution records;
a new attempt requires a new `AgentRun`.

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
`tools`, `envSecretRefs`, and a narrowly authorized service API. Keep provider
or product semantics in profiles and tools rather than adding them to the
controller core.
