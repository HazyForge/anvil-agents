# Conversations and coordination: candidate contract

Status: candidate contract for direct chat, manager routing, and coordination.
Evidence E5, E6, and E8 in the [implementation index](../implementation-notes/reconnaissance-evidence.md)
distinguishes implemented paths from future product direction.

## 1. Anvil owns the wrapper conversation

**Observed:** An Anvil conversation records its own identity, history and execution
binding. A native harness session ID is separate. Local discovery starts with
**Anvil conversations first**; it does not scan arbitrary Claude/Codex histories.

| Surface | Observed state and execution | Visibility / limit |
| --- | --- | --- |
| Local direct Chat | Browser registry, per-conversation draft and binding; wrapper history passed to installed native/WSL harness | Local browser-origin state; active execution remains browser-bound. |
| Local Manager | Same conversation registry plus host-owned accepted tasks, questions, attempts and snapshots | Host execution continues after browser close; it is not a remote synced inbox. |
| Ordinary remote chat | PostgreSQL threads/messages/outbox; accepted turn creates append-only AgentRun; real public reply saved | Existing namespace-shared access semantics apply. The creator's OIDC subject is recorded but does not make every thread private. |
| Remote Manager subjects | Owner/namespace-scoped subjects and routing receipts linked to private chat work | Subject/thread/run/log visibility is owner-scoped; this does not retroactively privatize ordinary shared chat. |

**Observed:** Optional standing process execution can resume native sessions for
selected remote recipes. Local wrapper replay, durable remote message history,
and native resume are different mechanisms. Changing a harness binding does not
automatically migrate its home, native session, or provider authentication.

## 2. Message routing and delivery

**Observed:** Routing considers a bounded inventory of existing Anvil work and
judges subject and intent. Local delivery distinguishes continuing work, creating
work, reusing general conversation, inspecting saved status, waiting, and asking
for a target. Remote subjects have a separate bounded owner-scoped candidate
selection and durable dispatch path. Manual selection remains meaningful when
classification is uncertain or unavailable.

**Candidate invariant — Proposed:** Classification chooses a proposed target and
action; authorization and runtime admission validate execution separately. A
status question should read available evidence without starting unrelated work.
The selected target and the actual dispatch receipt remain inspectable.

**Observed:** Remote manager acceptance freezes target/prompt and queues work
behind active execution locks. Later subject edits/merges do not retarget accepted
work. Local admission binds the conversation, runtime/distro and canonical folder.
Conflicts and unknown completion cannot be erased merely by switching tabs.

**Unknown:** Calibrated live-model routing accuracy across real user workloads.
The existing scenario bench has independent scripted fixtures and manual cases;
passing it demonstrates orchestration, not measured provider accuracy.

## 3. Detect, ask, act, verify

**Observed local loop:** Detect records an execution observation; Ask creates a
durable question when recovery needs a choice; Act admits the chosen attempt;
Verify checks process completion and public reply. These are recorded transitions,
not a requirement to ask on every message. One host worker serializes accepted
tasks; shared workspace guards also cover direct Chat.

**Observed:** Questions have IDs, versions and bounded choices. A central-composer
answer is addressed to the selected conversation's pending question. Exact scoped
phrases can work without a model; optional answer classification can abstain.
It cannot attest that an unknown process has stopped. Replayed answer requests
converge on the recorded result rather than adding another attempt.

**Proposed product behavior:** Continue clear authorized work. Ask when necessary
information, authority, conflicting targets, or material uncertainty prevents the
next action. Preserve an unanswered question with its work while other independent
work proceeds. Domain verification should feed new observations back into this
loop using explicit outcome adapters and bounded recovery policy.

**Observed limit:** The current loop's verified public reply is not a domain
success guarantee. Browser-owned failures become visible when their registry is
inspected; the host does not continuously discover every external native session.
Signed-in direct-Chat tools remain per-turn; the host loop does not persist OIDC
credentials for autonomous retries.

## 4. Peer coordination

**Observed remote chat:** Either Agent or Manager role can opt into coordination.
Explicit allowed profiles share namespace and application scope. The service
validates the entire batch before dispatch, assigns stable delivery identities,
queues busy recipients, and links real independently produced replies. Current
bounds are eight allowed profiles and four distinct recipients per parent turn.
Children do not inherit delegation permission.

**Observed:** Councils are inventory/guidance. Controller requestPeer behavior,
chat delivery, linear chains, and local manager attempts are separate mechanisms.
The controller requestPeer path selects a requested same-namespace profile and
creates a peer run; it does not use chat's explicit recipient allowlist or
four-recipient gate.
A delivery receipt is not evidence of mid-generation steering or interruption.

**Proposed:** Addressed delivery to existing local or remote conversations should
record sender, recipient, request identity, authority, acceptance, execution and
completion. Peer completion could become a manager observation without inventing
another agent's reply. General existing-thread forwarding and automatic return of
all peer outcomes are not established by today's remote delegation path.

## 5. Live interface contract

**Observed:** Current manager surfaces show routing candidates, intent, selected
action, receipt, runtime state, pending questions, attempts and verification
evidence. Local host loop updates are polled; other paths stream public events.
Those are implementation choices for the same need to show actual progress.

**Proposed:** A consistent conversation view should let the person inspect:

- which Anvil work the message addresses and why the available evidence supports
  that choice;
- whether it was only classified, durably accepted, queued, running, waiting on
  a decision, or checked against a stated completion criterion;
- the concrete harness/location and the linked execution or recipient receipt;
- an unresolved choice, its consequences, and the effect of the recorded answer.

This interface presents observable events and concise decision summaries. It
does not require hidden model reasoning, credentials, or raw private tool output.
Reconnect should restore recorded state and expose conflicts; it should not
animate invented progress or silently submit work again.

Detailed current paths and feature proposals remain in
[Manager live feedback](../../docs/ManagerLiveFeedback.md),
[local routing](../../docs/local-manager-routing.md),
[local loop recovery](../../docs/local-manager-loop.md), and
[remote subjects](../../docs/subject-vault-routing.md).
