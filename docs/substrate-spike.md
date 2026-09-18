# Substrate standing-chat spike

Status: architectural spike, slice 2 (live dispatch behind an explicit
opt-in gate; Kind numbers still open).

## Why

Desktop standing chat feels slow partly because interactive turns often pay a
cold Job/Pod start (~8–44s measured on Primaris) before harness/model time.
[Substrate](https://github.com/agent-substrate/substrate) multiplexes idle
actors onto warm workers with Create/Resume/Suspend/Pause, so a standing-chat
turn can resume a warm actor instead of waiting for a fresh Pod. Batch
AgentRuns do not have this problem shape — one Job per bounded task is already
the right model — so Jobs stay for scouts and batch.

## Architecture

The execution plane is orthogonal to the harness adapter. `backend.kind`
still selects the adapter (`codex`, `openCode`, ...); `execution.runtime`
selects where it runs:

- `Job` (default, empty means Job): exactly one Kubernetes Job per AgentRun.
  This is the default plane for everything. Scouts, batch, scheduled, and
  chained runs always use it.
- `SubstrateActor` (optional spike surface): the turn is eligible to run on a
  warm Substrate actor. Select it per `AgentHarnessProfile`
  (`execution.runtime` + `execution.substrate`), so Wrapper/manager standing
  chat can opt in while every other profile keeps Job semantics. Slice 2 adds
  the opt-in live backend (`internal/substrate` live `Client` + controller
  dispatch behind `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`); with the gate off
  the controller keeps holding these runs without creating a Job.

Boundaries that do not move in this spike:

- AgentRun stays append-only; new execution intent creates a new AgentRun.
- `create-agent` stays Wrapper/manager-only; peers request via `requestPeer`.
  Snappy peer messaging is not gated on that tool: any Wrapper, manager, or
  peer thread whose harness profile selects `SubstrateActor` is eligible for
  the warm-actor plane, because eligibility is per harness profile, not per role.
- Every accepted message already triggers meaningful work: a direct turn and
  each peer child turn each create a real AgentRun through the same turn path
  (`queueChatTurn` / durable peer dispatch), so the actor plane accelerates
  work rather than replacing it.
- Substrate is early and its APIs will churn, so the Anvil side binds only to
  stable lifecycle concepts behind the `substrate.Client` interface
  (`internal/substrate`): Create/Resume/Suspend/Pause plus describe. A future
  live implementation swaps the transport without touching AgentRun types,
  merge rules, or chat selection.
- The controller never creates a Job for a SubstrateActor run. With the live
  gate off it holds the run as `NeedsHuman/SubstrateActorNotWired` with
  guidance instead, and records `status.executionRuntime`. With the gate on it
  binds the warm actor (`Create`/`Resume` via `ActorNameForThread`), records
  `status.substrateActor`, suspends on idle, and still never creates a Job.
  Malformed selections (substrate section on a Job runtime, missing section on
  an actor runtime) fail closed as `InvalidSubstrateSpec`.
- The OIDC API, RBAC posture, Secret handling, and Primaris Argo sync policy
  are unchanged. No chart values were added; the live plane is configured only
  through the controller gate (`ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED` +
  `ANVIL_AGENTS_SUBSTRATE_ENDPOINT`).

## What was in slice 1 (API-first)

- CRD/profile fields: `execution.runtime` (`Job`/`SubstrateActor`),
  `execution.substrate` (`actorClass`, `pool`, `suspendOnIdle`), and
  `status.executionRuntime` / `status.substrateActor` for the future live
  backend. Regenerated with `make manifests`.
- Thin client interface plus in-memory `FakeClient` with warm-reuse semantics
  (`internal/substrate`), covered by unit tests. No test needs a Substrate
  cluster.
- Merge rules (`agentRunMergeExecution`) and blocking validation with the
  `SubstrateActorNotWired` hold, covered by controller unit tests.
- Chat selection: standing-chat turns already address their harness through
  the thread's profile/harness selection, so choosing a SubstrateActor
  harness profile is sufficient; covered by a `runapi` selection test. The
  default Job path is asserted unchanged.
- Sample: `config/samples/control_v1alpha1_agentharnessprofile_substrate.yaml`.

## Peer messaging maps onto ResumeActor

Peer coordination already creates one durable child thread per recipient
profile with a deterministic ID and queues each delivery through the same turn
construction as a direct message; each recipient runs its own configured
harness. The live backend therefore needs no new peer protocol:

- Thread ID maps to a stable actor name via `substrate.ActorNameForThread`
  (one actor per thread for Wrapper, manager, and peer threads alike).
- A peer message resumes the recipient's thread actor (`ResumeActor`), runs
  the turn as a real AgentRun, then suspends on idle (`SuspendActor`).
- Deterministic delivery request IDs keep retried peer messages idempotent
  end to end; the existing durable wait for busy recipients carries over, so
  a warm actor must never silently drop a queued peer turn.

`requestPeer` siblings created through the public run-create path resolve
their harness the same way (profile/harness selection plus the existing
backend-kind overlay, which stays orthogonal to the runtime plane).

## What is in slice 2 (live dispatch behind a gate)

- Live `substrate.Client` (`internal/substrate/live.go`) speaking only the
  stable lifecycle (create-or-reuse, resume, suspend, pause, describe) over
  HTTP to a gateway origin. No vendored Substrate API types; the versioned
  path prefix lives in one constant so a churning upstream only re-points the
  transport. Covered by `httptest` round-trip tests, including 404 mapping to
  `ErrActorNotFound` and endpoint validation that rejects userinfo, query, and
  fragment (access tokens never travel in query strings).
- Explicit opt-in gate, off by default: `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`
  plus `ANVIL_AGENTS_SUBSTRATE_ENDPOINT` (controller flags
  `--substrate-actors-enabled` / `--substrate-endpoint` mirror the same env).
  An endpoint alone never enables dispatch, and the optional
  `ANVIL_AGENTS_SUBSTRATE_TOKEN` travels only as an `Authorization` header —
  never in status, logs, or API JSON.
- Controller dispatch (`internal/controller/agent_run_substrate_live.go`): with
  the gate on, a well-formed SubstrateActor run binds its thread actor through
  `EnsureTurnActor`, records `status.substrateActor` (turn-to-actor binding),
  and reports `Running/SubstrateActorBound` with warm-vs-cold guidance. The
  branch runs before any Job-launch receipt is written, so no Job is ever
  created for the run; terminal runs suspend on idle (best-effort). Peer child
  runs take the identical path via their recipient child-thread source, so
  peer resume is `ResumeActor` with no new peer protocol. Covered by
  fake-backend reconcile tests asserting Running + binding + zero Jobs for
  direct turns, warm reuse across append-only turns, peer-child binding, and
  suspend-on-idle.
- Peer contract (`TestPeerResumeContract` plus a runapi source-mapping test):
  deterministic child-thread and delivery request IDs converge retries onto
  one warm recipient actor; the durable wait for busy recipients still owns
  queueing, so a warm actor never drops a queued peer turn.
- Latency harness (`cmd/substrate-latency` + `hack/substrate-latency-compare.sh`):
  records cold create vs warm resume for a direct turn AND a peer delivery
  with p50/p95 fields, against an explicit Job baseline. Fake-backend by
  default (CI-safe); live on Kind with the gate on. See the latency section
  below.

## What is NOT in this slice

- No actor CRD/controller, no Kind e2e against a real Substrate cluster in CI,
  and no chat-log replay onto actors. Peer resume against live actors is wired
  and tested via fakes; the remaining gap is live cluster numbers.
- The live gateway path prefix (`/v1/actors/...`) is a spike mapping, not a
  pinned upstream contract: Substrate is early and its APIs will churn. If the
  upstream Kind install serves a different lifecycle surface, re-point
  `internal/substrate/live.go` only — AgentRun types, merge rules, chat
  selection, and the controller dispatch branch do not change.

## Kind-local spike install

Prefer the Kind-local install docs from Substrate itself
(`hack/create-kind-cluster.sh` / `install-ate-kind` in
agent-substrate/substrate); Substrate is early, so treat the upstream README
as authoritative if script names drift. Do not require GKE for the spike, and
do not change Primaris Argo sync policy. The spike needs only:

1. A Kind cluster from the upstream script (Austin's cluster or WSL Kind both
   work; CI does not install Substrate).
2. The Substrate control plane + a lifecycle gateway reachable from the
   controller as one origin URL, for example
   `http://substrate-gateway.substrate:8080`. The Anvil live client only needs
   the stable lifecycle (create-or-reuse, resume, suspend, pause, describe);
   point the gateway paths at `/v1/actors/{namespace}/{name}` with
   `/resume`, `/suspend`, `/pause` actions, or re-point `live.go` to match
   whatever the install serves.
3. A harness profile selecting the actor plane (sample:
   `config/samples/control_v1alpha1_agentharnessprofile_substrate.yaml`).

Enable the gate on the controller only (no chart values were added, no
Primaris sync changes):

```bash
export ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED=true
export ANVIL_AGENTS_SUBSTRATE_ENDPOINT=http://substrate-gateway.substrate:8080
# Optional: export ANVIL_AGENTS_SUBSTRATE_TOKEN=... (header-only, never logged)
```

Or the equivalent flags: `--substrate-actors-enabled --substrate-endpoint
http://substrate-gateway.substrate:8080`. With the gate off (the default),
SubstrateActor runs keep holding as `SubstrateActorNotWired` with no Job.

## Latency compare (direct turns and peer deliveries)

1. Baseline the Job path on Kind and Primaris: per-turn cold-start cost (Job
   create to Pod ready) plus Desktop chat-latency JSONL
   (`waitingMs`/`firstTokenMs`/`runningMs`/`replyReadyMs`, see
   `internal/desktop/chat_latency.go`). Primaris measured ~8–44s of cold
   Job/Pod start dominating interactive turns.
2. Fake-backend sanity (no cluster, CI-safe):

```bash
hack/substrate-latency-compare.sh --iterations 20 --out /tmp/substrate-latency-fake.json
```

3. Live compare on the Kind spike cluster (Austin's cluster/WSL Kind):

```bash
hack/substrate-latency-compare.sh --live --iterations 20 \
  --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000 \
  --out /tmp/substrate-latency-live.json
```

The report records `directCold`, `directWarm`, `peerCold`, and `peerWarm`
scenarios with `count/minMs/meanMs/p50Ms/p95Ms/maxMs` (warm scenarios also
report `warmOps`), plus `comparisonMs` savings of warm resume against the Job
baseline when the baseline flags are passed. Fake-backend numbers only prove
the harness and the warm-reuse contract; the promote/reshape/retire decision
below needs the live Kind numbers, including a peer-delivery warm check with
the busy-recipient durable wait holding.

## Decision: promote, reshape, or retire

Run the live compare above on Kind, then apply these bars. All comparisons
are warm actor resume (direct AND peer) against the Job cold-start baseline on
the same cluster shape:

- Promote: warm resume p95 lands in the low single seconds (order of 10x
  under the Primaris 8–44s Job baseline) for BOTH direct turns and peer
  deliveries, peer retries stay idempotent with the durable busy-recipient
  wait holding, suspend-on-idle multiplexes without resume regressions, and
  the upstream lifecycle surface proves stable enough to pin. Promotion means
  graduating the gate toward default-on for Wrapper/manager standing chat
  while scouts and batch stay on Jobs.
- Reshape: warm resume wins clearly for direct turns but peer deliveries do
  not (for example, recipient actors contend on shared harness locks, or the
  durable wait dominates the saving). Then keep the actor plane for direct
  Wrapper/manager turns and route peer fanout back to Jobs, or reshape the
  peer path (per-recipient pools, warmer standby actors) and re-measure before
  promoting peers.
- Retire: warm resume p50 is within noise of the Job baseline, live numbers
  never materialize because the upstream API churns faster than the spike can
  pin, or operating the gateway (RBAC, pool tuning, suspend/resume failure
  modes) costs more than the seconds it saves. Retiring means removing the
  gate, the live client, and the dispatch branch while keeping the
  `execution.runtime` API surface parked or deleting it per a planned API
  migration.

## NEXT

- [x] Kind-local Substrate install note from the spike path above.
- [x] Live `Client` implementation behind an explicit opt-in gate, including
  peer resume per the mapping above with a warm-actor latency check for peer
  turns specifically (busy-recipient durable wait must hold). Tested via fakes
  and the `httptest` gateway; live Kind numbers still open.
- [x] Actor identity in `status.substrateActor` and turn-to-actor binding.
- [x] Latency harness (Job cold start vs warm actor resume) covering direct
  turns and peer deliveries, not only standing Wrapper chat. Fake-backend
  green; live Kind numbers still open (Austin's cluster/WSL Kind).
- [ ] Decision: promote, reshape, or retire the `SubstrateActor` surface once
  live Kind numbers land.
