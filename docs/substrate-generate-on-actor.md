# Generate-on-actor (follow-on)

Status: **not in this slice**. Keep Primaris `substrate.actorsEnabled=false`.
Desktop standing grok stays on API `ProcessBackend`. Standing claim still
wins first.

Anvil’s live ATE client is lifecycle-only (`Get` / `Create` / `Resume` /
`Suspend` / `Pause` in `internal/substrate`). There is no ateapi `Execute`
RPC. Upstream prompt delivery is HTTP/ACP through `atenet-router` **after**
`ResumeActor`. Wiring that into AgentRun completion is a second stacked PR
so JWT CA trust and the constrained overlay can land without flipping the
gate.

## Why this is not a small spike

A real “Resume + stream prompt via atenet, mark AgentRun `Succeeded`” path
needs all of:

1. An ACP-capable actor image. Overlay `ActorTemplate/standing-chat` currently
   pins `anvil-agent-run-grok-build` (Job runner). That image does not serve
   ACP on atenet. Using it as a standing actor would Resume a process that
   cannot take a chat turn.
2. An Anvil atenet client (HTTP to `atenet-router.ate-system.svc`, actor
   identity from `status.substrateActor`, stream the turn prompt, fold
   completion/failure onto AgentRun phase). This is new code beside the
   gRPC Control dialer — not a flag.
3. Auth to atenet that is not Secret-in-JSON. JWT mode uses projected SA
   tokens for ateapi; atenet’s auth story must be verified against 0.0.8
   before Primaris enablement.
4. Reconcile rules that do **not** steal standing GET observe-only work
   (PR #264) and do **not** run ahead of `ProcessBackend` while Desktop
   still depends on it.
5. Tests: fake atenet + fake Control that Resume then stream then
   `Succeeded`, plus a failure path that does not leave
   `Running/SubstrateActorBound` forever.

Execute-via-Control is not an option: Anvil must not grow an Execute RPC
the upstream API does not have.

## Unblock order

1. Land JWT CA trust (`DialATEControl` + chart `substrate.ca` /
   `substrate.token`, this slice).
2. Land constrained overlay files (this slice). Operator applies **only**
   after a one-node-safe render **and** step 3.
3. Follow-on PR: ACP actor image (or a documented pause+sidecar spike),
   atenet stream client, reconcile completion, tests. Then — and only then —
   consider Primaris `substrate.actorsEnabled=true` with
   `substrate.endpoint=api.ate-system.svc:443`,
   `substrate.ca.configMapName=ateapi-ca`,
   `substrate.ca.namespace=ate-system`,
   `substrate.token.projected=true`.
4. Do not restart the Primaris API solely to flip that gate. Do not move
   `hazy-trade-agent-manager` off Job/RWO OAuth.

## Kind-only until then

`hack/substrate-latency-compare.sh --live` may use loopback
`--substrate-insecure` against a port-forward. That is skip-verify TLS,
loopback-only, and does not complete AgentRun prompts on Primaris.
