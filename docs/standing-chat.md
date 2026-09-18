# Remote harness chat

Standing chat is an OIDC API feature backed by PostgreSQL. A conversation selects
an existing AgentRunProfile, an existing AgentHarnessProfile, or a profile with a
harness override. Harness-only conversations do not create profiles. Agent and
Manager are roles on the same chat feature; neither fixes the model provider.

Every accepted message creates one append-only AgentRun with the conversation
history. The API saves the real assistant reply extracted from that runner's
native output. This is turn-based execution, not a continuously running native
CLI session. Running Jobs retain their original prompt. Native mid-generation
steering and interruption are not implemented by this chat feature.

## Desktop workflow

1. Sign in, open Chat, and select a namespace.
2. Choose an agent or Harness only, then select the remote harness. An agent's
   configured harness remains the default.
3. Choose Agent or Manager. Optionally enable message delegation and select
   allowed peers. The server restricts peers to the same namespace and
   application scope.
4. Send a message. The UI shows waiting/queued/running state and runner activity.
   Replies and peer delivery receipts survive reloads and API restarts.
5. Open a peer conversation to see that agent's independently generated reply.

New conversations can select a different harness. Existing conversations retain
their selected identity and harness. Provider authentication and permissions come
from the selected harness/profile, including its existing mounted home and
service account. Selecting a harness does not authenticate its provider.

## Coordination

Coordination is opt-in for either role. Thread metadata contains:

```json
{"coordination":{"enabled":true,"allowedProfiles":["desktop-reviewer"]}}
```

The selected harness receives a bounded inventory of actual active work for its
allowed peers and may return:

```json
{"reply":"I asked the reviewer to check that.","messages":[{"profileName":"desktop-reviewer","content":"Review the supplied proposal."}]}
```

The API validates the entire batch before dispatch. Each recipient runs its own
configured harness. A parent turn can address up to four distinct allowed peers;
the allowlist contains at most eight profiles. Deterministic delivery IDs prevent
retries from starting the same peer work twice. Busy recipients wait durably.
Delivery receipts link to the real peer conversation and run. Children do not
inherit delegation permission, bounding fanout and preventing recursive loops.
The manager can use the work inventory to avoid duplicate assignments; the
service does not claim to detect all semantically duplicate tasks.

Existing controller requestPeer/interruptDuplicate behavior remains separate.
A chat delivery receipt is not evidence that a live generation was interrupted.

## Persistence and recovery

The API mounts `ANVIL_AGENTS_CHAT_DATABASE_URL` from the existing archive database
Secret, or `api.chatDatabaseURLSecret` when explicitly configured. Chat does not
read Kubernetes Secret values and adds no Secret RBAC.

Schema `anvil_agents_chat` owns threads, ordered messages, and durable turn outbox
records. Accepting a turn and its user message is one database transaction. The
outbox freezes the execution intent before Kubernetes creation. Stable names and
verified run identities prevent duplicate execution after ambiguous writes.
Completion appends one assistant message atomically. A background worker resumes
queued turns after restart; GET reconciliation also refreshes their state.

Only one active turn is accepted for a conversation and its execution target.
A retry with the same requestId/content returns the original accepted turn;
reusing an ID with different content is rejected. Explicit peer deliveries wait
for an occupied target instead of dropping the message.

## Configuration and API

Enable `api.enabled`, `api.config.chat.enabled` and
`api.config.runs.createEnabled`. The database URI remains a Secret environment
reference. Native Desktop authentication uses `api.config.ui.desktop.oidcClientId`.
Use exact allowed loopback CORS origins and existing namespace-scoped OIDC
bindings. Chat read/write permissions do not bypass runs:create. Coordination
also requires runs:read for the current-work inventory.

| Endpoint under `/api/v1/namespaces/{namespace}` | Behavior |
| --- | --- |
| `GET /chat/threads` | List saved conversations (chat:read) |
| `POST /chat/threads` | Create profile/harness-bound conversation (chat:write) |
| `GET /chat/threads/{id}` | Messages, turns, active turn and delegate receipts (chat:read) |
| `GET /chat/threads/{id}/messages` | Ordered saved messages (chat:read) |
| `POST /chat/threads/{id}/messages` | Accept a turn (chat:write and runs:create) |

Create body: `profileName?`, `harnessProfileName?`, `mode` (`persona` or `fleet`),
`title?`, `metadata?`. At least one execution selector is required. Append body:
`content`, `requestId?` (UUID; clients should always send one for safe retries).
The append response is HTTP 202 `{thread,user,turn}`. It does not contain an
invented immediate assistant reply. Thread detail exposes `turns` and `activeTurn`.

Threads are namespace-shared under the existing authorization model, not private
per-user message storage. The creating subject is recorded. Changing a person's
access affects subsequent HTTP requests; already accepted work retains its
execution intent.

## Validation

`make verify` runs local source, chart and runner checks. Run
`hack/test-archive-postgres.sh` for real PostgreSQL migration, outbox concurrency,
idempotent completion and deferred-peer activation. Desktop browser contract
checks are in `hack/desktop-remote-chat.browser-test.mjs`; set PLAYWRIGHT_MODULE to
an installed Playwright module. These fixtures prove UI behavior, not provider
authentication. Release verification separately records authenticated live API,
actual runner replies and browser results.

## Helm-owned Desktop profiles

The Primaris overlay includes `desktop-assistant` and `desktop-reviewer` in
`anvilhub`, with application scope `desktop-chat` and the existing
`anvil-primaris-opencode-observe` harness. They have neutral text-only prompts
and no autonomous schedules. The Desktop can override either harness on a new
conversation. These are ordinary interactive profiles, not a self-development
fleet or manager policy authority.

The chart's optional `extraObjects` list renders literal Kubernetes objects
without evaluating templated strings. Helm owns those objects: removing an entry
on a subsequent Helm upgrade deletes it. Keep persistent volumes, credentials
and unrelated existing resources out of this list. The API treats these
Helm-owned profiles as read-only composition entries because they are not
console-managed. Their definitions stay in Git; they do not carry the
Argo-exclusive `ownership=gitops` label.

### PostgreSQL TLS mounts

A database URL containing `sslrootcert=/path/ca.crt` requires that exact file in
the API container. Configure `api.extraVolumes` and `api.extraVolumeMounts`;
controller mounts are intentionally independent. The Primaris overlay projects
only the existing Secret's `ca.crt` key into the API mount. Also verify PostgreSQL
admits the API node's egress address before enabling chat.

## Optional Substrate actors (spike)

A new conversation can select a harness profile whose `execution.runtime` is
`SubstrateActor` to make its turns eligible for the warm-actor plane. Existing
conversations keep their selected harness, and the controller never creates a
Job for SubstrateActor runs: with the live gate off it holds them as
`SubstrateActorNotWired`, and with the gate on it will bind the warm actor
once the pending ATE dialer lands (until then the operator fails fast with
the gate enabled). See
[Substrate spike](substrate-spike.md).
