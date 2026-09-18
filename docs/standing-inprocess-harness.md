# Standing in-process harness + WebSocket chat delivery

Status: slice 5b (persistent native session resume for ProcessBackend) on top
of slice 5a (standing vs Job latency compare harness), slice 4
(controller-hold yield + multi-replica claim for API-owned standing turns) and
slice 3b (real harness process behind `standing.Backend`) — exactly one API
replica drives a standing turn through an annotation claim the controller
respects, while open standing streams multiplex live token frames during the
turn and the durable turn record stays the source of truth. Architectural
direction locked by Austin 2026-09-18.

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
  from `StreamTurn` start (Desktop `firstTokenMs` vocabulary). Eight scenarios
  (`directEnsureCold`, `directEnsureWarm`, `directTurnWarm`,
  `directFirstToken`, plus the four `peer…` peers), each with
  `count/minMs/meanMs/p50Ms/p95Ms/maxMs`.
- **Backends.** `fake` (default) drives `standing.FakeBackend`: deterministic,
  no cluster, no subprocess, no model call — CI-safe. `process-stub` runs the
  real `ProcessBackend` session/streaming code behind a fixed-latency stub
  runner (still no PATH binary, no credentials). `process-exec` (optional
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

# Optional local live path: real CLI per turn (needs e.g. codex on PATH).
hack/standing-latency-compare.sh --live --harness codex -n 10 --out .runtime/standing-latency-live.json
# Equivalent direct binary run:
# go run ./cmd/standing-latency -backend process-exec -harness codex -n 10 -out .runtime/standing-latency-live.json
```

The report is gitignored local JSON (`.runtime/standing-latency-*.json`,
also printed to stdout) with the eight scenarios, `jobBaseline`,
`comparisonMs` savings of warm turn and first-token against the Job
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

Tests: `cmd/standing-latency/main_test.go` pins the report shape, warmOps
per warm scenario, the documented baseline defaults, the fail-closed backend
selection, and the `harness-ok` ceiling for deterministic backends — all with
no cluster and no subprocess.

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
  stays documented under NEXT. `FakeBackend`, gate-off, Job-plane, and scout
  paths are byte-identical.

Tests: `internal/standing/process_resume_test.go` pins the plumbing with a
stub `SessionRunner` (first turn cold + records, second turn resumes,
rotation, unsupported kinds cold with nothing recorded, plain `Runner`
byte-identical, rejection holds + clears + re-discovers cold) plus the pure
argv/validation/extraction tables and real-`ExecRunner` subprocess turns
against stub shell binaries (codex `resume <uuid>`, openCode `--session`,
openClaw stable `--session-key`, primeAgent with no resume argv anywhere) —
no model calls, no credentials.

## NEXT (slice 5c and beyond)

1. **Live numbers + Desktop e2e.** The slice-5a harness is green on
   deterministic backends and slice-5b resume is pinned against stub CLIs;
   `process-exec` live numbers on the same cluster shape as the Job baseline
   are still open (needs a harness CLI with local auth) — measure cold first
   turns AND resumed second turns, then wire Desktop chat to
   `openChatThreadStream` end to end and apply the promote/reshape/retire
   bars above.
2. **Retire the envelope wrapper** entirely once no Fake-only live path
   remains. Provider credentials stay outside the API's Secret surface
   throughout. Production enablement also needs the API role granted
   `update`/`patch` on `agentruns` (claim stamp) and `update` on
   `agentruns/status` (Succeeded mark) — the slice-4 claim degrades to hold
   behavior without them, so no chart change rode slices 4–5b.
