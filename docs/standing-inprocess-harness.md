# Standing in-process harness + WebSocket chat delivery

Status: process-exec cold-first + resumed-second turn measurement path on top
of slice 5c (chart/RBAC enablement for API-owned standing turns), Desktop chat
e2e over the standing WebSocket, slice 5b (persistent native session resume
for ProcessBackend), slice 5a (standing vs Job latency compare harness),
slice 4 (controller-hold yield + multi-replica claim for API-owned standing
turns) and slice 3b (real harness process behind `standing.Backend`) —
exactly one API replica drives a standing turn through an annotation claim
the controller respects, while open standing streams multiplex live token
frames during the turn and the durable turn record stays the source of truth.
Architectural
direction locked by Austin 2026-09-18 and reaffirmed the same day: standing
in-process harness + WebSocket is the **primary** interactive path.
Substrate/ATE stays **optional** for isolation/density (see [Substrate
spike](substrate-spike.md)) — not promoted, not default-on. Jobs stay for
scouts/batch.

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
turns landed in slice 4.

## Slice 3: long-lived WebSocket token subscription (landed)

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

## Slice 3b: real harness process behind standing.Backend (landed)

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

## Slice 4: controller-hold yield + multi-replica claim (this slice)

Slice 4 keeps the turn-based model (one append-only AgentRun per accepted
message, frozen intent in, `Succeeded` completion out) and adds the
ownership contract so multiple API replicas can safely own standing turns
without double-driving one. It composes with every `standing.Backend`
(`FakeBackend` in tests, `ProcessBackend` live): claiming happens around
`StreamTurn`, never inside it.

- **Claim contract.** Before streaming, the driving replica stamps
  `control.anvil.hazyforge.io/standing-claim` on the turn's AgentRun
  (`internal/standing.Claim`: durable turn ID + replica owner
  `hostname/pid` + Unix-seconds timestamp, structured JSON, no secrets).
  The winner is decided by optimistic concurrency — the first metadata
  `Update` to land owns the turn. A lost write (conflict) or an already-live
  foreign claim keeps today's hold behavior: the loser never streams and
  observes the winner's `Succeeded` mark on a later pass. Gate off writes no
  claim, so gate-off / Job-plane / scout paths stay byte-identical.
- **Controller yield.** `agentRunInProcessHold` checks the same
  `standing.ClaimForTurn` predicate: a live claim for the run's chat turn
  yields `NeedsHuman/StandingClaimed` (still no Job) instead of racing the
  live stream with `InProcessNotWired`. Absent, malformed, mismatched, or
  stale claims keep the original hold. The yield needs no new RBAC — the
  controller only reads the annotation.
- **Takeover and liveness.** Claims expire after `standing.ClaimTTL` (90s)
  so an owner crash cannot wedge the turn: any replica may overwrite a stale
  claim and re-stream (at-least-once, matching existing crash recovery), and
  a `NeedsHuman` hold carrying this turn's stale-or-own claim is re-driven
  instead of failed. A `NeedsHuman` hold carrying a *live* claim never fails
  the turn — the holder completes it. A `NeedsHuman` hold with no claim
  still fails with guidance, exactly as before.
- **Tests.** `internal/standing/claim_test.go` pins encode/parse/liveness;
  `internal/runapi/chat_standing_claim_test.go` drives two replicas over one
  shared fake client + chat store (winner drives + stamps, loser holds and
  completes off the winner, stale takeover re-streams exactly once, live
  foreign hold stays active, unclaimed hold still fails);
  `internal/controller/agent_run_inprocess_test.go` pins the
  yield-vs-`InProcessNotWired` boundary.

Stubbed vs live, explicitly: tests drive `FakeBackend` (no model calls, no
harness subprocess). Full Desktop e2e stays a later slice.

## Slice 5a: standing vs Job latency compare harness (this slice)

Slice 5a measures the standing InProcess warm path against the documented Job
cold-start baseline, for a direct turn AND a peer delivery, with explicit
promote/reshape/retire bars mirroring the Substrate spike. The turn-based
model does not move: one append-only AgentRun per accepted message, frozen
intent in, `Succeeded` completion out; Jobs stay the default for scouts and
batch; no Primaris Argo changes; no Secret expansion.

- **What it measures** (`cmd/standing-latency`): per thread plane (direct
  thread, peer child thread) it times session cold-create (`EnsureTurnSession`
  on a fresh thread), warm resume (pre-bound session, `warmOps` counts the
  resumes), the full warm turn (resume + `StreamTurn`), and time-to-first-token
  from `StreamTurn` start (Desktop `firstTokenMs` vocabulary) — plus, since
  slice 5c, cold FIRST turns (`*TurnCold` / `*FirstTokenCold`: fresh thread,
  first `StreamTurn`, records the slice-5b native session id when the harness
  kind supports one) and resumed SECOND turns (`*TurnResumed` /
  `*FirstTokenResumed`: same thread, second `StreamTurn` reusing the recorded
  native id). Sixteen scenarios (the eight `direct…` above plus `directTurnCold`,
  `directFirstTokenCold`, `directTurnResumed`, `directFirstTokenResumed`, and
  the eight `peer…` peers), each with
  `count/minMs/meanMs/p50Ms/p95Ms/maxMs`, plus a `nativeResume` section
  (`supported`, `coldTurns`, `resumedTurns`, `nativeIDsObserved`, `note`)
  proving whether second turns actually resumed a native session or honestly
  ran cold. `-only` (also `--only` on the compare script) runs a
  comma-separated scenario subset for cheap live probes; the bars need the
  full matrix, so a subset `process-exec` run reports `inconclusive-live`.
- **Backends.** `fake` (default) drives `standing.FakeBackend`: deterministic,
  no cluster, no subprocess, no model call — CI-safe. `process-stub` runs the
  real `ProcessBackend` session/streaming code behind a fixed-latency
  resume-aware stub runner (still no PATH binary, no credentials): the stub
  implements `standing.SessionRunner`, so resume-supporting kinds record the
  deterministic placeholder `stub-ses-latency-probe` on cold turns and resume
  it on second turns. `process-exec` (optional
  local only) runs one real harness subprocess per measured turn through
  `ExecRunner` with the desktop delegate's filtered env; it fails closed when
  the harness CLI is missing and never reads Secrets.
- **Job baseline.** The harness cannot measure Job scheduling itself, so the
  comparison defaults to the documented Primaris Pod-ready figures (~12s p50 /
  ~44s p95, `-job-baseline-p50-ms` / `-job-baseline-p95-ms`). Those figures
  cover Job create to Pod ready only — full turn time (harness/model time on
  top) is separate; compare full turns via the Desktop chat-latency JSONL
  (`waitingMs`/`firstTokenMs`/`runningMs`/`replyReadyMs`, see
  `internal/desktop/chat_latency.go`). Pass `0`/`0` for raw warm-path numbers
  with no comparison.
- **How to run.**

```bash
# Deterministic CI path (default): FakeBackend, artifact under .runtime/.
hack/standing-latency-compare.sh --iterations 20 --out .runtime/standing-latency-fake.json

# Stubbed ProcessBackend code path (still no binary, no credentials).
hack/standing-latency-compare.sh --process-stub --iterations 20 --out .runtime/standing-latency-stub.json

# Optional local live path: real CLI per turn (needs e.g. codex on PATH
# plus that CLI's own local auth, e.g. ~/.codex/auth.json — never a Secret).
hack/standing-latency-compare.sh --live --harness codex -n 10 --out .runtime/standing-latency-live.json
# Equivalent direct binary run:
# go run ./cmd/standing-latency -backend process-exec -harness codex -n 10 -out .runtime/standing-latency-live.json

# Cheap live probe: cold first turns + resumed second turns only.
hack/standing-latency-compare.sh --live --harness codex -n 5 \
  --only directTurnCold,directTurnResumed --out .runtime/standing-latency-live-cold-resume.json
```

Live collection checklist (same cluster shape as the Job baseline; provider
credentials stay in each CLI's own auth home and outside the API Secret
surface throughout):

1. `command -v <cli>` (`codex` | `opencode` | `openclaw` | `grok` |
   `prime-agent` | `agy` for harness kinds `codex` | `openCode` | `openClaw` |
   `grokBuild` | `primeAgent` | `agy`).
2. Authenticate the CLI itself once with its own login flow and confirm one
   manual turn works, e.g.
   `printf 'reply briefly: hi' | codex exec --skip-git-repo-check --json | head -c 300`.
   Resume-supporting kinds (`codex`, `openCode`, `openClaw`) resume the
   recorded native session on second turns; `grokBuild` / `primeAgent` / `agy`
   and Fake-only kinds honestly run cold (the report's `nativeResume.note`
   says so).
3. Run the compare script with `--live` (full matrix for a bars verdict, or
   `--only` for a cold+resume probe).
4. Compare `directTurnCold` vs `directTurnResumed` p50/p95 and check
   `nativeResume.nativeIDsObserved` (4×n when every cold turn records and
   every resumed turn reuses); feed the bars below. Re-measure Desktop
   send→firstToken via `.runtime/chat-latency.jsonl` on the same shape.

The report is gitignored local JSON (`.runtime/standing-latency-*.json`,
also printed to stdout) with the sixteen scenarios, `nativeResume`,
`jobBaseline`,
`comparisonMs` savings of warm, cold, resumed, and first-token turns against
the Job
baseline, and a `verdict` + `verdictReason`. Deterministic backends always
report `harness-ok` — fake numbers prove the harness and the warm-reuse
contract, never a promotion. Only `process-exec` live numbers on the same
cluster shape as the baseline feed the bars:

| Verdict | Bar |
| --- | --- |
| `promote` | Warm turn p95 an order of magnitude under the Job p95 (≤ p95/10) for BOTH direct and peer, with first-token p95 in the low single seconds (≤ 5000ms). Promoting means graduating the opt-in gate toward default-on for Wrapper/manager standing chat while scouts and batch stay on Jobs. |
| `reshape` | Direct wins clearly but peer does not (direct turn p95 ≤ p95/10, peer turn p95 above it). Keep standing for direct turns, route peer fanout back to Jobs — or reshape the peer path (per-recipient warmth) and re-measure before promoting peers. |
| `retire` | Warm turn p50 within noise of the Job p50 (under a 2x win). The standing plane does not pay for itself; remove the live backend wiring while keeping the `execution.runtime` API surface parked per a planned migration. |
| `inconclusive-live` / `harness-ok` / `no-baseline` | Between the bars, deterministic-only, or no baseline passed: collect more live iterations on the same cluster shape before deciding. |

Tests: `cmd/standing-latency/main_test.go` pins the report shape (sixteen
scenarios, `warmOps` per warm scenario), the `nativeResume` record/reuse
accounting (resume-aware stub records + resumes, fake and resume-unsupported
kinds honestly run cold), the `-only` subset flag and its withheld live
verdict, the documented baseline defaults, the fail-closed backend
selection, and the `harness-ok` ceiling for deterministic backends — all with
no cluster and no subprocess.

## Slice 5c: process-exec cold-first + resumed-second measurement path (this slice)

Slice 5c makes the `process-exec` live-number path as runnable and documented
as possible without a harness CLI in the agent environment — no live numbers
are captured here, and none are invented. The turn-based model does not move:
one append-only AgentRun per accepted message, frozen intent in, `Succeeded`
completion out; no Primaris Argo changes, no Secret expansion, no
Substrate/WarmPool changes, no chart/RBAC changes.

- **Cold first turns AND resumed second turns are explicit in flags and
  output JSON.** `cmd/standing-latency` gains `*TurnCold` /
  `*FirstTokenCold` (fresh thread, first `StreamTurn`) and `*TurnResumed` /
  `*FirstTokenResumed` (same thread, second `StreamTurn` reusing the slice-5b
  native session id when `standing.SupportsNativeResume` says the kind
  documents one) for both planes, plus the `nativeResume` report section and
  the `-only` subset flag (`--only` on `hack/standing-latency-compare.sh`)
  for cheap live probes.
- **Stub gap wired shut.** `process-stub` now drives the real
  `ProcessBackend` record/resume plumbing through a resume-aware stub
  `SessionRunner` (deterministic `stub-ses-latency-probe` id, still no PATH
  binary, no credentials, no model call), so the cold-vs-resumed scenario
  shape is green in CI before any live CLI exists. Fake stays honestly cold
  with no native ids.
- **Live collection is a documented local run.** The exact Kind/local
  commands, CLI-to-kind mapping, per-CLI auth-home posture, and the
  cold-vs-resumed comparison to make live in the checklist under "How to
  run" above. Stub OIDC still blocks signed-in Desktop live samples, so the
  live path here is the standing-latency `process-exec` run, not Desktop
  OIDC.

Desktop e2e (slice #220) is done and stays untouched: this slice touches no
chart RBAC and no Desktop `standingChatTurn` code.

## Slice 5b: persistent native session resume (this slice)

Slice 5b keeps one subprocess per turn (no daemon harness) and adds native
session continuity where the CLI documents it: `ProcessBackend` records the
harness's durable session id per standing session and resumes it on the next
turn for the same thread, so warm turns keep model context beyond the
process-local `EnsureTurnSession` identity. The turn-based model does not
move: one append-only AgentRun per accepted message, frozen outbox intent in,
`Succeeded` completion out, peer fanout and the slice-4 claim/hold contract
unchanged.

- **Plumbing.** `standing.SessionRunner` (`internal/standing/process_resume.go`)
  is an optional `Runner` extension: `RunWithResume(handle, turnID, prompt,
  resumeID)` returns the reply plus the id the CLI actually served under.
  `StreamTurn` prefers it exactly when the kind supports native resume
  (`SupportsNativeResume`); plain `Runner`s (the latency `process-stub`,
  every existing test double) run every turn cold, byte-identical to slice
  3b. The recorded id is observable via `NativeSessionID` and never carries
  credentials — only the CLI's opaque session token.
- **Verified resume surfaces** (flags checked against the desktop PATH
  catalog recipes, the Job runner images, and the CLIs' documented
  reference; resume argv is catalog constants plus the validated id, exec'd
  with no shell):

| Harness kind | Resume argv | Id discovery |
| --- | --- | --- |
| `codex` | `exec resume <uuid>` (documented non-interactive resume subcommand; prompt still on stdin) | `thread_id` from the `thread.started` JSONL event (`codex exec` reference: "can be used to resume the thread later") |
| `openCode` | `run --session <ses_ID>` (`--session`/`-s`, "Session ID to continue"; unknown ids exit non-zero) | top-level `sessionID` every `--format json` event carries (verified in `run.ts`) |
| `openClaw` | `agent --session-key <key>` (Job image passes a fresh key per AgentRun) | client-assigned stable key per standing session (`NativeSessionKey`), no output parsing |

- **Cold argv alignment.** `codex` cold turns gain `--json` and `openCode`
  cold turns gain `--format json`, matching the Job runner images
  (`docker/agent-run-codex`, `docker/agent-run-openclaw`,
  `docker/agent-run-opencode`) and the shapes the reply extractors already
  parse — structured output is the only way the resume path can discover an
  id. `openClaw` cold turns are unchanged through plain `Run`; the
  `--session-key` rides the resume-aware path only.
- **Fail closed.** `grokBuild` (no documented resume flag), `primeAgent`
  (explicitly `--no-session`), `agy` (single-turn stream-json, no persistent
  conversation), and the Fake-only kinds (`hermesAgent`, `piAgent`, `custom`,
  empty) run cold and record nothing; the turn still succeeds, only the
  native-context win is skipped. Malformed recorded ids never reach argv.
  A resume the CLI rejects fails the turn back to hold (no silent cold retry
  inside the failed turn) and clears the recorded id, so the next turn
  re-discovers cold instead of wedging on a dead session.
- **No surface expansion.** No Secret access (the child env filter is
  untouched; provider auth stays in each CLI's own auth home), no Primaris
  Argo/chart changes, no RBAC changes — the slice-4 claim follow-up (API
  role `update`/`patch` on `agentruns`, `update` on `agentruns/status`)
  landed as slice 5c. `FakeBackend`, gate-off, Job-plane, and scout
  paths are byte-identical.

Tests: `internal/standing/process_resume_test.go` pins the plumbing with a
stub `SessionRunner` (first turn cold + records, second turn resumes,
rotation, unsupported kinds cold with nothing recorded, plain `Runner`
byte-identical, rejection holds + clears + re-discovers cold) plus the pure
argv/validation/extraction tables and real-`ExecRunner` subprocess turns
against stub shell binaries (codex `resume <uuid>`, openCode `--session`,
openClaw stable `--session-key`, primeAgent with no resume argv anywhere) —
no model calls, no credentials.

## Slice 5c: chart/RBAC enablement for API-owned standing turns (this slice)

Slice 5c lands the minimal Helm/RBAC chart change so standing live mode can
claim + mark Succeeded in multi-replica production. No Substrate/WarmPool
changes, no new execution path — the slice-4 claim and the Succeeded mark
already exist in code and degrade to hold behavior without these verbs.

- **Opt-in gate.** `api.config.standing.liveEnabled` (default `false`)
  renders into the API ConfigMap verbatim as `standing.liveEnabled`, so one
  value flips both the process gate and the RBAC. Gate off renders
  byte-identical read-only RBAC (`get`/`list` on `agentruns`), pinned by the
  exact rule contract in `hack/test-api-chart.sh`.
- **Exactly two additions, both scoped to standing turns.** Gate on grants
  `update`/`patch` on `agentruns` (the slice-4 claim stamp is a metadata
  `Update`) and `update` on `agentruns/status` (the streamed Succeeded
  mark is a status `Update`). No `create`/`delete`, no other resources, no
  Secret access — the status rule is update-only by construction.
- **Composes with existing gates.** External-trigger `create`/`patch`/
  `update` on `agentruns` is preserved without duplicating verbs when both
  gates are on; chat, composition, controls, and Secret rules are untouched.

Operator path: set `api.config.standing.liveEnabled=true` (standing chat
must already be enabled with its database Secret) and upgrade the release;
replicas past the upgrade claim through the annotation while older replicas
keep hold behavior, converging without wedging.

## Desktop chat e2e over the standing WebSocket (this slice)

Desktop Chat Send (`streamDesktopChat` in `web/desktop/src/wrapper/harnessChat.ts`)
now attempts one live turn over the slice-3 standing WebSocket token path
before its existing POST path. The turn-based model does not move: one
append-only AgentRun per accepted message (the POST carries one `requestId`
the server dedupes by, so an attempt and its fallback can never create two
turns), frozen intent in, `Succeeded` completion out, peer fanout unchanged.
No Primaris Argo changes, no Secret expansion, no chart/RBAC broadening —
Desktop opens the standing WS with its existing OIDC bearer and chat-read
grant.

- **Open first, then send.** `streamStandingChatTurn`
  (`web/desktop/src/wrapper/standingChatTurn.ts`) opens the thread stream
  (`openChatThreadStream`, WebSocket-first with authenticated SSE fallback,
  never a query-string token) before the POST, so tokens published during
  the handshake still reach the turn. The snapshot classifies the plane:
  `snapshot.standing` present means the standing path, absent means the Job
  plane and the existing POST path stays the turn path.
- **Live tokens are display only.** `token` frames (`threadId`/`turnId`/
  `runName` + `seq` + `done`, peer-child threads filtered out) render as
  deltas and mark first-token timing; the turn settles from the durable
  thread read, exactly like the existing history-recovery path.
- **Every failure falls back.** No WS available, OIDC denied (stub OIDC
  historically blocks signed-in Desktop chat — the denial is recorded, not
  fatal), snapshot/terminal timeouts, or a Job-plane snapshot all return to
  the existing POST path with the same `requestId`. An already-accepted turn
  (`sent: true`) settles by polling the durable thread, never by re-POSTing.
- **Latency samples.** Every send persists one JSONL line to the gitignored
  `.runtime/chat-latency.jsonl` sink (via `ANVIL_CHAT_LATENCY_JSONL` on the
  `anvil-desktop` host process, `POST /local/v1/chat-latency` from the
  renderer) with at least `ts`, `source: "desktop-chat"`,
  `sendToFirstTokenMs` (= `firstTokenMs`), `threadId`, `sessionId`
  (standing session name), `path` (`standing` vs `job`), and `error` when
  the turn did not deliver.

How to run the Desktop e2e / collect samples:

```bash
# 1. Deterministic CI path: fake WS stream, no cluster, no OIDC.
node --experimental-strip-types --test hack/desktop-standing-chat-stream.mjs
node --experimental-strip-types --test hack/desktop-chat-latency.mjs

# 2. Same-origin host + UI (Kind-local API origin for real OIDC).
export ANVIL_CHAT_LATENCY_JSONL=$PWD/.runtime/chat-latency.jsonl
go run ./cmd/anvil-desktop --listen 127.0.0.1:1738 --api-origin http://127.0.0.1:18080
# terminal 2: cd web/desktop && npm run dev  (or --ui-dir web/desktop/dist --open)

# 3. Sign in (Authorization Code + PKCE; stub OIDC denies -> fallback path
#    with error recorded) and Send one chat message; then:
cat .runtime/chat-latency.jsonl
```

Stub OIDC denies the WS upgrade and the POST alike, so signed-in Desktop
chat against the stub still lands on the honest failure copy — with the
denial recorded in the JSONL `error` field. Live send→firstToken samples
need real OIDC (Kind-local issuer or Zitadel) plus a standing-enabled
manager thread (`standing.liveEnabled` / `ANVIL_AGENTS_STANDING_LIVE` on the
API). The fake path above is the CI gate; it never blocks on auth.

### Kind-local signed-in samples (no Zitadel, no stub)

`cmd/kind-oidc-issuer` is a minimal loopback-only test issuer that makes the
Kind-local path runnable without standing up a full IdP: it serves OIDC
discovery + JWKS on loopback and mints short-lived RS256 access tokens
against the same key. It refuses non-loopback listen addresses, caps minted
TTLs at one hour, and never changes API validation (provider selection stays
issuer/audience/client-id only). `hack/desktop-standing-chat-live.mjs` is the
scripted counterpart: it opens the standing WS with the minted bearer, sends
one message, measures send→firstToken, settles from the durable thread, and
appends one `source: "desktop-chat-live-signed-in"` line to
`.runtime/chat-latency.jsonl`. Anything short of a delivered standing turn —
no bearer, a 401 stub denial, a 503 verifier outage, a Job-plane snapshot —
exits 0 with `skip`/`inconclusive` and writes nothing, so the sink never
collects invented numbers. The bearer travels via `ANVIL_AGENTS_ACCESS_TOKEN`
or `--token-file` only: never argv, never query strings, never logs, never
the JSONL file. `examples/live-api/kind-local-api-config.yaml` is the
matching API config (loopback bind, Kind-local issuer, the
`kind-local-desktop` binding with chat + runs:create, loopback CORS, chat and
standing live on).

```bash
# 0. Pure unit gates (no cluster, no OIDC).
go test ./cmd/kind-oidc-issuer/
node --experimental-strip-types --test hack/desktop-standing-chat-live.test.mjs

# 1. Loopback issuer (terminal 1). The key file is 0600 and gitignored;
#    never commit it or any minted token.
go run ./cmd/kind-oidc-issuer --key-file /tmp/kind-oidc.key.json

# 2. API against Kind from the same host (terminal 2; KUBECONFIG -> Kind,
#    chat.enabled needs PostgreSQL, e.g. hack/test-archive-postgres.sh).
ANVIL_AGENTS_STANDING_LIVE=1 ANVIL_AGENTS_CHAT_DATABASE_URL=postgresql://... \
  go run ./cmd/anvil-agents-api --config examples/live-api/kind-local-api-config.yaml

# 3. Mint a bearer (terminal 3) and create one standing-enabled manager
#    thread (needs an InProcess harness profile on the thread's agent).
go run ./cmd/kind-oidc-issuer mint --key-file /tmp/kind-oidc.key.json \
  --issuer http://127.0.0.1:18081 --audience anvil-agents \
  --subject kind-local-desktop --roles kind-local-desktop --namespaces agents
export ANVIL_AGENTS_ACCESS_TOKEN=<minted-token>

# 4. Probe one signed-in standing turn; appends the JSONL sample on delivery.
node --experimental-strip-types hack/desktop-standing-chat-live.mjs --live \
  --api-origin http://127.0.0.1:18080 --namespace agents --thread <thread-id> \
  --out $PWD/.runtime/chat-latency.jsonl

# 5. Read the real sample (source desktop-chat-live-signed-in, not a probe).
cat .runtime/chat-latency.jsonl
```

Expected honest non-sample outcomes while the path is still blocked:
`skip: no bearer`, `skip: stub-oidc-denied` (a stub `anvil-desktop-stub`
session against the real API 401s on the thread read and the POST alike),
`skip: oidc-unavailable` (API verifier down), `inconclusive: job-plane`
(thread snapshot carries no `standing` session — the thread's harness is not
`InProcess` or the API live gate is off). Desktop interactive sign-in keeps
its existing fallback behavior; the probe is the scripted measurement hook,
not a second login flow.

Tests: `hack/desktop-standing-chat-stream.mjs` fakes the WS stream and
asserts first-token timing, the standing/job classification, the
never-send-twice fallback, and the JSONL fields; `internal/desktop/
chat_latency_standing_test.go` pins the new sink fields server-side;
`cmd/kind-oidc-issuer/main_test.go` proves a minted token verifies through
the production `OIDCAuthenticator` and authorizes chat write on `agents`
(plus loopback refusal, TTL cap, and 0600 key handling);
`hack/desktop-standing-chat-live.test.mjs` pins the skip/inconclusive
contract, the 202 append shape, and token redaction;
`internal/runapi/kind_local_example_test.go` keeps the example config
loadable with its gates and binding intact.

## Kind-local chat Postgres for standing chat (blocker 1, scripted)

`chat.enabled=true` requires `ANVIL_AGENTS_CHAT_DATABASE_URL`, and the
Kind-local host-run API has no database to point at — blocker (1) on the path
to a real signed-in Desktop standing-chat latency sample.
`hack/kind-chat-postgres.sh` closes exactly that gap: it starts a disposable
loopback-only `postgres:17-alpine` container (same hardening as
`hack/test-archive-postgres.sh` — UID/GID 70, `cap-drop ALL`,
`no-new-privileges`, tmpfs data, `127.0.0.1` publish), waits for readiness,
and prints the export line the host-run API needs. The API applies the
`anvil_agents_chat` schema itself on startup (`OpenPostgresStore` →
`Migrate`), so no manual SQL is needed.

```bash
# 1. Start disposable Postgres and export the URL it prints.
eval "$(./hack/kind-chat-postgres.sh)"

# 2. Start the host-run API with chat enabled (existing config flags).
ANVIL_AGENTS_STANDING_LIVE=1 go run ./cmd/anvil-agents-api --config <api-config-with-chat.enabled>

# 3. Reuse / inspect / tear down.
./hack/kind-chat-postgres.sh --url    # reprint the export line for a new shell
./hack/kind-chat-postgres.sh --stop   # destroy the container and its data
```

- The emitted URL is always `postgresql://…@127.0.0.1:<port>/…?sslmode=disable`
  with a docker-assigned host port (pass `--port` to pin one). The default
  credential is a dev-only placeholder; override with
  `ANVIL_KIND_CHAT_POSTGRES_PASSWORD` (or `--password`) on any shared host.
- `./hack/kind-chat-postgres.sh --check-url <url>` validates the shape
  offline — no Docker, no network — and fails fast on non-loopback hosts,
  missing userinfo/database, or a missing `sslmode=disable`. The same shape
  is pinned parse-level by `TestKindChatPostgresURLShapeLoadable` (through
  `pgxpool.ParseConfig`, the entry point `OpenPostgresStore` uses) and
  end to end offline by `hack/kind-chat-postgres_test.sh`.
- Scope: host-run Kind-local API only (loopback bind). The container is
  unreachable from inside the Kind cluster; in-cluster installs use the
  chart's `archive` standalone / cloudnativepg modes instead (see
  [PostgreSQL Archive](archive.md)). Disposable only: `--stop` destroys all
  data, and this URL must never leave loopback Kind-local bring-up.
- Still blocked after this slice, in order: real OIDC for signed-in Desktop
  chat (Kind-local issuer or Zitadel), a standing-enabled manager thread
  (`standing.liveEnabled` / `ANVIL_AGENTS_STANDING_LIVE` plus an InProcess
  harness profile), and a harness CLI with local auth on the API host —
  without the last, turns still hold as `InProcessNotWired`. No latency
  numbers are captured here.

## NEXT (after slice 5c + Kind-local signed-in scaffolding)

Standing in-process harness + WebSocket is the **primary** interactive path
(reaffirmed 2026-09-18); Substrate/ATE stays **optional** for
isolation/density and is not promoted (see [Substrate
spike](substrate-spike.md)).

0. **Kind-local signed-in samples (scaffolding landed, live run open).**
   `cmd/kind-oidc-issuer`, `hack/desktop-standing-chat-live.mjs`
   (`source: "desktop-chat-live-signed-in"`), and
   `examples/live-api/kind-local-api-config.yaml` land the full loopback
   runbook above with an honest skip/inconclusive contract — but no live
   sample is captured or committed here (no Kind cluster, harness CLI, or
   Postgres in the agent environment). Next: Austin runs steps 1–5 on WSL
   Kind, then applies the promote/reshape/retire bars in "Slice 5a" above.
   Known blockers on that path, in order: (a) PostgreSQL for
   `chat.enabled` (needs `ANVIL_AGENTS_CHAT_DATABASE_URL`); (b) a
   standing-enabled manager thread (an `InProcess` harness profile bound to
   the thread's agent plus `standing.liveEnabled` on the API); (c) a
   harness CLI with local auth on the API host for `ProcessBackend` turns
   (otherwise turns hold as `InProcessNotWired` and the probe reports a
   delivered-turn failure, never a sample). Stub OIDC still denies by
   design; the probe reports `skip: stub-oidc-denied` there.
1. **Live numbers (the remaining open measurement item).** Desktop e2e is
   done (see "Desktop chat e2e over the standing WebSocket" above), the
   slice-5a harness is green on deterministic backends, slice-5b resume is
   pinned against stub CLIs, and slice 5c wires the cold-first vs
   resumed-second turn scenarios plus the resume-aware stub end to end — but
   `process-exec` live numbers on the same cluster shape as the Job baseline
   are still open (needs a harness CLI with local auth; none exists in the
   agent environment, so no live numbers are captured or committed here).
   Next: collect more `process-exec` live samples against a local/Kind
   harness CLI (cold first turns AND resumed second turns), then apply the
   promote/reshape/retire bars above to the standing plane.
2. **Signed-in Desktop OIDC for chat-latency.jsonl.** Stub OIDC still denies
   signed-in Desktop chat, so live send→firstToken samples need real OIDC
   (Kind-local issuer or Zitadel) plus a standing-enabled manager thread
   (`standing.liveEnabled` / `ANVIL_AGENTS_STANDING_LIVE` on the API).
   Disposable Postgres for `chat.enabled` is scripted
   (`hack/kind-chat-postgres.sh`, see above) and no longer blocks bring-up.
   Next: sign in against real OIDC, Send chat messages over the standing WS
   path, and grow `.runtime/chat-latency.jsonl` with real `standing`-path
   samples.
3. **Retire the envelope wrapper** entirely once no Fake-only live path
   remains. Provider credentials stay outside the API's Secret surface
   throughout.
