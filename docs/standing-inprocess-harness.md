# Standing in-process harness + WebSocket chat delivery

Status: slice 3b (real harness process behind `standing.Backend`, stacked on
slice 3) — live InProcess turns execute through a real harness subprocess
with the same turn, gate, fanout, and WebSocket token-hub behavior the Fake
proved. Architectural direction locked by Austin 2026-09-18.

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
| WebSocket | `Upgrade: websocket` + versioned subprotocol (below) | 101, `snapshot` JSON text frame; Job-plane threads follow with `terminal` and close, while standing threads multiplex live `token` frames before the same `terminal` and close |

- Event contract parity: `snapshot` carries the thread, the durable message
  tail (`messages`, default 50, max 200; full history stays on the existing
  GET), `turns`/`activeTurn`, and the resumed `standing` session. `terminal`
  carries `standing_ready` (session resumed) or `job_plane` (thread stays on
  the Job plane — keep using the existing AgentRun `events` stream). Live
  `token` frames carry the full turn identity (`threadId`/`turnId`/`runName`
  + `seq` + `done`); the durable turn record stays the source of truth, so a
  dropped live frame never loses the reply.
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
- **InProcess (opt-in, slices 2–3 live):** interactive Wrapper/manager/peer
  chat where a standing session removes the per-turn cold start. Slice 1
  selected, named, resumed, and streamed (Fake); slice 2 executes turns
  through the backend behind the `standing.liveEnabled` /
  `ANVIL_AGENTS_STANDING_LIVE` opt-in gate (Fake-backed in tests — no model
  calls, no harness subprocess yet); slice 3 multiplexes live token frames
  into open standing WebSocket streams. Gate off still holds as
  `InProcessNotWired` with guidance and creates no Jobs.

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

## Slice 2: live standing backend in the turn path (landed)

Slice 2 wired a live `standing.Backend` **into** `reconcileChatTurn`
(`internal/runapi/chat_execution.go`), beside the `writes.Create` branch —
never around the durable turn:

- **Opt-in gate, deny by default.** `standing.liveEnabled` config plus the
  `ANVIL_AGENTS_STANDING_LIVE` environment variable (either enables; neither
  disables the other), and an attached backend — all required. No chart
  values, no Primaris sync changes. Gate off keeps today's Job /
  `NeedsHuman` hold behavior byte-identical: the run is created from the
  outbox-frozen intent and left untouched for the existing hold path, and no
  Job is ever created for it.
- **One durable AgentRun per accepted message, unchanged.** The run is still
  created from the outbox-frozen `turn.RunJSON` exactly as today; the
  standing backend consumes that frozen prompt and never bypasses it. After
  `EnsureTurnSession` + `StreamTurn` (bound to thread ID, turn ID, and run
  name in the sink), the full reply marks the run `Succeeded` so the
  existing `Succeeded` branch completes the turn: same 64KiB truncation,
  same `dispatchChatCoordination` peer fanout, same `CompleteTurn`.
  `SuspendIdleSession` runs best-effort on terminal reconciliation.
- **Peer child turns take the identical branch.** Peer deliveries already
  queue through `queueChatTurnInternal` into the same `reconcileChatTurn`,
  keyed off the recipient child thread — so a child whose profile resolves
  to `InProcess` streams through its own session with the same
  deterministic delivery IDs, while Job-plane parents and children are
  untouched.
- **Gate off, unresolvable harness, or any backend error falls back** to
  today's behavior (active hold; the controller's `InProcessNotWired`
  guidance surfaces on its normal pass). Queue, read-refresh, and
  background recovery racing on one turn are serialized by a per-turn
  singleflight guard in the API process (the chart runs one API replica).
- **Stream visibility without a protocol change.** The streamed reply lands
  in the durable turn record, so the existing snapshot tail already serves
  it over SSE and WebSocket (`standing_ready`); token events carry the full
  turn identity (`threadId`/`turnId`/`runName`) for the future live
  subscription, which stays a follow-up.

Stubbed vs live, explicitly: tests drive `FakeBackend` (deterministic
word-streamed prose — no model calls, no provider credentials, no harness
subprocess in the API process). The per-harness envelope wrapper
(`standingRunOutput`) is stub glue so the fake's plain text parses through
the existing reply extractors; a real harness process returns
native-enveloped output directly and the wrapper goes away with it. Crash
recovery between create and the `Succeeded` mark re-streams at least once;
a multi-replica claim and a controller-hold yield for API-owned standing
turns are later-slice work, not this slice.

## Slice 3: long-lived WebSocket token subscription (this slice)

Slice 3 keeps an open standing WebSocket stream past the snapshot so it
receives live `token` frames during an InProcess turn — not only the
post-turn snapshot. The turn-based model does not move: one append-only
AgentRun per accepted message, frozen intent in, `Succeeded` completion out.

- **Same snapshot, then tokens, then the same terminal.** A standing stream
  writes the snapshot (with the resumed `standing` session), subscribes to
  the thread's token hub before the upgrade completes, multiplexes `token`
  frames (`threadId`/`turnId`/`runName` + `seq` + `done`, `runName` stamped
  by `standingRunSink`), and closes with the existing `standing_ready`
  terminal. SSE stays the snapshot + terminal fallback and never
  subscribes. Job-plane and gate-off reads keep the snapshot + terminal +
  close lifecycle byte-identical and stay on the AgentRun events stream.
- **Process-local hub, non-blocking publish.** `standingTokenHub` fans
  stamped events out per namespace/thread (no cross-namespace leaks). The
  turn path publishes through the sink's downstream; a slow reader drops
  live frames instead of stalling the turn, and the durable reply still
  lands in the turn record, so the next snapshot tail stays complete.
- **Peer children multiplex on their own thread.** Each recipient child turn
  publishes under its child thread ID, so a parent stream never sees peer
  tokens and a child stream sees only its own.
- **Auth unchanged.** No tokens in query strings; bearer header or the
  verified `bearer.<token>` subprotocol, with the same exact-origin CORS
  enforcement before the upgrade.

Stubbed vs live, explicitly: tests drive `FakeBackend` (no model calls, no
harness subprocess). Desktop's `openChatThreadStream` already handles generic
event types, so token frames flow through its `onEvent` handler with no
protocol break; full Desktop e2e stays a later slice.

## Slice 3b: real harness process behind standing.Backend (this slice)

Slice 3b replaces the FakeBackend-only live path with
`standing.ProcessBackend` (`internal/standing/process.go`): `EnsureTurnSession`
session identity stays process-local and turn-based, while `StreamTurn` runs
one real harness subprocess per accepted message and streams its stdout as
live token events through the same sink → token-hub → WebSocket path slice 3
built. One append-only AgentRun per message, the opt-in
`standing.liveEnabled` / `ANVIL_AGENTS_STANDING_LIVE` gate, peer fanout, Job
defaults for scouts/batch, and the optional Substrate plane are all
unchanged.

- **Closest in-process adapter, no second protocol.** The recipe table
  (`processRecipes`) mirrors the delegatable entries of the desktop PATH
  catalog (`internal/desktop` `Catalog`): same binaries, same constant argv,
  same prompt transport (stdin, 0600 prompt file, or agy stream-json), and
  the same ambient-credential posture. The subprocess's native stdout is the
  reply, returned verbatim: the runapi Fake envelope wrapper
  (`standingRunOutput`) is skipped for this backend via the
  `ReturnsNativeEnvelope` gate (`standingTurnOutput`), so native output is
  never double-wrapped and the existing per-harness reply extractors parse
  it directly.
- **Turn-based, not a daemon.** Each turn spawns one subprocess bound to the
  durable turn identity; there is deliberately no continuously running native
  CLI session. The snappiness win is skipping the Job/Pod cold start
  (~8–44s measured on Primaris), not skipping process start. Subprocesses
  are serialized per thread session; peer child threads own their own
  sessions, so peer fanout still runs concurrently across threads.
- **Fail closed everywhere.** Unknown sessions, oversized prompts, a missing
  CLI on PATH, a non-zero exit, a timeout (2 minutes, desktop-delegate
  default), empty output, or a Fake-only harness kind all return an error
  and the turn keeps today's hold behavior (`InProcessNotWired`, no Job).
- **No Secret surface.** The backend never reads Secrets and gains no Secret
  RBAC. Provider credentials stay in the harness CLI's own auth home
  (`~/.codex/auth.json`, …); the child env is the filtered ambient env (no
  `KUBE*`/`KUBERNETES_*`, no OIDC/token material, no `KUBECONFIG`, no
  `*DATABASE_URL`). No Primaris Argo changes, no chart values.
- **Wiring.** `cmd/anvil-agents-api` attaches `NewProcessBackend(nil)`
  (default `ExecRunner`: PATH-resolved CLIs) exactly when the live gate is
  on. Gate off attaches nothing, so hold behavior is byte-identical.

Live vs Fake-only harness kinds, explicitly:

| Harness kind | Slice 3b | Notes |
| --- | --- | --- |
| `codex` | live subprocess (`codex exec --skip-git-repo-check`, stdin) | native envelope parsed by the codex extractor |
| `openCode` | live subprocess (`opencode run`, stdin) | |
| `openClaw` | live subprocess (`openclaw agent --message-file`, 0600 file) | |
| `grokBuild` | live subprocess (`grok --prompt-file`, 0600 file) | |
| `primeAgent` | live subprocess (`prime-agent --print --mode json --no-session`, stdin) | |
| `agy` | live subprocess (stream-json user event on stdin) | |
| `hermesAgent`, `piAgent` | Fake-only | inventory-only in the desktop catalog: no documented prompt-safe local invoke |
| `custom` | Fake-only | operator-owned container image: no local binary contract |
| empty kind | Fake-only | names no process; fails closed to hold |

Tests: Fake still drives every slice-2/3 unit test (no model calls anywhere
in unit). Slice 3b adds `internal/standing/process_test.go` (stub `Runner`
for session/streaming/validation semantics; stub shell binaries for the real
`ExecRunner` stdin/file transports, failure, and missing-CLI paths) and
`internal/runapi/chat_standing_process_test.go` (native reply end to end
through `queueChatTurn`: verbatim output, no double wrap, warm reuse, peer
child on its own session, failure and Fake-only kinds keep hold).

## NEXT (slice 4 and beyond)

1. **Persistent native session resume** (pass a harness session ID across
   turns where the CLI supports it) now that the process path is live; then
   retire the envelope wrapper entirely once no Fake-only live path remains.
   Provider credentials stay outside the API's Secret surface throughout.
2. **Controller-hold yield + multi-replica claim** for API-owned standing
   turns (annotation claim the controller respects), so the
   `InProcessNotWired` hold can never race a live stream.
3. **Latency compare** (direct turns AND peer deliveries, warm resume vs the
   Job cold-start baseline on the same cluster shape) with promote/reshape/
   retire bars mirroring the Substrate spike, then wire Desktop chat to
   `openChatThreadStream` end to end.
