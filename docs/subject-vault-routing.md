# Central manager and subject vault

Anvil Agents Desktop's **Manager** accepts a conversation that moves between
projects, questions, preferences, and ongoing work. Before dispatch, it selects
an owned subject and an intent. Its live activity panel shows the actual API
steps: message received, candidate subjects loaded, classification in progress,
the selected subject/intent with confidence and alternative probabilities, and
the durable handoff. These are routing receipts, not generated reasoning.

## Enable

This feature is opt-in. Set `api.config.chat.subjectRoutingEnabled: true` in the
existing Helm API configuration, alongside `chat.enabled: true`, the existing
chat PostgreSQL configuration, and `runs.createEnabled: true`. The caller needs
chat read/write and run-create permission in the selected namespace. Desktop's
agent selector also uses `composition.readEnabled` and composition-read
permission; a missing roster must not hide existing subject history. The API
advertises the gate in `/ui-config.json`; Desktop uses Manager as its default
remote landing page while the gate is enabled. Direct agent chat remains
available.

The existing server-side `TYPESAFE_API_KEY` environment injection supplies Jev.
`chat.jevModel` selects its serving model, as on the intent hook. The API process
reads no additional Kubernetes Secrets and gains no additional RBAC. The old
`chat.jevIntentEnabled` gate controls the direct-chat intent hook separately;
manager routing uses one batched subject/intent request and does not run that
second classifier. Missing credentials or provider failures produce a visible
choice, without silently selecting a subject.

This document describes source behavior. Enabling a local configuration does
not enable the deployed API or install a new Desktop binary.

## Decisions and execution

`internal/subjectrouting.Provider` supplies typed judgments behind an adapter.
The included Jev adapter uses the existing HTTP client and asks two independent
Choice questions in one request. The manager chooses among 15 recent subjects,
`new_subject`, and `none`; a named label or alias can recall an older subject
from the latest 200 owned records. Intents are `question`, `amend_running`,
`new_work`, `status`, `decide`, and `unclear`.

Code validates the closed sets and probability distributions before applying
confidence and ambiguity gates. Opening a new subject needs the higher 0.8
bar; uncertain routing waits for a user choice. These initial thresholds are
policy defaults, not measured accuracy guarantees. The provider sees bounded,
redacted labels, summaries, aliases, and the message. Common credential forms
are redacted; arbitrary secrets in prose cannot be identified reliably.

An explicit user choice can resolve an uncertain match or choose a subject
when classification is unavailable. It is marked as a human selection without
invented model confidence. General conversation reuses an owned conversation
subject for the selected profile, so asides do not each open a project.

A subject points to a real persisted chat thread and its configured agent
profile. The target thread's history plus structured subject context enter the
existing chat outbox. The resulting AgentRun is append-only. The route itself
grants no tools or manager authority. `amend_running` queues a continuation via
the existing execution locks; it does not interrupt or rewrite an active
AgentRun. Multiple manager follow-ups are retained in FIFO order. Their prompts
are the context snapshots accepted at send time; later assistant output is not
retroactively added to a frozen Job prompt. A persistent standing harness also
retains its session context. Cross-agent requests still use the existing
coordination contract.

## Persistence and privacy

The existing chat PostgreSQL migration adds `subjects` and `routing_requests`.
Subjects carry kind, label, aliases, summary, target thread/profile, lifecycle,
and timestamps. Summaries retain the original topic and the latest request;
the owner can edit them. Parked subjects can be reopened by a match. Merging
preserves source transcripts and routing receipts while redirecting future
messages to the target. Historical execution is never moved or replayed.

Every manager request requires a UUID. A database advisory lock serializes
that request across replicas; content/profile hashes reject conflicting reuse.
Persisted routing events and the frozen target survive retries. Once a target
is frozen, a later merge cannot move a retry to another thread. The existing
chat outbox then deduplicates the actual execution. An accepted receipt is
immutable. Browser disconnects do not cancel accepted processing; the routing
operation has a bounded deadline and can be recovered with the same request ID.

Subjects and routing receipts are scoped to the authenticated subject and
namespace. Manager-created threads also carry an owner marker, enforced by
thread list/detail/message/stream and run list/detail/log endpoints. Private
run prompts are excluded from other conversations' coordination inventories.
Kubernetes administrators and the configured harness retain their existing
infrastructure access; this does not introduce a new policy broker.

## API and live delivery

All paths below are under `/api/v1/namespaces/{namespace}/chat` and require
normal bearer authentication; tokens are never accepted in query strings.

| Endpoint | Behavior |
| --- | --- |
| `GET /subjects` | Current owner's subjects |
| `POST /subjects/{id}` | Update label, summary, aliases, kind, or lifecycle |
| `POST /subjects/{id}/merge` | Merge into owned `targetId` |
| `GET /routing` | Recent durable manager-message receipts |
| `GET /routing/{requestId}` | Recover one receipt |
| `POST /manager/messages` | Route `{requestId, content, profileName, subjectId?}` |

The manager POST accepts `text/event-stream` and flushes `routing` events as
the stages occur. The terminal `result` is `accepted`, `clarify`, or `failed`.
An accepted result contains the actual thread, user message, and turn. Desktop
then follows the existing authenticated thread stream and durable snapshots
for the assistant response. A routing handoff is not an execution completion.
With ordinary JSON Accept, the same POST returns the final result object.

Clarification keeps the same request ID/content/profile and accepts an
explicit `subjectId` (an owned ID, `new_subject`, or `none`). Editing the message
starts a new request. Receipts let Desktop restore previous messages and
choices after a reload; interrupted requests retain their retry identity.

## Verification

Run `go test ./internal/subjectrouting ./internal/chat ./internal/runapi` and
the repository's `make verify` gate. PostgreSQL integration tests use
`ANVIL_AGENTS_TEST_CHAT_DATABASE_URL` pointed at a disposable loopback database.

After `npm ci && npm run build` in `web/desktop`, run the isolated UI protocol
check with `node hack/desktop-subject-routing.browser-test.mjs`. For the built
UI against the actual Go API and PostgreSQL, set the disposable database URL
and `ANVIL_SUBJECT_BROWSER=1`, then run:

```sh
go test ./internal/runapi -run '^TestSubjectManagerBrowser$' -count=1 -v
```

Both browser scripts accept `PLAYWRIGHT_MODULE` and
`PLAYWRIGHT_CHROMIUM_EXECUTABLE` for an already installed browser runtime.
They exercise SSE updates, ambiguity resolution, receipt recovery, and narrow
layouts. The API fixture uses actual storage and HTTP routes. Identity,
classifier, Kubernetes, and harness completion are explicit fixtures; these
checks do not establish live Jev accuracy or production acceptance.
