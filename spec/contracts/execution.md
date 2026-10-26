# Execution and durable state: candidate contract

Status: candidate contract; **Observed** sections describe source behavior at the
[baseline](../README.md#scope-and-baseline). See evidence E1, E3, E7, and E8 in the
[implementation index](../implementation-notes/reconnaissance-evidence.md).

## 1. Acceptance and identity

**Observed:** An AgentRun is append-only execution intent with mutable runtime
status. New intent creates a new run. Namespaced composition resolves role,
harness, skills, tools, and optional council association. In the Job path,
materialization freezes payload and execution digests and records child ownership.
It does not yet establish an earlier universal acceptance snapshot for every
queued cluster request.

**Observed:** Remote chat additionally persists its message and outbox together,
freezing dispatch intent before creation. Stable request and run identities allow
reconciliation after uncertain writes. Local persistent Manager instead records
a host task with bound intent and distinct execution attempts. Direct local Chat
does not inherit that host task lifecycle.

**Candidate invariant — Proposed:** Once accepted, work retains enough identity,
context, and receipts to explain what was attempted and avoid implicit replay.
This does not promise exactly-once effects in external systems.

## 2. Runtime choices

| Runtime | Observed behavior | Limit |
| --- | --- | --- |
| Cluster Job | Default run plane; one verified Job, selected adapter, declared identity and resources | One Pod is scheduled to one node; independent runs provide horizontal distribution. |
| Standing InProcess | Opt-in API-owned turns execute through a standing backend; supported recipes launch native subprocesses | Session identity can persist across turns, but the process backend still uses a subprocess per turn. Native resume is recipe-specific. |
| SubstrateActor | Opt-in actor binding/lifecycle; separately gated generation for allowed actor classes | Standing-chat generation is excluded from the actor-generation allowlist in the current path. Missing wiring holds visibly; it is not an implicit Job fallback. |
| Desktop local direct Chat | Installed native or selected WSL harness, wrapper history, public activity stream | Browser-bound turn with cleanup/uncertainty guards; no universal native resume. |
| Desktop local Manager | Host persists tasks and runs accepted work serially through existing local adapters | Continues without browser pages; host restart reconciles uncertainty instead of silently replaying an interrupted attempt. |

**Proposed:** Capability descriptions should make resume, cancellation, streaming,
tool access, isolation, and verification explicit for each adapter and execution
location. The unified harness protocol document currently contains a proposal
and library; it does not prove connected production transport or interruption.

## 3. Lifecycle and recovery

**Observed Job lifecycle:** validate → resolve/materialize → record create attempt
→ observe verified child → persist terminal state → optional archive/retention.
Terminal phases include `Succeeded`, `Failed`, and `NeedsHuman`. Controller restart
observes existing children. Ambiguous missing children after a create attempt do
not authorize replacement execution.

**Observed standing exception:** A raw run `NeedsHuman` phase can represent an
API-owned standing hold. The chat API projects such holds as running and keeps
their streams open. A run phase alone is therefore insufficient to infer that a
conversation is terminal or that the person has a pending question.

**Observed local Manager lifecycle:** accepted work can be queued, running,
verifying, waiting on a decision, paused, completed, or cancelled. Task/decision
records carry identity and replay protection. Retry adds an attempt. Interrupted
or unconfirmed execution first requires checked-stopped resolution, then an
explicit retry or closure choice. Closing work does not undo prior effects.

**Candidate invariant — Proposed:** Recovery separates “known not dispatched,”
“accepted,” “running,” and “outcome unknown.” A timeout, missing reply, or HTTP
error alone is insufficient evidence to repeat external work.

## 4. Producers, concurrency, and control

**Observed:** Schedules use intervals and manual nudges, not cron expressions.
They support configured overlap/queue policy, concurrency and daily limits,
terminal backoff, and ordered template rotation. Rotated templates are independent.

**Observed:** Chains advance linearly from configured terminal outcomes and
optional decision-action gates. NeedsHuman stops advancement. Configuration is
frozen for an active workflow, and handoff is status text rather than shared peer
credentials. Final chain completion requires Succeeded plus any configured
decision-action gate, even when intermediate steps accept another terminal phase.
Cancellation stops advancement while an active child drains.

**Observed:** External triggers authenticate GitHub deliveries and apply bounded
deduplication and limits before creating or annotating work. Adverse signals and
situations provide separate event evidence and responder paths.

**Observed:** Launch pause/concurrency controls govern new work. They are not
generation interruption. A requested peer interruption or mailbox design is not
proof that a native harness can interrupt a running turn. Local admission also
guards conversation and canonical workspace bindings shared with direct Chat.

**Proposed:** Common budgets, fair queues, and explicit cancel/stop guarantees
should be defined independently of the producer or model provider. Arbitrary
DAGs, joins, cross-cluster dispatch, and universal process interruption remain
outside the observed contract.

## 5. Durable state and maintenance

**Observed:** Durable data volumes bind to verified current-owner/PVC identities
in the same namespace. Size grows; claim/class rebinding is not an in-place
migration. Volume copy uses a distinct destination, creating it when needed,
and waits for idle mounts.
Object-store synchronization configuration is stub-only.

**Observed:** Authentication maintenance uses a separate append-only session and
maintenance Job against verified state. Reauthentication can use a short-lived
staging Secret; it does not overwrite an ExternalSecret-managed bootstrap target.
These operator paths do not grant Desktop or ordinary chat Secret access.

**Observed:** Terminal status is durable before Job TTL cleanup begins. When
archive is configured, successful archive precedes retention deletion of the run.
Local host tasks, browser history, chat storage, volumes, and run archives have
different retention and recovery mechanisms.

**Unknown:** Product-wide export, deletion, backup, retention limits, and tested
restore guarantees across all these stores. Local stores are bounded, not an
unlimited permanent record.

## 6. Completion evidence

**Observed:** Harness status and extracted public replies describe execution.
Local Manager verifies successful process exit and an identifiable public reply.
That check does not establish that a repository change is correct, a PR merged,
a service deployed, or a user's broader goal achieved.

**Proposed:** Each work type should name its completion criteria and link relevant
artifacts or verifier results. Verification failure should become an observation
with a bounded next action, not a fabricated success or automatic replay.
