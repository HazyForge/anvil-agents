# Phase 1: product reconnaissance

Status: candidate whole-product model. Labels and baseline are defined in the
[specification entry point](../README.md). Source pointers are collected in the
[evidence index](../implementation-notes/reconnaissance-evidence.md).

## 1. Product purpose

**Inferred:** The product makes agent work addressable, composable, observable,
and recoverable across workstation and cluster execution. A person should be
able to identify the work, the executing role and harness, the authority used,
the current state, and the evidence behind any completion claim.

**Observed:** Independent heavyweight jobs can be distributed across nodes in
one Kubernetes cluster. Desktop can execute installed local harnesses and access
the optional remote API. These capabilities have different stores and failure
domains today; a seamless shared workspace is not established.

**Proposed:** Direct agent interaction and central management should share a
coherent product vocabulary. The user can inspect or correct a manager decision,
continue a specific conversation, and understand which runtime is doing the work.

**Operator direction, 2026-10-26:** The shared Anvil work layer maintains a
superset of participating chats across harnesses such as Codex and Claude Code,
reducing context switching. User-defined strengths, tags, and model/tool
specializations guide which setup receives coding, review, image, video, or other
work. The [candidate contract](../contracts/shared-work-and-harness-strengths.md)
defines this selection and handoff behavior; it remains proposed implementation.

## 2. Candidate component boundaries

| Boundary | Owns | Inputs and outputs | Exclusions / status |
| --- | --- | --- | --- |
| Execution operator | Composition resolution, cluster execution lifecycle, child ownership, terminal evidence | Resource intent → resolved payload → execution status | **Observed.** Does not own consumer delivery policy or model-provider internals. |
| Composition library | Reusable roles, harness envelopes, skills, tools, workforce inventory | Namespaced definitions → selected effective composition | **Observed.** Council membership does not grant authority. |
| Work producers | Manual creation, schedules, linear chains, signed webhooks, adverse responders | Trigger + configured intent → append-only runs | **Observed.** Cadence, workflow transitions, and external event authentication are distinct. |
| OIDC API and Console | Authorized summaries, logs, gated mutations, remote chat and live events | Principal + scoped request → durable record / response / event | **Observed.** Separate process and identity from controller; not an unrestricted Kubernetes proxy. |
| Runtime CLI | Operator run, launch-control, schedule, auth and volume operations | Kubernetes-authorized command → runtime resource or evidence | **Observed.** Does not replace external manager policy APIs. |
| Desktop | Local harness discovery/delegation and client access to optional remote API | Anvil conversations and user actions → local execution or remote requests | **Observed.** No kubeconfig or cluster-policy authority. |
| Conversation and manager services | History, routing receipts, pending decisions, bounded peer deliveries | Message + saved context → target/action + linked execution | **Observed in distinct local/remote forms.** No global imported native-session index. |
| State and evidence | Chat/outbox, host tasks, volume state, archive, public execution events | Durable writes and observations → recovery and inspection | **Observed.** No single universal transcript, archive, or retention policy. |
| External policy and integrations | Consumer authorization, repository/release policy, credentials, external services | Explicit scopes and tool results across interfaces | **Observed boundary.** Anvil does not acquire policy authority through a role name. |

## 3. Principal user journeys

### 3.1 Compose and run an agent

**Observed:** An operator selects role, harness, instruction and tool sets, and
explicit durable state. A run resolves these in its namespace, validates them,
and records execution evidence. The Job runtime schedules one workload; optional
standing/actor runtimes use their own gated paths. The person follows status and
authorized logs, and can distinguish launch failure from a harness result.

### 3.2 Continue a conversation

**Observed:** The person selects an Anvil conversation and execution location.
Local direct Chat invokes an installed harness with wrapper history. Remote chat
accepts a durable turn, freezes dispatch intent, and records the real assistant
reply. A selected remote standing backend can separately resume supported native
sessions; this is not a property of every local or remote harness.

### 3.3 Work through a manager

**Observed:** Manager routing considers saved Anvil work, classifies subject and
intent, and chooses a bounded delivery action. The local persistent loop records
Detect / Ask / Act / Verify transitions and questions. The remote manager retains
subject and handoff receipts in the chat database. Existing remote coordination
can send allowed peers real work with independent execution receipts.

**Proposed:** Extend this into an explicit loop that can verify domain outcomes
and incorporate addressed completion messages across existing local and remote
conversations. The current product does not provide that universal loop.

### 3.4 Automate recurring or dependent work

**Observed:** Schedules create independent interval-driven work; chains advance
through configured linear steps after accepted outcomes. Signed GitHub deliveries
and adverse evidence can activate configured responders. Launch gates and limits
remain applicable. A chain waiting for human input has not completed merely
because its last child stopped running.

### 3.5 Recover and maintain state

**Observed:** Reconnect and restart inspect persisted identities rather than
blindly replaying work. Unknown local execution becomes a pending recovery
decision. Operators can maintain provider authentication and copy durable volumes
through dedicated resources. Terminal evidence precedes configured cleanup.

## 4. Candidate product invariants

The following are **Proposed** product-wide invariants, supported to differing
degrees by current implementations. The component contracts describe the limits.

1. Each accepted execution has an addressable identity, bound context, and
   recoverable state; transport loss does not itself authorize another attempt.
2. Configuration, prompt content, classification, and council membership cannot
   independently enlarge the executing principal's authority.
3. A user can distinguish proposed routing, accepted dispatch, active execution,
   pending decisions, and the evidence for completion.
4. Location or harness changes expose effects on credentials, workspace, and
   session continuity rather than implying memory migration.
5. Questions resolve material ambiguity or missing authority. Known authorized
   steps proceed without recurring confirmation.
6. Verification names the actual criterion checked. Process exit, reply text,
   domain success, merge, install, and deployment remain distinct.
7. Local and remote execution respect their own owners and visibility rules;
   indexing work does not make its private contents generally accessible.

## 5. Product scenarios and acceptance evidence

These are **Proposed** cross-product acceptance scenarios, not a claim that a new
suite has been implemented or run. Existing evidence is linked by ID below.

| Scenario | Expected observable outcome | Existing evidence / remaining gap |
| --- | --- | --- |
| Compose a role, swap its harness for future work | Selected role/skills persist; new runtime and credential binding are visible; prior run remains inspectable | E2, E5. No automatic home migration. |
| Launch heavyweight work and restart controller | Same verified execution child is observed; no replacement after ambiguous creation | E1. Cluster acceptance is separate from source checks. |
| Lose the response after accepting a chat message | Replay of the same request returns the original accepted turn; conflicting content is rejected | E5. Runtime effects still need their own evidence. |
| Switch between two local projects and ask for status | Correct wrapper context and folder are retained; saved-status inspection launches no harness | E6. Fixture classifier results do not measure provider accuracy. |
| Fail local work, close browser, then answer its pending question | Host retains state; answer addresses that decision; explicit retry adds one attempt | E6. Unknown execution first requires checked-stopped resolution. |
| Message a busy allowed peer | Durable delivery waits and links the recipient's own execution and reply | E5. Arbitrary existing local-thread forwarding remains proposed. |
| Run a scheduled task and a dependent two-step chain | Schedule cadence and chain dependency are distinguishable; NeedsHuman stops chain advancement | E3. No arbitrary DAG support implied. |
| Receive a duplicate signed webhook | Authentication and deduplication precede bounded configured action | E3, E4. Non-GitHub intake remains outside the current slice. |
| Inspect another owner's private manager work | Subject/thread/run/log access remains scoped; ordinary shared chat retains its documented semantics | E5. Shared-chat privacy changes require explicit migration design. |
| Preserve a durable home and archive terminal work | Correct PVC identity is used; copy verifies distinct source/destination; configured archive precedes retention deletion | E7. Object-store synchronization is not implemented. |
| Observe an unsupported or disabled runtime | Visible hold/failure explains missing capability; no implicit switch to a different execution plane | E1, E8. Production enablement requires separate proof. |
| Coordinate code, review, image and video work from one conversation | Linked harness conversations preserve shared context; editable strengths guide eligible setup/model selection; real artifacts and receipts return to the work | E10 establishes the current gap. Dedicated acceptance scenarios are in the shared-work contract; they are not implemented tests. |

## 6. Investigation priorities

| Priority | State | Next contract question |
| --- | --- | --- |
| 1 | **Unknown** | How should a common work identity link local tasks, remote turns, runs, and delivered artifacts without conflating their lifecycles? |
| 1 | **Proposed** | Implement user-defined setup strengths and capability-aware harness/model selection within the shared work layer, including a delegatable Claude Code adapter. |
| 1 | **Proposed** | Domain verifiers and addressed completion delivery should close the manager loop beyond successful process execution. |
| 1 | **Unknown** | Which conversation migration/export and owner model should bridge local browser history, host snapshots, and remote storage? |
| 2 | **Proposed** | Strengthen admission policy, namespace/provider budgets, and fairness before broader multi-tenant operation. |
| 2 | **Proposed** | Freeze authority-bearing inputs earlier for queued cluster work; formalize portable source and harness capability contracts. |
| 2 | **Unknown** | Define retention, deletion, backup, and restoration guarantees across every durable store. |
| 3 | **Proposed** | Consider richer workflows and cross-cluster dispatch only with explicit dependency and authority contracts. |

The [design roadmap](../../docs/design-roadmap.md) supplies implementation-era
context. Priorities here are draft sequencing, not promises of shipped behavior.
