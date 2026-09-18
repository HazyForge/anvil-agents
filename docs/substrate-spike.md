# Substrate standing-chat spike

Status: architectural spike, slice 1 (API-first, no live dispatch).

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
  This is the only live plane. Scouts, batch, scheduled, and chained runs
  always use it.
- `SubstrateActor` (optional spike surface): the turn is eligible to run on a
  warm Substrate actor. Select it per `AgentHarnessProfile`
  (`execution.runtime` + `execution.substrate`), so Wrapper/manager standing
  chat can opt in while every other profile keeps Job semantics.

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
- The controller never creates a Job for a well-formed SubstrateActor run. It
  holds the run as `NeedsHuman/SubstrateActorNotWired` with guidance instead,
  and records `status.executionRuntime`. Malformed selections (substrate
  section on a Job runtime, missing section on an actor runtime) fail closed
  as `InvalidSubstrateSpec`.
- The OIDC API, RBAC posture, Secret handling, and Primaris Argo sync policy
  are unchanged. No chart values were added; there is nothing to configure
  until live dispatch exists.

## What is in this slice

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

## What is NOT in this slice

- No live Substrate transport, no actor CRD/controller, no Kind e2e against a
  real Substrate cluster, and no chat-log replay onto actors. Those are the
  follow-up. Peer resume against live actors is specified above but also lands
  with the live backend.

## Spike path (follow-up)

Prefer the Kind-local install docs from Substrate itself
(`hack/create-kind-cluster.sh` / `install-ate-kind` in
agent-substrate/substrate) for the spike cluster. Do not require GKE for the
spike, and do not change Primaris Argo sync policy.

## Latency compare plan (follow-up)

1. Baseline the Job path on Kind and Primaris: per-turn cold-start cost (Job
   create to Pod ready) plus Desktop chat-latency JSONL
   (`waitingMs`/`firstTokenMs`/`runningMs`/`replyReadyMs`, see
   `internal/desktop/chat_latency.go`).
2. Bring up the Kind-local Substrate install and implement a live
   `substrate.Client` behind a feature gate (off by default).
3. Replay the same standing-chat script against a SubstrateActor harness
   profile and compare turn p50/p95, separating warm resume from cold actor
   create, against the Job baseline.

## NEXT

- [ ] Kind-local Substrate install note from the spike path above.
- [ ] Live `Client` implementation behind an explicit opt-in gate, including
  peer resume per the mapping above with a warm-actor latency check for peer
  turns specifically (busy-recipient durable wait must hold).
- [ ] Actor identity in `status.substrateActor` and turn-to-actor binding.
- [ ] Latency compare (Job cold start vs warm actor resume) with numbers,
  covering direct turns and peer deliveries, not only standing Wrapper chat.
- [ ] Decision: promote, reshape, or retire the `SubstrateActor` surface.
