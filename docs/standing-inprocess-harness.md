# Standing in-process harness + WebSocket chat delivery

Status: slice 1 (API-first) — architectural direction locked by Austin
2026-09-18. This is the first slice of the standing in-process +
WebSocket interactive path.

## Direction

1. Prefer **standing in-process harnesses** for interactive chat snappiness:
   one long-lived harness/model session per agent thread (Grok-Bot feel)
   instead of paying a cold Job/Pod start (~8–44s measured on Primaris) per
   turn.
2. Prefer **WebSocket + streaming** for chat delivery / first-token feel.
3. The architecture MAY change, but the **same product results** must hold:
   multi-harness cooperation (Wrapper/managers/peers, `requestPeer`), Jobs
   still work for scouts/batch, and chatting still creates real durable
   turns/AgentRuns.

Context already on `master`: the optional SubstrateActor/ATE plane
(#206–#211) stays as the optional isolation/density plane — it is NOT ripped
out, and Jobs remain the default for scouts. Desktop chat streaming exists
(SSE via `openAgentRunStream`); standing-chat persistence and the chat
delivery contract live in [Standing Chat](standing-chat.md) and
[Chat delivery](chat-delivery.md).

## How a standing process owns a thread's harness session across turns

One chat thread owns exactly one standing session, resumed across turns.
Standing chat today is **turn-based, not a continuously running native CLI
session**: every accepted message creates one append-only AgentRun, and that
does not change here. A standing session evolves the model by keeping warm
harness/model context (session identity, working state, backend envelope)
across turns so interactive turns skip the cold start — it never substitutes
for the durable turn record:

- Every accepted message still creates exactly one append-only AgentRun
  through the same turn path (`queueChatTurn` in
  `internal/runapi/chat_execution.go`), with the execution intent frozen in
  the durable turn outbox (`QueueTurn`) before anything executes. The
  outbox, stable run names, idempotent completion, and background recovery
  (`runChatRecovery`) are untouched.
- `reconcileChatTurn` still binds the turn to its recorded AgentRun identity
  (turn/thread labels, `ChatThread` source ref, UID) and still completes the
  turn with the real assistant reply via `CompleteTurn`. A standing backend
  consumes the outbox-frozen intent; it never bypasses it.
- Peer deliveries still fan out through `dispatchChatCoordination` into one
  durable child thread per recipient, each completed through the same turn
  path with deterministic delivery IDs. Each thread (parent or peer child)
  owns its session via the same `EnsureTurnSession` path (see below).
- Running executions keep their original prompt; native mid-generation
  steering and interruption remain unimplemented, as documented in
  [Standing Chat](standing-chat.md).
- Jobs stay the default plane for scouts, batch, scheduled, and chained
  runs, and the optional SubstrateActor isolation/density plane is
  untouched.

The session contract itself (`internal/standing.Backend`):

- `internal/standing.Backend` is the thin contract: `CreateSession` (or
  reuse), `ResumeSession` before a turn, `StreamTurn` for token delivery,
  `SuspendSession` when the turn goes idle. Slice 1 ships `FakeBackend`
  (in-memory, warm-reuse semantics) so every behavior is unit-tested with no
  live harness.
- `standing.SessionNameForThread(threadID)` maps any thread — Wrapper,
  manager, or peer child — to one stable session name (`standing-…`, total
  over thread IDs, hash-suffixed past the DNS bound).
- `standing.EnsureTurnSession` binds a turn to its session and reports warm
  reuse: resume when the session exists, create exactly once when it does
  not. Suspend-on-idle defaults to on; suspend is density only, never a
  correctness gate (a suspended session still resumes warm).
- Selection is opt-in per harness profile: `execution.runtime: InProcess`
  (orthogonal to `backend.kind`, exactly like `SubstrateActor`). Empty still
  means `Job`. The controller holds well-formed `InProcess` runs as
  `NeedsHuman/InProcessNotWired` without creating a Job until a live backend
  is wired behind an explicit opt-in gate — so the Job default cannot
  regress. A `substrate` section on an `InProcess` runtime fails closed as
  `InvalidSubstrateSpec`.
- The runapi chat stream endpoint resumes the thread's session on read
  (`resumeChatStreamSession`) and reports it in the snapshot (`standing:
  {sessionName, harness, warm, resumes}`). No execution happens on read;
  the session simply stays warm for the next turn.

## How WebSocket streaming maps to the existing chat delivery contract

`GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/stream`
delivers the **same events over two transports**:

| Transport | Request | Response |
| --- | --- | --- |
| SSE (default) | `Accept: text/event-stream` + `Authorization: Bearer …` | `snapshot` + `terminal` events, then close |
| WebSocket | `Upgrade: websocket` + versioned subprotocol (below) | 101, same `snapshot` + `terminal` JSON text frames, then server close |

- Event contract parity: `snapshot` carries the thread, the durable message
  tail (`messages`, default 50, max 200; full history stays on the existing
  GET), `turns`/`activeTurn`, and the resumed `standing` session. `terminal`
  carries `standing_ready` (session resumed) or `job_plane` (thread stays on
  the Job plane — keep using the existing AgentRun `events` stream).
- Nothing about the delivery contract moves: AgentRun stays append-only, new
  intent creates a new run, `purpose=interactive` scoping and the
  queue/steer/interrupt non-goals in [Chat delivery](chat-delivery.md) are
  unchanged. Streaming is delivery, not storage — the durable turn record
  still comes from the existing turn path.
- Auth stays deny-by-default and provider-neutral: bearer in the
  `Authorization` header, or — because browsers cannot set headers on a
  WebSocket — a verified `bearer.<token>` subprotocol alongside the selected
  `anvil-agents.chat-stream.v1` protocol. Query-string tokens are rejected
  on this route like everywhere else. Browser handshakes keep the existing
  exact-origin CORS enforcement (the shared middleware runs before the
  upgrade).
- Desktop: `web/desktop/src/api/chatThreadStream.ts` opens the stream
  WebSocket-first (`openChatThreadStream`) and falls back to authenticated
  SSE when the upgrade fails (Electron constraints, proxies, older
  servers). No token ever enters a query string.

## How peer cooperation still works

Unchanged, by construction:

- Every accepted message still creates a real durable turn/AgentRun through
  the same turn path (`queueChatTurn` / durable peer dispatch). A standing
  session accelerates turns; it never replaces them.
- `create-agent` stays Wrapper/manager-only; peers request via
  `requestPeer`. Peer coordination still creates one durable child thread
  per recipient with deterministic delivery IDs, and each child turn still
  runs its own configured harness.
- Each thread owns its session through the same `EnsureTurnSession` path:
  the parent message resumes the parent thread's session, a peer delivery
  resumes the recipient child thread's session. Retried deliveries converge
  on the same warm session; the durable wait for busy recipients still owns
  queueing, so a warm session never drops a queued peer turn.
- Merge rules carry `execution.runtime` wholesale (profile default, run
  override), so a harness-profile swap selects the plane without touching
  composition boundaries.

## When to use which plane

- **Job (default):** scouts, batch, scheduled, chained, and every run that
  must survive the API process or scale across nodes. One Job per AgentRun,
  unchanged.
- **SubstrateActor (optional):** warm-actor isolation/density when operating
  a Substrate/ATE gateway pays off (see [Substrate spike](substrate-spike.md)
  for the promote/reshape/retire bars). Unchanged by this slice.
- **InProcess (opt-in, slice 1 API-first):** interactive Wrapper/manager/peer
  chat where a standing session removes the per-turn cold start. Slice 1
  only selects, names, resumes, and streams (Fake); live harness execution
  behind it is the next slice. Until then `InProcess` runs hold as
  `InProcessNotWired` with guidance and create no Jobs.

## Explicit non-goals for slice 1

- No live harness execution in-process (no model calls, no provider
  credentials in the API process). `StreamTurn` against a real harness is
  the next slice.
- No long-lived subscription yet: both transports serve snapshot + terminal,
  then close. Live token frames into an open stream are the next slice.
- No new execution identity from reads: the stream endpoint creates no
  AgentRuns and records no turns (preview tokens would lie about
  durability).
- No mailbox/steer/interrupt, no generation-interrupt hook, no Stop control
  — the [Chat delivery](chat-delivery.md) non-goals still hold.
- No Primaris Argo changes, no chart values for the plane, no Secret access
  for the API beyond what standing chat already mounts (database URI only).

## NEXT (exact next PR)

Wiring the live backend **into the turn path** (not around it), so every
standing turn remains one durable AgentRun:

1. **Live `standing.Backend` behind an explicit opt-in gate** (env/flags,
   off by default; no chart values, no Primaris sync changes). In
   `reconcileChatTurn` (`internal/runapi/chat_execution.go`), beside the
   `writes.Create` branch (marked with an anchor comment in slice 1): when
   the thread's harness resolves to `execution.runtime: InProcess` and the
   gate is on, create the append-only AgentRun from the outbox-frozen
   `turn.RunJSON` exactly as today, then `EnsureTurnSession` for the thread
   and `StreamTurn` bound to the turn's identity (thread ID, turn ID, run
   name in the sink metadata). Persist the returned full reply through the
   existing `CompleteTurn` path (including the 64KiB truncation and
   `dispatchChatCoordination` peer fanout), and `SuspendIdleSession` on
   terminal reconciliation (best-effort). Gate off (or unresolvable
   harness, or any backend error) falls back to today's behavior: the run
   holds as `InProcessNotWired` and no Job is ever created for it.
2. **Peer turns take the identical branch** via their recipient child-thread
   source: resume the child thread's session, stream the child turn, complete
   it with the same deterministic delivery IDs. No new peer protocol; the
   durable wait for busy recipients still owns queueing.
3. **Long-lived stream subscription**: keep the WS connection open past the
   snapshot and multiplex `token` frames (`TokenEvent`: thread/turn/seq +
   done marker) for the turn bound in step 1, with the same terminal codes;
   SSE stays the fallback. Bound connection counts already exist via the
   shared stream limiter.
4. **Latency compare** (direct turns AND peer deliveries, warm resume vs the
   Job cold-start baseline on the same cluster shape) with promote/reshape/
   retire bars mirroring the Substrate spike, then wire Desktop chat to
   `openChatThreadStream` end to end.
