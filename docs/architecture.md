# Architecture

Anvil Agents separates desired run policy from harness implementation. The
controller resolves a run, materializes immutable payload files in a ConfigMap,
creates one Kubernetes Job, and projects Job, Pod, log, and structured report
state back onto the `AgentRun`.

## Control Loop

1. A user or `AgentSchedule` creates an `AgentRun`.
2. The controller resolves its namespace-local `AgentRunProfile`,
   `AgentHarnessProfile`, ordered `AgentSkillSet` and `AgentToolSet` refs,
   optional `AgentCouncil` association, and local overrides.
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

Each `AgentRun` selects one of `codex`, `openCode`, `hermesAgent`, `openClaw`,
`grokBuild`, `piAgent`, or `custom`. The built-in adapters share the same mounted payload,
tool setup, tool verification, environment, and structured-status contract.
Provider-native model and authentication fields remain adapter-specific.

Kubernetes can schedule runs across nodes. `AgentSchedule.runTemplates` can
rotate independent work through different profiles and harnesses. `Allow` and
`Queue` can make multiple runs active, subject to schedule and application-scope
limits.

This is a horizontal workload model. Large builds, test suites, security
passes, indexing, and other expensive tools can be expressed as independent
runs and placed across the cluster instead of sharing one workstation.
`AgentHarnessProfile` resource requests give the scheduler the information it
needs, while node selectors, affinity, and tolerations route workload classes
to CPU, memory, accelerator, or storage-local node pools. The same role and
skill sets can select a different execution envelope without copying policy.
The normal Kubernetes scheduler can bin-pack or spread those independent Jobs
across eligible workers.

One run still creates one Pod on one node. Scaling across machines means
creating concurrent independent runs; it does not make a single harness
process distributed. Schedule concurrency, application-level
`AgentRunControl`, namespace quota, cluster capacity, and volume access modes
all bound the effective parallelism. Provider quotas, API rate limits, and
external-service capacity can impose additional ceilings even when worker
machines remain available. See
[Distributed Workloads](distributed-workloads.md) for an operational model.

These runs do not share process memory or a live conversation. `subagents`
describes personas or delegated passes to the selected harness; it does not
cause the controller to create child Jobs. Durable coordination requires an
explicit API, Git repository, message bus, database, or `AgentDataVolume`.

## Composition Boundaries

An `AgentRunProfile` owns why and where a role operates. An
`AgentHarnessProfile` owns how one provider runtime executes, including its
image, workload identity, credentials, storage, placement, and limits. An
`AgentSkillSet` owns reusable backend-neutral instruction packs and optional
delegated personas. `AgentToolSet` owns setup and verification contracts for
external tools whose lifecycle is independent from those instructions.
`AgentCouncil` owns a durable workforce inventory of highest-level profile
roles plus optional interaction guidance. Councils never create Jobs and never
grant Secrets, ServiceAccounts, harnesses, tools, or storage; association via
`councilRef` is required before guidance is injected.

The split allows the same knowledge or review policy and external client to run
through Codex, OpenCode, Pi, or a custom harness without copying them. Skill
and tool sets cannot select a Secret, ServiceAccount, image, or volume, so
choosing one never silently grants runtime identity. Tool setup scripts still
execute code, so their authors remain a code-execution authority. See [Agent
Composition](composition.md) for the exact merge and override rules.

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
existing agent image or an internal harness. Add external clients through an
`AgentToolSet`, teach roles when to use them through an `AgentSkillSet`,
provide identities through an `AgentHarnessProfile`, and expose narrowly
authorized service APIs. Keep provider-native semantics in
harness profiles and product policy in run profiles rather than adding either
to the controller core.

Applications connect adverse events through the immutable `AdverseSignal`
contract or an administrator-owned structured GVK watch. Both boundaries use
generic references and unstructured status; the controller never imports the
consumer's API. Reporters provide evidence only. The destination
`AdverseSituation` remains the authority boundary that selects whether and how
an agent responder may run.
