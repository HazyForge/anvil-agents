# Persistent local manager loop

The [manager specification](ManagerLiveFeedback.md) records product behavior,
scenarios and proposed next slices. This page describes the implemented local
loop and its operating contract.

Anvil Agents Desktop owns a persistent loop for local Manager work:
**detect → ask → act → verify**. It observes native execution and saved Anvil
conversation state. The classifier interprets messages and answers; the host
worker owns scheduling, process admission, persistence, and completion evidence.

## What the loop observes

Each accepted Manager message creates a durable work record with intent bound to an Anvil
conversation ID, harness, working folder, native/WSL runtime, and transcript.
The host reserves the actual workspace before execution, using the same exclusion
as direct Chat. Accepted work waits when another turn occupies that workspace.
Another unresolved turn in the same conversation is rejected so it cannot run
with a stale transcript.

The host records transitions as they happen. Native failure, missing public reply,
unconfirmed cleanup, and interrupted execution produce a durable question.
Previously saved browser conversations with unfinished or failed turns can also
be brought into the loop as observations. Observing a problem never starts work.
Questions remain attached to the original work and do not reappear on every poll.
Browser-owned failures are discovered when Manager reads that browser's registry;
already admitted host work continues to be tracked with no page open.

## Decisions and attempts

The Manager shows the observation, current question, available choices, selected
action, and verification evidence. A failed turn offers retry, leave paused, or
close without retrying. Retry starts a distinct attempt of the saved intent;
it can repeat effects of a previous attempt. Closing a record does not undo them.

Uncertain completion first requires **I checked that this work has stopped**.
That acknowledgement releases the uncertainty guard; it does not itself retry.
The same question then offers the ordinary retry/close choices. Leaving it paused
preserves the guard and the decision for later.

Answers are explicitly addressed to one pending question. Whole-message commands
such as "retry it", "leave it", and "cancel it" work without a model. When the
existing classifier is configured, other wording is evaluated against only the
available choices, with an `unclear` option. The policy validates the full closed
set of probabilities, requires confidence and selected probability of at least
0.85, and a 0.20 margin. These are initial policy thresholds, not measured model
accuracy. A classifier cannot attest that an unknown process has stopped.

The central composer can also route a `decide` message to the pending question on
the selected conversation. Pending questions provide bounded router context.
The answer then goes through the same versioned choice endpoint; it is not sent
to the harness as a new user turn. Each question also has its own answer field
and fixed buttons, which remain usable without subject classification.

Each action uses a request ID and decision version. Repeated delivery of an
accepted request returns the recorded outcome. A competing answer or stale tab
cannot overwrite a newer decision.

## Persistence and lifecycle

The host stores work, questions, replay keys, and recent events in
`manager-loop.json` beside Desktop's `config.json`, under its configured config
directory. Writes use a private temporary file and atomic replacement; an OS lock
prevents two hosts sharing one store. Failed writes stop new actions instead of
pretending a decision was saved. Store limits reject new admissions while keeping
existing records.

The current limits are 200 work records and 2,000 accepted request identities;
the latest 2,000 transition events are retained. One host worker executes accepted
work serially.

Closing or reloading a browser page does not cancel admitted Manager work.
Desktop must remain running to execute and reconcile. On host restart, saved
queued intent can resume; a possibly executed attempt becomes unconfirmed and
requires a decision. It is never automatically replayed.

The browser registry still owns conversation names, settings, and drafts. It
projects host work into the conversation transcript once per work ID, checking
the original binding and transcript. Storage failures or conflicting local history
remain visible. The host retains its result independently of browser storage.
This is Anvil conversation continuity, not native harness session resumption.

## What verification means

The loop verifies a completed native process, zero exit status, and an identifiable
public assistant reply, then saves the evidence durably. UI labels describe that
turn-level proof. It does **not** infer that a software change works, a deployment
is healthy, or a broader user objective is achieved from an assistant claim.
Those outcomes require their own domain verification adapters or user review.

The loop uses existing local harness adapters and their existing tool authority.
It does not store OIDC tokens or add cluster/policy-broker authority. Conversations
that require a signed-in Anvil tool bridge continue through direct Chat, where
the existing per-turn connection is prepared. No external Claude/Codex transcript
directories are scanned.

## API and validation

The loopback endpoints are:

| Endpoint | Contract |
| --- | --- |
| `GET /local/v1/manager/loop` | Durable snapshot with revision, tasks, questions and events. |
| `POST /local/v1/manager/loop/work` | Idempotent admission of bound conversation history and content. |
| `POST /local/v1/manager/loop/observe` | Record an unfinished/failed saved conversation without executing it. |
| `POST /local/v1/manager/loop/decisions/{id}` | Apply a fixed choice with request ID and expected version. |
| `POST /local/v1/manager/loop/decisions/{id}/answer` | Resolve text into an available choice or leave the question open. |

The browser refreshes the durable snapshot while open. These events are observed
state transitions, not simulated reasoning or hidden model reasoning.

Backend tests exercise the actual file store, HTTP admission and native subprocess
boundary, including duplicate requests, restart uncertainty and workspace exclusion.
The integrated browser scenario uses harmless executable fixtures and the real Go
host. It covers failure → question → retry → verified reply, question persistence,
queueing behind a running process, and execution while every browser page is closed.
This proves application orchestration; it is not a live-provider accuracy result.

```sh
# Build web/desktop first; use an installed Playwright runtime.
ANVIL_LOCAL_LOOP_BROWSER=1 go test ./internal/desktop -run '^TestLocalManagerLoopBrowser$' -count=1 -v
```

See [local-manager-routing.md](local-manager-routing.md) for subject selection,
the separate routing scenario bench and optional classifier configuration.
