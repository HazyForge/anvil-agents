# Substrate standing-chat spike

Status: architectural spike, slice 3 (live ATE gRPC dial behind an explicit
opt-in gate; live Kind numbers collected 2026-09-18). Decision locked
2026-09-18 (Austin): keep Substrate/ATE **optional** for isolation/density —
NOT promoted, NOT default-on. Standing in-process harness + WebSocket remains
the primary interactive path; Jobs stay for scouts/batch (see
[Standing in-process harness](standing-inprocess-harness.md)).

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
  chat can opt in while every other profile keeps Job semantics. The opt-in
  live backend dials ATE's real surface (`internal/substrate` ATE `Client` +
  gRPC dialer + controller dispatch behind
  `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`); with the gate off
  the controller keeps holding these runs without creating a Job, and with
  the gate on it dials ateapi and dispatches onto warm actors.

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
  calls `kubectl ate` makes — through a minimal hand-written `ateapipb`
  binding (`internal/substrate/ateapipb`, wire-pinned by golden tests), so
  transport changes stay inside the dialer without touching AgentRun types,
  merge rules, or chat selection.
- The controller never creates a Job for a SubstrateActor run. With the live
  gate off it holds the run as `NeedsHuman/SubstrateActorNotWired` with
  guidance instead, and records `status.executionRuntime`. With the gate on
  (plus an ateapi endpoint) it dials ateapi, binds the warm actor
  (`Create`/`Resume` via `ActorNameForThread`), records
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

## What is in slice 2 (live ATE binding behind a gate)

- Live `substrate.Client` (`internal/substrate/live.go`) speaking ATE's real
  surface: the stable lifecycle (create-or-reuse, resume, suspend, pause,
  describe) mapped onto ateapi `Control` RPCs — the same calls `kubectl ate`
  makes — through a minimal hand-written `ateapipb` binding with no invented
  HTTP mapping:

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
  fake tests (warm reuse with resume-count preservation) plus in-process gRPC
  round-trips over the real wire codec (`TestGRPCControlWireRoundTrip`,
  `TestDialInsecureLoopbackRoundTrip`), with the hand-written stubs pinned to
  upstream-generated bytes by `TestWireGoldenVectors` in
  `internal/substrate/ateapipb` (see `ateapi.proto` there for the refresh
  procedure).
- Explicit opt-in gate, off by default: `ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED`
  plus `ANVIL_AGENTS_SUBSTRATE_ENDPOINT` — the ateapi gRPC target
  (`kubectl ate --endpoint` parallel, e.g. `ate-api-server.ate-system.svc:443`
  in cluster or a localhost port-forward), with
  `ANVIL_AGENTS_SUBSTRATE_TEMPLATE` as the default ActorTemplate, optional
  `ANVIL_AGENTS_SUBSTRATE_ATESPACE`, and
  `ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE` preferred over the inline
  `ANVIL_AGENTS_SUBSTRATE_TOKEN` (controller flags `--substrate-actors-enabled`
  / `--substrate-endpoint` / `--substrate-token-file` / `--substrate-atespace`
  / `--substrate-template` mirror the same env; the inline token stays
  env-only). With no explicit token the dialer mints a short-lived
  `ate-client` ServiceAccount token like `kubectl ate` does. An endpoint
  alone never enables dispatch, and token material never lands in status,
  logs, or API JSON.
- Live gRPC dialer (`internal/substrate/ate_grpc.go` over the hand-written
  `internal/substrate/ateapipb` stubs): verified TLS via the live podcert
  `ClusterTrustBundle` before any bearer token is attached (TLS 1.3,
  `ServerName api.ate-system.svc`), per-RPC bearer auth with file-token
  rotation, round-robin across ateapi replicas, and `NotFound`/`AlreadyExists`
  mapped onto the warm-reuse contract — parity with upstream
  `internal/ateclient`. Kind-only escape hatch:
  `ANVIL_AGENTS_SUBSTRATE_INSECURE=true` (flag `--substrate-insecure`) dials
  TLS with certificate verification skipped but refuses every non-loopback
  endpoint, so it can only reach a local port-forward. ateapi always serves
  TLS, so this is still a TLS channel — never plaintext. Prefer verified TLS;
  use insecure-dev only while the local trust bundle is not yet wired.
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
  with p50/p95 fields, plus the busy-recipient durable-wait peer path
  (`peerBusyWaitWarm`: occupy a warm recipient, wait, resume the same actor),
  against an explicit Job baseline. Fake-backend by default (CI-safe); live
  on Kind with the gate on. See the latency section below.

## What is NOT in this slice

- No Kind e2e against a real Substrate cluster in CI and no chat-log replay
  onto actors. Peer resume against live actors is mapped and wire-tested
  (thread-actor naming plus Resume-then-Suspend over real gRPC); live cluster
  numbers landed 2026-09-18 (see the latency section); the busy-recipient
  durable-wait peer check is confirmed at the unit level
  (`TestPeerSubstrateBusyRecipientDurableWait`, FakeClient, no cluster) and
  the live scenario harness now records `peerBusyWaitWarm` (wait+resume of
  the same warm actor; FakeClient-tested, `--live` on kind-substrate-spike
  collects the Kind numbers). Suspend-on-idle multiplexing is
  stressed at the unit level (many-actor warm resume, rapid
  Create→Suspend→Resume cycles, concurrent peer+direct resume — see the
  latency section).
- If upstream churns the lifecycle surface, update the hand-written
  `internal/substrate/ateapipb` binding per its refresh procedure (field
  numbers are what matter on the wire) — AgentRun types, merge rules, chat
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
# With no explicit token the dialer mints an ate-client ServiceAccount token
# like kubectl ate does (needs that RBAC; mount a token file in-cluster).
```

Or the equivalent flags: `--substrate-actors-enabled --substrate-endpoint
ate-api-server.ate-system.svc:443 --substrate-template standing-chat
[--substrate-token-file /run/ate/token]`. With the gate off (the default),
SubstrateActor runs keep holding as `SubstrateActorNotWired` with no Job.

### Kind port-forward + live env

The spike runs out-of-cluster (laptop/WSL) against the Kind install, so point
`ANVIL_AGENTS_SUBSTRATE_ENDPOINT` at a local port-forward of the ateapi
Service — the same pattern `kubectl ate` uses when `--endpoint` is omitted.
Upstream forwards the `api` Service in `ate-system` (port 443); with a plain
`kubectl` port-forward the local port is yours to pick:

```bash
# Terminal 1: forward ateapi to localhost (keep running).
# Prefer the upstream helper (internal/portforward, Service api in ate-system):
kubectl port-forward -n ate-system svc/api 8443:443
# Any local port works; note the one you picked.

# Terminal 2: prove the lifecycle with kubectl ate first.
kubectl ate --endpoint 127.0.0.1:8443 create actor my-counter-1 \
  -a ate-demo-counter --template counter
kubectl ate --endpoint 127.0.0.1:8443 get actor my-counter-1 -a ate-demo-counter

# Terminal 2 (continued): point Anvil at the same forward and measure.
# Target the counter demo (real ActorTemplate, not a fictional standing-chat
# template): the harness suspends each cold-created actor before the next
# iteration, so cold create then warm resume fits the small counter
# WorkerPool.
export ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED=true
export ANVIL_AGENTS_SUBSTRATE_ENDPOINT=127.0.0.1:8443
export ANVIL_AGENTS_SUBSTRATE_ATESPACE=ate-demo-counter
export ANVIL_AGENTS_SUBSTRATE_TEMPLATE=counter
# Verified TLS (default, preferred): uses your kubeconfig's
# ClusterTrustBundle plus a token file, an inline token, or a minted
# ate-client token, in that order.
# export ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE="$HOME/.config/ate/token"
# Kind-only fallback while the local trust bundle is not yet wired: ateapi is
# always TLS, so ANVIL_AGENTS_SUBSTRATE_INSECURE=true is skip-verify TLS
# (never plaintext), loopback-only (refuses non-local endpoints). Leave it
# unset for verified TLS.
# export ANVIL_AGENTS_SUBSTRATE_INSECURE=true

hack/substrate-latency-compare.sh --live -n 10 \
  --namespace ate-demo-counter --actor-class counter --pool '' \
  --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000 \
  --out /tmp/substrate-latency-live.json
# Equivalent direct binary run:
# go run ./cmd/substrate-latency -n 10 -namespace ate-demo-counter \
#   -actor-class counter -pool '' ...
```

The harness dials `ANVIL_AGENTS_SUBSTRATE_ENDPOINT` with the same TLS/token
rules as the controller (`KUBECONFIG` selects the cluster for trust-bundle
fetch and token minting), runs cold-create vs warm-resume for a direct turn
AND a peer delivery, plus the busy-recipient durable-wait peer path
(`peerBusyWaitWarm`), and writes the JSON report. Probe first —
`hack/substrate-latency-compare.sh --probe [--out /tmp/substrate-probe.json]`
reports `{"reachable":true/false}` with no timings, so an unavailable gateway
is honest instead of blocking or fabricating numbers. Unset
`ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED` to go back to the fake backend; with
the gate off, SubstrateActor runs hold as `SubstrateActorNotWired` with no Job.

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

3. Live compare on the Kind spike cluster (Austin's cluster/WSL Kind) via
   the port-forward setup above — the harness dials ateapi and times real
   Create/Resume calls against the counter demo:

```bash
hack/substrate-latency-compare.sh --live -n 10 \
  --namespace ate-demo-counter --actor-class counter --pool '' \
  --job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000 \
  --out /tmp/substrate-latency-live.json
```

The report records `directCold`, `directWarm`, `peerCold`, `peerWarm`, and
`peerBusyWaitWarm` scenarios with `count/minMs/meanMs/p50Ms/p95Ms/maxMs`
(warm scenarios also report `warmOps`), plus `comparisonMs` savings of warm
resume (and the busy-wait+resume path) against the Job baseline when the
baseline flags are passed. `peerBusyWaitWarm` occupies one recipient actor
with a warm turn, queues a peer delivery that durable-waits, then resumes
the same actor (never drop, no second Create). `-busy-hold` (default 25ms;
`--busy-hold` on the wrapper script) is the occupying hold included in those
samples. Fake-backend numbers only prove the harness and the warm-reuse
contract. The busy-recipient durable wait is confirmed at the unit level
(`TestPeerSubstrateBusyRecipientDurableWait` in `internal/runapi`,
`TestBusyRecipientWaitThenWarmResumeSameActor` in `internal/substrate`, and
`TestMeasurePeerBusyWaitWarmFakeClient` in `cmd/substrate-latency`, all
FakeClient, no cluster). The same harness collects live Kind numbers on
`kind-substrate-spike` via `--live` (optional follow-up; Substrate stays
optional / not promoted).

### Live Kind numbers (collected 2026-09-18, Austin's WSL Kind cluster)

First real live compare against `kind-substrate-spike` (~6:45 PM CT,
`n=10`, backend `ate-live`, namespace `ate-demo-counter`, counter demo via an
INSECURE loopback dial to `127.0.0.1:8443`). The probe reported
`reachable:true` with the gate on before the live run. The raw artifact lives
at `.runtime/substrate-latency-live.json` on the WSL checkout (gitignored —
do not commit the JSON; docs only). Job-baseline flags passed:
`--job-baseline-p50-ms 12000 --job-baseline-p95-ms 44000`.

| Scenario | p50Ms | p95Ms | count/warmOps |
| --- | --- | --- | --- |
| `directCold` | 7.49 | 27.70 | count 10 |
| `directWarm` | 353.04 | 1233.01 | warmOps 10 |
| `peerCold` | 7.47 | 8.62 | count 10 |
| `peerWarm` | 301.54 | 398.70 | warmOps 10 |

`peerBusyWaitWarm` was not in this first 2026-09-18 ~6:45 PM CT run. A later
same-evening pulse on the same cluster (≈10:30–10:45 PM CT, `n=10`,
`busyHoldMs=25`, probe `reachable:true`, artifact
`.runtime/substrate-latency-live-pulse.json`) collected it:

| Scenario | p50Ms | p95Ms | count/warmOps |
| --- | --- | --- | --- |
| `directWarm` (pulse) | ≈306.5 | ≈413.1 | warmOps 10 |
| `peerWarm` (pulse) | ≈292.2 | ≈356.4 | warmOps 10 |
| `peerBusyWaitWarm` (pulse) | ≈664.3 | ≈845.9 | warmOps 10 |

`peerBusyWaitWarm` stays well under the Job baseline (saved ≈11336 ms vs
Job p50 / ≈43154 ms vs Job p95 at the passed 12s/44s flags) while including
the durable busy-hold. Substrate remains optional / not promoted.

`comparisonMs` savings of warm resume vs the passed Job baseline (first run):

| Comparison | Saved ms |
| --- | --- |
| `directWarmSavedVsJobP50` | ≈11647 |
| `peerWarmSavedVsJobP50` | ≈11698 |
| `directWarmSavedVsJobP95` | ≈42767 |
| `peerWarmSavedVsJobP95` | ≈43601 |

Reading note: cold RPC times are much faster than warm resume here. That is
expected if the cold path is create-only vs resume of a suspended actor — the
promote/reshape/retire bars below compare warm resume to the Job cold-start
baseline, not cold vs warm.

Remaining opens, tracked as optional follow-ups (not promote blockers — the
2026-09-18 decision keeps Substrate optional regardless): the busy-recipient
durable-wait peer check has FakeClient coverage plus live Kind numbers on
`kind-substrate-spike` (`peerBusyWaitWarm` p50/p95 ≈664/846ms in the
2026-09-18 ~10:45 PM CT pulse); suspend-on-idle multiplexing is stressed at the unit
level (many actors, rapid Create→Suspend→Resume cycles, concurrent
peer+direct resume warm without identity churn or drops — `TestSuspendIdleMultiplexManyActorsWarmResume`,
`TestSuspendIdleRapidCreateSuspendResumeCycles`,
`TestSuspendIdleConcurrentPeerDirectResumeWarm`,
`TestATEClientSuspendIdleMultiplexStress` in `internal/substrate` plus
`TestSubstrateLiveSuspendIdleMultiplexStress` in `internal/controller`, all
FakeClient / in-memory ATEControl, no cluster); upstream pin is currently
`944abe3`.

## Decision: keep optional (locked 2026-09-18)

Austin skipped the Substrate promote widget: Substrate/ATE stays **optional**
for isolation/density. It is NOT promoted and NOT default-on. The standing
in-process harness + WebSocket is the primary interactive path; Jobs stay for
scouts/batch.

The live Kind numbers above (`n=10`, `kind-substrate-spike`: directWarm p95
1233.01ms, peerWarm p95 398.70ms vs the passed Job p95 baseline of 44000ms)
show warm resume far under the Job cold-start baseline, but the architectural
call is still keep-optional, not promote. The bars below are kept for the
record of how the spike was evaluated.

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
  reuse + `ErrActorNotFound`) plus in-process gRPC round-trips. Live Kind
  numbers landed 2026-09-18 (see the latency section below). The
  busy-recipient durable wait on the warm-actor peer path is confirmed at the
  unit level (peer delivery durable-queues as `waiting` while the recipient
  is busy, then runs and binds the same warm actor with no drop:
  `TestPeerSubstrateBusyRecipientDurableWait`; suspend-then-warm-resume:
  `TestSuspendedActorResumesWarm`; actor-plane wait+resume:
  `TestBusyRecipientWaitThenWarmResumeSameActor`) and the latency harness
  records `peerBusyWaitWarm` on FakeClient and under `--live` (live Kind
  numbers collected 2026-09-18 ~10:45 PM CT on `kind-substrate-spike` —
  Substrate stays optional / not promoted); suspend-on-idle multiplexing is stressed
  at the unit level (many-actor warm resume, rapid Create→Suspend→Resume
  cycles, concurrent peer+direct resume warm without identity churn or drops:
  `TestSuspendIdleMultiplexManyActorsWarmResume`,
  `TestSuspendIdleRapidCreateSuspendResumeCycles`,
  `TestSuspendIdleConcurrentPeerDirectResumeWarm`,
  `TestATEClientSuspendIdleMultiplexStress`, plus gate-on reconcile
  multiplexing: `TestSubstrateLiveSuspendIdleMultiplexStress`).
- [x] Live gRPC dialer (`internal/substrate/ate_grpc.go` over hand-written
  `ateapipb` stubs) with TLS/token parity to upstream `ateclient`, plus a
  Kind-only insecure-dev loopback dial. Gate-on dispatch and live Kind
  numbers are unblocked.
- [x] Actor identity in `status.substrateActor` and turn-to-actor binding.
- [x] Latency harness (Job cold start vs warm actor resume) covering direct
  turns, peer deliveries, and the busy-recipient durable-wait peer path
  (`peerBusyWaitWarm`). Fake-backend
  green; live Kind numbers collected 2026-09-18 on `kind-substrate-spike`
  (`n=10`, `ate-demo-counter`/`counter`, INSECURE loopback dial to
  `127.0.0.1:8443`, probe `reachable:true` with the gate on — see the live
  numbers in the latency section). Raw artifact
  `.runtime/substrate-latency-live.json` stays gitignored on the WSL
  checkout. Before any `--live` run, `hack/substrate-latency-compare.sh
  --probe` (or `go run ./cmd/substrate-latency -probe-only`) dials ateapi
  once and reports `{"reachable":true/false}` JSON with no timings — an
  unreachable gateway surfaces as `reachable:false` instead of fabricated
  numbers.
- [x] Upstream alignment to `944abe3` (RevertActor #1675): the only
  lifecycle-subset drift vs the previous pin was the new
  `ACTOR_STATE_REVERTING = 9`, which folds onto Suspended (Revert targets
  SUSPENDED) while staying distinct on the seam; the five bound RPC
  signatures and all lifecycle field numbers are unchanged, and the spike
  binds no Revert/Delete RPCs.
- [x] Decision (locked 2026-09-18, Austin): keep the `SubstrateActor`
  surface **optional** for isolation/density — not promoted, not default-on.
  Live Kind numbers (`n=10`, directWarm p95 1233.01ms and peerWarm p95
  398.70ms vs Job p95 44000ms — see the latency section) show warm resume far
  under the Job baseline, but the call is keep-optional regardless. Remaining
  opens are optional follow-ups, not promote blockers: busy-recipient
  durable-wait peer check has FakeClient coverage plus live Kind numbers
  (`peerBusyWaitWarm` ≈664/846ms p50/p95 on kind-substrate-spike),
  suspend-on-idle multiplexing is stressed at the unit
  level (many-actor warm resume, rapid Create→Suspend→Resume cycles,
  concurrent peer+direct resume warm without identity churn or drops),
  upstream pin is `944abe3`.
