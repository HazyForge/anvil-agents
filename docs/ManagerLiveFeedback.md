# Anvil manager live feedback and runtime decisions

This is a supporting feature design for the
[Anvil Agents product specification](../spec/README.md). The whole-product
contract covers execution, composition, authority, storage, interfaces, and
coordination; this document develops the manager experience in detail.

Status: first feature draft, 2026-10-26. Subject routing, the local persistent loop,
and their live UI are implemented on `codex/subject-vault-routing`, through
`d10b48e`, in [PR #317](https://github.com/HazyForge/anvil-agents/pull/317).
That is source and local verification status; this work has not been merged,
installed, or deployed. Sections marked **designed** are proposed next slices.

This follows the feature-by-feature format of Hazy Trade's
`docs/HazielLiveFeedback.md` (the local `feat/haziel-live-feed` working tree): the problem,
the data path, what the user sees, and the evidence required for each slice.
The detailed current API and recovery contracts remain in
[local routing](local-manager-routing.md), [the local loop](local-manager-loop.md),
and [remote subject routing](subject-vault-routing.md).

Operator direction:

- One central conversation moves between the projects, questions, preferences,
  and ongoing work the user already has.
- Start with **Anvil conversations first**. These are Anvil-owned sessions layered
  over native harnesses; their identities and history belong to Anvil.
- The manager chooses what a message relates to and how to deliver it. Existing
  runtime adapters carry out the work.
- Show the observed steps, choices, and outcomes while they happen.
- **Ask only when needed or very unsure.** Continue when the intended action and
  its authority are already clear.

## 1. Live activity feed (implemented in source)

Problem: a final reply alone cannot explain which conversation received a message,
whether work was accepted, or why execution is waiting. The user needs to see
those distinctions and answer an unresolved decision in the same interface.

Data path:

```text
central message -> Anvil conversation inventory -> subject + intent judgment
  -> delivery policy -> routing events and receipt
  -> host accepts bound work -> durable task, decision and transition records
  -> browser refreshes host snapshot -> Detect / Ask / Act / Verify feed
```

The routing endpoint streams actual classification stages. The local loop view
refreshes its durable snapshot every 500 ms while open. Each work card shows the
conversation, current state, attempt number, pending question, available choices,
and saved evidence. The conversation rail reflects waiting, running, paused, or
decision-needed state. A completed turn exposes its saved public reply.

Every activity row must come from a routing event or persisted runtime transition.
Confidence is a classifier judgment; acceptance and completion require runtime
evidence. Missing evidence stays missing. Raw tool output and hidden reasoning
are not presented as the manager's decision explanation.

Current limit: the loop exposes lifecycle events, not a unified live tool-call
trace for every native harness. Richer native activity needs an adapter that can
identify public events, redact them, and associate them with the correct attempt.

Verification: the real Desktop-server browser scenario must show a native failure,
one durable question, the chosen answer, a new attempt, and verified completion.
The existing routing browser checks cover early streamed stages and candidate
inspection. Missing providers and manual choices must remain visibly distinct.

## 2. Subject and intent routing (implemented, classification opt-in)

Problem: the user should be able to say "continue the camera work", "how is the
launch going?", or "retry it" without navigating to a harness first.

The manager asks two typed questions before normal dispatch:

| Question | Options |
| --- | --- |
| Which subject? | Shortlisted Anvil conversations, `new_subject`, `none` |
| What next? | `question`, `amend_running`, `new_work`, `status`, `decide`, `unclear` |

Local routing considers up to 200 conversation records and offers up to 15 to the
provider. Recent resolved messages, elapsed time, named older subjects, and pending
questions help resolve references. A subject's relevance and the permitted next
action remain separate decisions.

| Delivery | Required behavior |
| --- | --- |
| Continue | Use the selected conversation's saved history and harness/runtime/folder binding. |
| New work | Create a wrapper conversation only after a new-subject decision or explicit selection. |
| General conversation | Reuse the appropriate general conversation for asides. |
| Status | Read saved state; a status question does not launch a harness. |
| Decide | Address the pending question on the selected conversation; do not append the answer as a new harness prompt. |
| Wait | Retain the unsent message; explicit queue admission saves intent for the host to execute later. |
| Clarify | Ask for the missing target or choice without guessing an execution destination. |

`internal/subjectrouting.Provider` supplies judgments; `internal/localrouting`
owns delivery policy. The current provider adapter is Jev. Local classification
requires `ANVIL_DESKTOP_SUBJECT_ROUTING=1` and server-side provider configuration.
Missing configuration leaves explicit selection available.

Initial routing floors are 0.5, with a 0.8 new-subject bar and 0.15 ambiguity
margin. These are policy settings, not measured accuracy guarantees. Provider
inputs contain bounded, redacted context; working directories and execution
credentials are excluded. Redaction cannot reliably identify arbitrary secrets
written in prose.

The local inventory currently comes from the browser's saved Anvil conversations.
The host owns admitted work and decisions. Native Claude/Codex histories, other
browser profiles, and external sessions are not automatically indexed. A native
session ID is provenance; native session resume is not implemented by this slice.

The remote manager uses owner- and namespace-scoped PostgreSQL subjects and
routing receipts, handing work to the existing append-only AgentRun outbox.
Remote routing and the local loop currently have separate stores and lifecycles.

## 3. Detect, ask, act, verify (implemented in source)

Problem: a failed or disconnected turn must not disappear, be retried twice, or
remain labelled running forever. The manager needs to retain the work, explain
what it knows, and resolve the missing intent.

The runtime owns four parts:

1. **Detect deterministically.** Observe actual process outcomes, missing public
   replies, interrupted execution, unavailable bindings, and persistence failures.
   Import failed or unfinished browser turns when Manager reads that registry.
2. **Keep one pending question.** Attach it to the work record with an ID, version,
   reason, and fixed choices. Re-observation preserves the question's identity.
3. **Act on recorded intent.** An answer selects an available action. The runtime
   saves it before admitting a new attempt through the existing harness adapter.
4. **Verify and persist.** Save observed completion or return to a decision state.
   A browser disconnect does not determine whether native work finished.

The control loop needs no model:

```text
on startup:
  recover saved work and decisions
  mark possibly executed, unverified attempts as completion unknown

on a wake-up or periodic pass:
  if storage cannot be saved: hold execution and probe recovery
  if an ended attempt lacks a durable outcome: keep one uncertainty decision
  for accepted queued work:
    revalidate its saved harness, runtime and canonical folder
    wait if the workspace is occupied or guarded
    persist admission, then run through the native adapter
    inspect exit status and public reply
    persist completion evidence, or keep one decision about the failure
```

This is a behavioral sketch. The current host worker executes serially and waits
for its adapter to return; it is not a separate concurrent supervisor for every
conversation. Desktop must remain running to execute work. Host startup can recover
accepted work without opening a browser page.

Current durable records:

| Record | Relevant information |
| --- | --- |
| Work | Request ID, conversation snapshot, content, origin, binding, state, attempt count, timestamps, reply and verification evidence |
| Decision | ID, work ID, version, reason, question, choices, selected choice and pending/resolved state |
| Transition | Work ID, Detect/Ask/Act/Verify stage, timestamp and observed message |
| Replay identity | Request ID and payload hash; recorded decision identity/choice where applicable |

Known stopped failures offer retry, leave paused, or close without retrying.
Unknown completion first requires the user to confirm they checked that the work
stopped. That acknowledgement does not execute another attempt. Retry may repeat
prior effects; it always creates a new attempt of the saved intent.

The host stores these records privately beside Desktop's config, using atomic
replacement and an OS ownership lock. Shared guards protect direct Chat and
managed work in the same workspace. Canonical paths prevent symlink aliases from
creating two execution slots for one folder.

Current verification means **successful native exit and an identifiable public
assistant reply saved durably**. It does not establish that the requested code,
deployment, or other real-world outcome is correct. Section 7 covers that gap.

## 4. Ask only when needed (product rule; current safeguards retained)

Problem: a central manager loses its usefulness if every classified message turns
into another confirmation. It also loses trust if uncertainty becomes an invented
destination, permission, or success report.

Proceed without another question when the target and action are sufficiently clear
and covered by the user's request. This includes reading status, continuing accepted
work, waiting for its workspace, recovering a recorded answer, and recording or
displaying runtime evidence. Do not ask the user to reconfirm a saved decision.

Ask when a missing answer changes what would execute: two plausible destinations,
an unavailable required binding, an ambiguous answer to a pending question, or a
new attempt whose intent has not been established. Unknown process completion
requires observation or the user's checked-stopped acknowledgement; confidence in
a message cannot establish whether that process still exists.

Questions should identify the affected conversation and the missing choice, offer
the smallest useful set of answers, and remain visible until resolved. One unresolved
subject should not cause repeated questions on every refresh. Clearly unrelated
work may proceed when its own runtime and workspace are available.

Exact answers such as "retry it", "leave it", and "cancel it" work within the
question without a model. The central composer uses `decide` routing to address
the same question. Optional answer classification accepts only the offered actions
plus `unclear`; the initial threshold is 0.85 with a 0.20 margin. A stale answer
cannot overwrite a newer choice. Recovery reuses the original request ID/version.

Designed, not implemented: saved per-conversation defaults for selected recoverable
conditions. Each default must name the condition, allowed action, retry bound and
scope. It cannot authorize replay of an unknown attempt or silently broaden tool
authority. Defaults should eliminate repeated routine questions only after their
behavior has representative scenario coverage.

## 5. Scenarios and route corrections (implemented)

The spec is exercised through sequences of messages and observed state changes.
An expected label is evaluation data; it must not manufacture the provider answer
or the runtime receipt being tested.

| Scenario | Required observable outcome |
| --- | --- |
| Switch projects and harnesses | Each request receives only its selected wrapper's context and folder. |
| Mention older work by name | Recall the named conversation despite a busy recent window. |
| Ask for progress | Read saved status without starting native work. |
| Failed turn, then "retry it" | Keep one question, route the answer to it, start one new attempt, save evidence. |
| Another workspace user is active | Retain accepted work and execute after the workspace is available. |
| Close every browser page | The host continues accepted work and records its result. |
| Abrupt host restart | Guard the uncertain attempt; do not silently replay it. |
| Response lost after answer acceptance | Recover the recorded choice using the same identity; no additional attempt. |
| Storage cannot save completion | Surface uncertainty after recovery; do not manufacture success. |
| Fresh browser after managed/direct/managed turns | Restore the latest host snapshot, including the intervening direct-Chat history. |

The routing bench has nine editable scenarios and 30 labelled turns, fixture and
explicit live-provider modes, individual reruns, candidate probabilities, latency,
and wrong-route correction export. Corrections retain the redacted input, context,
offered candidates and expected labels so a reported miss can become a regression
case. Live evaluation never executes native harness work.

The loop's browser scenario uses the real Go host, store, HTTP routes and subprocess
transport with harmless native fixtures. Backend checks include concurrent replay,
real host termination/restart, origin guards and damaged-store rejection. These
prove orchestration. Live classifier accuracy remains unmeasured for this Anvil
slice; Hazy Trade's benchmark numbers are not Anvil results.

Verification entry points:

- `go run ./cmd/local-routing-bench -mode fixture`
- `go test ./internal/desktop -run '^TestLocalLoop' -count=1 -race`
- After building Desktop: `ANVIL_LOCAL_LOOP_BROWSER=1 go test ./internal/desktop -run '^TestLocalManagerLoopBrowser$' -count=1 -v`
- `make verify` before publishing changes.

## 6. Next slice: thread-to-thread communication (designed)

Problem: related conversations can produce information another thread needs. The
manager should be able to read permitted thread state and deliver a relevant update
across harnesses, preserving who said it and why it was delivered.

Proposed flow:

```text
new source event -> permitted thread context -> relevance judgment
  -> bound target and delivery record -> existing coordination adapter
  -> target admission and response -> manager observes the result
```

A delivered message needs source conversation, source event/work, target,
correlation and delivery identity. The UI renders "from <conversation>" or "from
manager" rather than attributing generated text to the user. Delivery uses the
existing coordination contract and the target's normal admission rules. It cannot
grant peer creation authority or bypass owner/namespace/tool boundaries.

Repeated observation of one source event must not redeliver it. Correlation,
bounded forwarding, and explicit subscriptions must prevent two threads endlessly
reacting to each other. Busy destinations retain a delivery receipt and expose
whether the update is waiting, accepted, or completed.

Acceptance scenarios: one source event delivered once across two different harnesses;
a busy target resumes delivery; a denied target receives nothing; a source event
already caused by a manager delivery cannot create an unbounded cycle. Provider
choice and delivery receipt must both be inspectable.

Not built in the current Local Manager. The existing `origin` string distinguishes
manager work from imported observations; it is not the source-thread provenance
contract described here. No new injection endpoint is specified by this draft.

## 7. Next slice: verify the requested outcome (designed)

Problem: "the harness replied" is weaker than "the work is done". A manager that
keeps watching needs a declared condition whose truth can be checked outside the
assistant's prose.

Proposed outcome contract: work may reference a bounded verification adapter and
expected condition. The adapter records the exact attempt/artifact it checked,
observed result, timestamp, and evidence reference. Examples include a named test
run completing successfully, an expected output artifact with an exact identity,
or a service health result at the intended target.

The runtime may retry observations within a saved observation budget. An unknown
or failing verification must not automatically repeat the original mutation.
Distinguish still waiting, failed, unavailable and verified. If remediation needs
new intent, open one decision tied to that condition. Reuse an applicable recorded
choice when it already covers the condition and action.

Acceptance scenarios: successful native exit with a failing check; delayed evidence
that eventually verifies; inaccessible evidence; evidence from the wrong attempt;
and restart during verification. Completion requires the declared condition, not
a confident assistant statement. This adapter contract is not implemented yet.

## 8. Boundaries and next increments

Anvil conversations remain the local discovery scope. The Desktop loopback host
does not become a Kubernetes policy broker or gain Secret access. Remote AgentRuns
remain append-only. Native providers, classification providers, storage and
verification remain separate adapters.

Signed-in Anvil tool connections currently remain per-turn in direct Chat. Their
tokens are not persisted for background work. The current local store caps work
records at 200 and accepted request identities at 2,000, retaining the latest
2,000 transition events. Retention/archive and full host-owned conversation storage
need a separate migration design before expanding those bounds or discovery scope.

Proposed order after this baseline: collect representative routing corrections;
add one narrow outcome verifier with restart scenarios; then implement bounded
thread-to-thread delivery through coordination. Per-conversation recovery defaults
follow observed repeated cases. These are proposed implementation increments,
not additional changes performed by writing this spec.
