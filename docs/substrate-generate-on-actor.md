# Generate-on-actor

Status: **CreateActor 0.0.8 wire is live** (Helm 44,
`ghcr.io/hazyforge/anvil-agents@sha256:2eb01ba2ae7d74ad2794bf3508e0407820b26749d9d69229328fa8fc7298ab51`).
`substrate.actorsEnabled` and `substrate.generateOnActor` stay **false**.
Desktop `desktop-standing-assistant` stays on API `ProcessBackend`
(`actorClass: standing-chat`).

OIDC is done. [#272](https://github.com/HazyForge/anvil-agents/pull/272)
sends ATE 0.0.8 `actor_template_namespace`. Bounded smoke
`anvilhub/acp-spike-smoke-003` passed CreateActor auth+namespace, created
Redis atespaces `anvilhub`/`hazy-trade`, then failed
`Internal: while creating pause container: while running runsc create: exit status 128`.
No actor reply. Gates reverted with the overlay values file (no Helm `--set`
drift). Do not flip generate gates until runsc/gVisor on hel1-1 is fixed.

Anvil’s live ATE client is still lifecycle-only for Create/Resume/Suspend/Pause.
Prompt delivery is HTTP/ACP through `atenet-router` **after** `ResumeActor`.
This slice adds that path behind a second gate plus an exact `actorClass`
allowlist so flipping `actorsEnabled` cannot hang Desktop chat.

## What generate-on-actor does

When all of these are true:

1. Chart/flag `substrate.actorsEnabled=true` with an ateapi endpoint
2. `substrate.generateOnActor=true`
3. The run’s `execution.substrate.actorClass` is listed in
   `substrate.generateActorClasses` (exact match; empty list generates for
   nobody)
4. No live standing claim owns the turn

the controller binds the thread actor (`EnsureTurnActor` / Resume), POSTs the
frozen `spec.prompt` as ACP `session/prompt` to atenet, and marks the AgentRun
`Succeeded` (`SubstrateActorGenerated`) or `Failed`
(`SubstrateActorGenerateFailed`). No Kubernetes Job is created. Transient
atenet errors (503/429/504) requeue as `Running/SubstrateActorBound`;
permanent errors fail the run so it cannot sit Bound forever.

The API copies `generateActorClasses` into `standing.generateActorClasses`
only when generate is on, and **does not** ProcessBackend-claim those
classes. `standing-chat` is never generated even if listed, so Desktop
`desktop-standing-assistant` stays on ProcessBackend.

## Atenet request

- Target: `substrate.atenetEndpoint` (default
  `atenet-router.ate-system.svc:80`)
- `Host`: `<actorName>.<atespace>.actors.resources.substrate.ate.dev`
- Body: JSON-RPC 2.0 `session/prompt` with `prompt: [{type: text, text: <frozen prompt>}]`
- Replies accepted: ACP NDJSON `session/update` agent_message_chunk plus
  result; JSON `{"text":...}`; plain text
- Optional bearer from the same projected token file as ateapi. Token bytes
  never appear in errors, logs, status, or console JSON.

## ActorTemplate

Overlay `ActorTemplate/standing-chat` remains the Job grok-build placeholder
and is **not** ACP. Generate uses `ActorTemplate/acp-spike`: a digest-pinned
Python ACP echo on `:80` (atenet ExtProc forwards to `<pod_ip>:80`). Sample
harness: `config/samples/control_v1alpha1_agentharnessprofile_acp_spike.yaml`.

`cmd/anvil-actor-acp` is the same protocol compiled into the controller image
for Kind/local (`anvil-actor-acp --listen :80`).

## Primaris enablement (after this image is live)

Do **not** set `actorsEnabled=true` until:

1. Constrained overlay is applied one-node-safe (`hack/render-ate-constrained.sh`)
2. A smoke AgentRun with `actorClass: acp-spike` bind+generates (or curl
   through atenet to that actor)
3. Overlay pin:

```yaml
substrate:
  actorsEnabled: true
  generateOnActor: true
  generateActorClasses:
    - acp-spike
  endpoint: api.ate-system.svc:443
  template: acp-spike
  ca:
    configMapName: ateapi-ca
    namespace: ate-system
  token:
    projected: true
```

Leave `hazy-trade-agent-manager` on Job/RWO OAuth. Do not list
`standing-chat` in `generateActorClasses`.
