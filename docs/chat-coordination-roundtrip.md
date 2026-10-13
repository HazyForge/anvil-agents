# Correlated peer replies in standing chat

An opted-in coordination thread can now complete one bounded round-trip:
user task → originating harness A → allowlisted recipient harness B → persisted
peer result → automatic A synthesis → final answer in the original conversation.
It uses the existing chat outbox, profile scope checks, deterministic delivery
IDs and recipient harness selection. It creates no alternative mailbox or bus.

Create a thread with:

```json
{"profileName":"agent-a","mode":"persona","metadata":{"coordination":{"enabled":true,"allowedProfiles":["agent-b"],"returnReplies":true}}}
```

`returnReplies` is opt-in for compatibility. Desktop enables it when creating a
new conversation with explicit allowed peers. Existing threads without the flag
retain their previous delivery-only behavior.

The original execution remains an immutable success receipt when it has queued
its messages. Its separate durable `coordination.status` is `waiting`, then
`resuming`, and finally `done`, `failed` or `cancelled`. Waiting releases the
originating harness/home lock but blocks another user turn in that conversation.
Each child uses its own configured harness and ordinary exclusive resources.
The synthesis turn obtains those same resource locks on activation.

Recovery waits for every exact child turn to become terminal. It verifies the
service-controlled source thread/turn/profile relation and reads the persisted
reply for that child’s current execution receipt. Failed peers contribute failure
data. Up to four results are bounded by their encoded JSON size and labelled as
data, not permission grants. The service atomically inserts one deterministic
synthesis outbox entry and changes the originating journey to `resuming`.
The synthesis cannot delegate or invoke intent routing again.

Desktop polls durable state and shows waiting, preparing-final, completed and
cancelled receipts. It hides the internal synthesis instructions behind a compact
peer-results receipt. The original live stream ends with its originating execution;
the later final answer arrives through the persisted conversation refresh.

`POST /api/v1/namespaces/{namespace}/chat/threads/{threadId}/turns/{turnId}/cancel`
cancels this journey using the existing chat-read/write permissions. Repeated
cancellation is idempotent. It suppresses late peer/synthesis replies and prevents
new synthesis admission. The API interrupts its own standing contexts; execution
locks remain held until teardown or a terminal execution receipt. A foreign API
replica or Job is not forcibly killed. If its owner disappears without a terminal
receipt, its exclusive resources remain reserved for operator reconciliation.
This endpoint becomes available after the originating turn has queued peers; it
does not add general interruption of the original pre-delegation turn.

## Local autonomous proof

```sh
python3 hack/chat-coordination-e2e/run.py
```

The script builds the actual API fixture and `anvil-actor-acp`, starts two actual
adapter binaries with inert Python ACP subprocesses, sends one user message, and
checks automatic A→B→A→user delivery. The profile kinds exercise the existing
Codex/OpenCode output parsers; neither actual CLI nor any provider/model is called.
Authentication is a fixed local fixture marker, with no OIDC credentials. API and
adapter sockets bind only to IPv4 loopback. Kubernetes is in-memory. The default
chat store is memory; an explicitly supplied isolated loopback PostgreSQL database
named `anvil_chat_inert` exercises the same real durable implementation.

No package/model downloads are performed. A compatible existing Go toolchain and
cached module dependencies are required. Set `INERT_CHAT_E2E_GO` to an existing Go
executable if needed. Artifacts default to ignored `.runtime/chat-coordination-e2e`;
`INERT_CHAT_E2E_OUTPUT` may select a writable output directory.

The proof checks sender/correlation, one final answer, four concurrent duplicate
submissions, twelve reconnect reads, waiting/running cancellation, cancellation
during synthesis, one terminal failure explanation without peer replay, and the
existing cross-application allowlist boundary. It stops every owned subprocess.
Store race tests additionally cover atomic duplicate synthesis admission,
cancellation against enqueue/completion, resource exclusion and store reconnect.
Browser contract tests cover stale journey snapshots and receipt visibility.

This proves local protocol and UX plumbing. Live non-Grok harness compatibility,
provider authorization, model cost, ATE routing, production PostgreSQL and Desktop
deployment remain separate validation steps. No scans or security settings change.
