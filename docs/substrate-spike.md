# Substrate standing-chat spike

Status: architectural spike, slice 2 (live ATE binding behind an explicit
opt-in gate; generated-stub dial pending; Kind numbers still open).

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
  chat can opt in while every other profile keeps Job semantics. Slice 2 binds
  the opt-in live backend onto ATE's real surface (`internal/substrate` ATE
  `Client` + controller dispatch behind
  `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`); with the gate off
  the controller keeps holding these runs without creating a Job. The
  generated-stub gRPC dial is still pending (see below), so a gate-on
  operator currently fails fast instead of dispatching anywhere.

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
  (`internal/substrate`): Create/Resume/Suspend/Pause plus describe. The live
  binding maps that interface onto the real ateapi `Control` RPCs — the same
  calls `kubectl ate` makes — without vendoring upstream generated types, so
  the pending generated-stub dialer swaps only the transport without touching
  AgentRun types, merge rules, or chat selection.
- The controller never creates a Job for a SubstrateActor run. With the live
  gate off it holds the run as `NeedsHuman/SubstrateActorNotWired` with
  guidance instead, and records `status.executionRuntime`. With the gate on
  (plus an ateapi endpoint) it will bind the warm actor (`Create`/`Resume`
  via `ActorNameForThread`), record `status.substrateActor`, suspend on idle,
  and still never create a Job — once the pending ATE dialer lands. Until
  then the operator refuses to start with the gate enabled (fail-fast) instead
  of silently holding.
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

## What is in slice 2 (live ATE binding behind a gate)

- Live `substrate.Client` (`internal/substrate/live.go`) speaking ATE's real
  surface: the stable lifecycle (create-or-reuse, resume, suspend, pause,
  describe) mapped onto ateapi `Control` RPCs — the same calls `kubectl ate`
  makes — with no vendored Substrate API types and no invented HTTP mapping:

  | `substrate.Client` | ateapi `Control` RPC | `kubectl ate` equivalent |
  | --- | --- | --- |
  | `CreateActor` | `CreateActor` (after `GetActor`; `AlreadyExists` re-reads) | `create actor <name> -a <atespace> --template <template>` |
  | `ResumeActor` | `ResumeActor` (`resumed` feeds the handle count) | `resume actor <name> -a <atespace>` |
  | `SuspendActor` | `SuspendActor` | `suspend actor <name> -a <atespace>` |
  | `PauseActor` | `PauseActor` | `pause actor <name> -a <atespace>` |
  | `DescribeActor` | `GetActor` | `get actor <name> -a <atespace>` |

  Unknown actors surface as gRPC `NotFound` on every lifecycle RPC and map to
  `ErrActorNotFound`; ATE states fold onto the client tri-state
  (`RUNNING`/`RESUMING` → Active, `SUSPENDED`/`SUSPENDING`/`CRASHED` →
  Suspended, `PAUSED`/`PAUSING` → Paused). Covered by in-memory `ATEControl`
  fake tests, including warm reuse with resume-count preservation. The
  generated-stub gRPC dial is still pending (`TODO(ate-grpc-dial)` in
  `live.go`): `ATEClient` takes an injected `ATEControl` until it lands, so no
  socket speaks to ateapi yet.
- Explicit opt-in gate, off by default: `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`
  plus `ANVIL_AGENTS_SUBSTRATE_ENDPOINT` — now the ateapi gRPC target
  (`kubectl ate --endpoint` parallel, e.g. `ate-api-server.ate-system.svc:443`
  or a localhost port-forward), with `ANVIL_AGENTS_SUBSTRATE_TEMPLATE` as the
  default ActorTemplate, optional `ANVIL_AGENTS_SUBSTRATE_ATESPACE`, and
  `ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE` preferred over the inline
  `ANVIL_AGENTS_SUBSTRATE_TOKEN` (controller flags
  `--substrate-actors-enabled` / `--substrate-endpoint` mirror the same env).
  An endpoint alone never enables dispatch, and token material never lands in
  status, logs, or API JSON. Until the ATE dialer lands, enabling the gate
  fails fast at operator startup instead of silently holding.
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

- No generated-stub gRPC dialer yet (`TODO(ate-grpc-dial)` in
  `internal/substrate/live.go`): the ATE mapping is implemented and
  fake-tested, but no socket speaks to ateapi, so there is no Kind e2e against
  a real Substrate cluster in CI and no chat-log replay onto actors. Peer
  resume against live actors is mapped and tested via fakes (thread-actor
  naming plus Resume-then-Suspend); the remaining gap is the dialer plus live
  cluster numbers.
- If upstream churns the lifecycle surface, re-point
  `internal/substrate/live.go` only — AgentRun types, merge rules, chat
  selection, and the controller dispatch branch do not change.

## Kind-local spike install

Prefer the Kind-local install from Substrate itself
(`hack/install-ate-kind.sh` in agent-substrate/substrate, wrapping
`hack/install-ate.sh` with Kind defaults); treat the upstream README as
authoritative if script names drift. Do not require GKE for the spike, and
do not change Primaris Argo sync policy. The spike needs only:

1. A Kind cluster from the upstream script (Austin's cluster or WSL Kind both
   work; CI does not install Substrate). The script pins a local registry,
   host-arch images, and an explicit `KUBECTL_CONTEXT=kind-<name>` so the
   install lands on the Kind cluster.
2. The Substrate control plane with ateapi reachable from the controller at
   its gRPC target: in-cluster `ate-api-server.ate-system.svc:443`, or a
   localhost port-forward of the ate-api-server Service the way `kubectl ate`
   works when `--endpoint` is omitted. Prove the lifecycle with the counter
   demo first (`demos/counter`: atespace plus `counter` ActorTemplate, then
   `kubectl ate create actor my-counter-1 -a ate-demo-counter --template
   counter`) before pointing a standing-chat harness profile at it.
3. A harness profile selecting the actor plane (sample:
   `config/samples/control_v1alpha1_agentharnessprofile_substrate.yaml`).

Enable the gate on the controller only (no chart values were added, no
Primaris sync changes):

```bash
export ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED=true
export ANVIL_AGENTS_SUBSTRATE_ENDPOINT=ate-api-server.ate-system.svc:443
export ANVIL_AGENTS_SUBSTRATE_TEMPLATE=standing-chat
# Optional: export ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE=/run/ate/token (preferred)
# and/or ANVIL_AGENTS_SUBSTRATE_ATESPACE=... to force one atespace.
# NOTE: the generated-stub dialer is still pending, so the operator currently
# refuses to start with the gate enabled; unset the gate to keep the hold.
```

Or the equivalent flags: `--substrate-actors-enabled --substrate-endpoint
ate-api-server.ate-system.svc:443`. With the gate off (the default),
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

3. Live compare on the Kind spike cluster (Austin's cluster/WSL Kind) —
   blocked on the ATE dialer: the harness fails fast with the gate on until
   the dialer lands, so live numbers come after it:

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
- [x] Live `Client` binding onto real ATE lifecycle behind an explicit opt-in
  gate, including peer resume per the mapping above. Tested via fakes (warm
  reuse + `ErrActorNotFound`); the generated-stub dialer and the warm-actor
  latency check for peer turns specifically (busy-recipient durable wait must
  hold) are still open, so live Kind numbers are too.
- [ ] Generated-stub gRPC dialer (`TODO(ate-grpc-dial)` in
  `internal/substrate/live.go`) with TLS/token parity to upstream `ateclient`,
  unblocking gate-on dispatch and live Kind numbers.
- [x] Actor identity in `status.substrateActor` and turn-to-actor binding.
- [x] Latency harness (Job cold start vs warm actor resume) covering direct
  turns and peer deliveries, not only standing Wrapper chat. Fake-backend
  green; live Kind numbers still open (Austin's cluster/WSL Kind).
- [ ] Decision: promote, reshape, or retire the `SubstrateActor` surface once
  live Kind numbers land.
