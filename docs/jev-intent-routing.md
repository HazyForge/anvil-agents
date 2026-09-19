# Jev intent routing (live hook behind an opt-in gate)

Status: live — the spike client + fixed intent router landed first; the
chat-turn hook is now wired in `queueChatTurnAttempt`
(`internal/runapi/chat_execution.go`, via `internal/runapi/chat_jev_intent.go`)
behind an opt-in, deny-by-default gate. Standing in-process chat plus
WebSocket delivery remains the primary interactive path; Jobs stay the
default for scouts/batch; Substrate stays optional. Direction set by
Austin 2026-09-18: use Jev when something needs decision making at runtime
(e.g. decide user intent and route based on intent) — NOT for chat
generation.

## What Jev is

Jev is TypeSafe's System One model: one `POST
https://api.typesafe.ai/v1/systemone` call carries application state plus
typed questions (`noul` / `choice` / `score`) and returns calibrated
probabilities/confidence in ~70–500ms. It never generates strings. The live
source of truth for the API shape is the TypeSafe docs
(`https://docs.typesafe.ai/api.md`, plus the `typesafe-ai` agent skill at
`https://github.com/typesafe-ai/skills`) — this spike was built against
those pages, and field names in `internal/jev` mirror them exactly. No API
fields are invented here, and no live API responses are fabricated: tests
drive fakes with no network, and live calls only happen with
`TYPESAFE_API_KEY` set.

## When to use Jev vs the harness chat model

| Need | Use | Why |
| --- | --- | --- |
| Decide user intent and route (chat_reply, create_agent_request, peer_handoff, tool_run, unclear) | Jev (`internal/jev.Router`) | Millisecond typed decision with a confidence gate; cheap enough to run per message |
| Verify a claim, rank candidates, score one dimension | Jev (`noul`/`choice`/`score` directly) | Same calibrated-decision shape; code owns thresholds |
| Generate the reply text | Harness chat model (Codex, OpenCode, …) behind the standing session | Jev cannot write; standing chat still generates exactly as today |
| Long-horizon reasoning, tool orchestration | Harness / agent execution | System Two work stays out of Jev |

Standing chat still generates with Codex/etc. through the existing standing
turn path (`reconcileStandingTurn` in
`internal/runapi/chat_standing_turn.go`). Jev only decides.

## What landed in this slice

- `internal/jev/` — small stdlib-only client: `Request`/`Question`
  (`NoulQuestion`/`ChoiceQuestion`/`ScoreQuestion` with validation),
  typed `Response` accessors (`Noul`/`Choice`/`Score`), `HTTPClient` with
  `TYPESAFE_API_KEY`-from-env auth and 429/529 backoff, plus
  `FakeBackend`/`FuncBackend` fakes. The key is never hardcoded, logged,
  or persisted.
- `internal/jev/intent.go` — `Router.ClassifyIntent`: a `Choice` over the
  fixed intent set with a confidence gate (`DefaultConfidenceThreshold`
  0.5, mirroring the docs' example review floor). Below the floor — or on
  an unknown choice — the decision gates to `unclear` (human/clarification
  path) while keeping the raw model choice visible for observability.
  Fulfillment of `create_agent_request` and `peer_handoff` applies a
  higher named bar (`DestructiveActionConfidenceBar` 0.8) at the
  fulfillment site, not in the router. `tool_run` and `chat_reply` stay
  on the classify floor. Labeled-fixture coverage (FakeBackend, no
  network) pins high-confidence keep-class, near-floor gate to unclear,
  and ambiguous/truncated to unclear.
- `cmd/jev-probe` — one-message classifier printing the routing decision
  as JSON. Live with `TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message
  "hello"`; fake (no network) with `-fake` or no key set.
- Unit tests (`internal/jev/*_test.go`) — question/request validation,
  HTTP auth/shape/retry behavior against `httptest`, FakeBackend-style
  routing deciding each intent plus the confidence gate, and the labeled
  confidence-floor fixture suite (`TestIntentConfidenceFloorLabeledFixtures`).
- Live hook (`internal/runapi/chat_jev_intent.go`, covered by
  `internal/runapi/chat_jev_intent_test.go` with `FakeBackend`/error
  fakes, no network): `queueChatTurnAttempt` classifies the incoming
  message plus a small thread tail before `buildChatPrompt` freezes the
  execution intent, folds a per-intent `ROUTING_HINT` into the prompt, and
  records intent + confidence + serving model on the queued user message
  metadata and the turn's AgentRun annotations. Details below.

## Rules this slice holds

- Jev never replaces chat generation; there is no generation path here.
- Substrate is untouched and out of scope.
- `create-agent` stays Wrapper/manager-only. The `create_agent_request`
  intent is classification plus a request-only fulfillment hint: the turn
  names the existing `requestPeer` STATUS_JSON shape and carries a
  structured `jevNeedsManagerCreate` request flag for a Wrapper/manager to
  fulfill later (the existing manager-authorization path still owns that
  decision) — peers never create agents. Destructive fulfillment applies
  `jev.DestructiveActionConfidenceBar` (0.8) in code at the fulfillment
  site (`jevNeedsManagerCreate` / `jevNeedsPeerHandoff` / STATUS_JSON
  hints), not in the router. Below the bar the classified intent stays
  for observability; needs-* flags and STATUS_JSON are withheld.
- `peer_handoff` stays coordination-only. The hint names the existing-peer
  `requestPeer` STATUS_JSON shape (plus the chat coordination JSON
  equivalent) and the turn carries a structured `jevNeedsPeerHandoff`
  request flag — never creation, never a new fanout path, never sends
  outside existing paths.
- `tool_run` stays prompting-only. The hint names the existing tool
  surface (the turn's resolved `AgentToolSet` composition plus the harness
  tool step — no new tool API) and the turn carries a structured
  `jevNeedsToolRun` prompting flag — never invented results, never tools
  outside the configured sets.
- No invented live API responses: fakes in CI, live only behind the env key.

## The live hook (wired)

`queueChatTurnAttempt` in `internal/runapi/chat_execution.go` calls
`Server.classifyChatIntent` before `buildChatPromptWithIntent` freezes the
execution intent: classify the incoming message (+ up to 6 prior thread
messages for disambiguation), fold a `ROUTING_HINT` into the prompt, and
record the decision alongside the turn for observability. Classification
observes the message — it never rewrites the durable turn. The standing
execution path itself (`reconcileStandingTurn`) does not change: one
append-only AgentRun per accepted message, frozen intent in, `Succeeded`
completion out, peer fanout through `dispatchChatCoordination` unchanged
(child `queueChatTurnInternal` deliveries classify on their own path the
same way).

### Gate (opt-in, deny by default)

- Config: `chat.jevIntentEnabled` (default `false`; chart value
  `api.config.chat.jevIntentEnabled`, rendered verbatim into the API
  ConfigMap — no RBAC change, no Secret access).
- Env: `ANVIL_AGENTS_JEV_INTENT=1` enables the config flag but never
  disables it (same pattern as `ANVIL_AGENTS_STANDING_LIVE`).
- Model pin (optional, deny-by-default-safe): `chat.jevModel` (default
  `""`; chart value `api.config.chat.jevModel`, rendered verbatim into
  the API ConfigMap). Empty tracks the `jev-latest` alias
  (`jev.DefaultModel`) — today's behavior unchanged. Set a versioned ID
  to pin classification to that serving model; `jev-1.13.0` is the
  validated serving model from the live Kind-local e2e below. Env
  `ANVIL_AGENTS_JEV_MODEL` overrides the config-file value when
  non-empty; empty leaves the config value untouched.
- Backend: the API attaches the live System One client only when the gate
  is on AND `TYPESAFE_API_KEY` is set (`jev.ClientFromEnv` in
  `cmd/anvil-agents-api/main.go`, env only — the API gains no Secret
  access). Tests attach fakes via `Server.SetJevBackend`.
- Fallback: gate off, no backend (missing key), or any Jev error (timeout
  after 10s, transport/validation failure) keeps today's behavior —
  `chat_reply`, unchanged prompt, no intent metadata, never a hard fail.
  Gate-off turns are prompt- and metadata-byte-identical to before (pinned
  by `TestChatJevPromptByteIdenticalWhenUnclassified`).

### What each intent drives

| Intent | Prompt | Ownership |
| --- | --- | --- |
| `chat_reply` | unchanged prompt path | — |
| `create_agent_request` | at/above `DestructiveActionConfidenceBar` (0.8): hint names the concrete peer-safe request shape (`requestPeer` STATUS_JSON with `request=create-agent`, mirroring `skills/create-agent/SKILL.md` and the controller's injected skill content) and the turn carries a structured `jevNeedsManagerCreate` request flag; still Wrapper/manager-only. Between the 0.5 classify floor and the bar: intent kept for observability, no flag, no STATUS_JSON, ask to clarify. | Request-only — peers never create agents (controller strips `create-agent` from peers; Desktop `isCreateAgentPrincipal` refuses) |
| `peer_handoff` | at/above the destructive bar: hint names the concrete existing-peer `requestPeer` STATUS_JSON shape (`action=requestPeer` with `peerProfileName`/`summary`, mirroring `web/desktop/src/wrapper/requestPeer.ts` and the controller's `requestPeer` decision parsing) and the turn carries a structured `jevNeedsPeerHandoff` request flag; still coordination-only, never creation. Between floor and bar: intent kept, no flag, no STATUS_JSON, ask to clarify. | Request-only — no new fanout; without enabled coordination, answer directly |
| `tool_run` | tool-first hint naming the existing tool surface (the turn's resolved `AgentToolSet` composition — profile/run `toolSets` refs, `status.resolvedComposition.toolSetRefs` — run through the harness tool step before finalizing; never invent results, never tools outside the configured sets) and the turn carries a structured `jevNeedsToolRun` prompting flag. Stays on the 0.5 classify floor; the destructive bar does not apply. | Prompting-only — no new tool API; without a covering configured tool, name the needed lookup instead of guessing |
| `unclear` (choice, unknown output, or sub-threshold confidence) | clarification-first hint: ask a brief clarifying question; no destructive/delegating/creating acts | — |

### `create_agent_request` fulfillment slice (this change)

No new tool or API was invented: the peer-safe surface already exists on
both sides, and this slice wires the classified intent to it.

- Existing surfaces found: the controller injects the baked-in
  `create-agent` skill into Wrapper/manager profiles and strips it from
  everyone else (`internal/controller/create_agent.go`,
  `profileMayCreateAgent`); Desktop Wrapper fulfills creation through the
  composition API behind `isCreateAgentPrincipal`
  (`web/desktop/src/wrapper/createAgent.ts`,
  `createAgentPolicy.ts`) and refuses peer calls with a visible receipt;
  peers request through the existing `requestPeer` STATUS_JSON mesh
  (`skills/create-agent/SKILL.md`,
  `web/desktop/src/wrapper/requestPeer.ts`).
- What a classified, non-`unclear` `create_agent_request` at or above
  `DestructiveActionConfidenceBar` (0.8) now carries: the `ROUTING_HINT`
  names the exact peer-safe line the harness should emit (same shape the
  controller skill and the Desktop parser accept):
  `ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","request":"create-agent","name":"<dns-label>","description":"<why>","peerProfileName":"desktop-manager"}`
  (fill `name`/`description` from the request). The queued user message
  carries `jevNeedsManagerCreate: true` and the turn's AgentRun carries
  `control.anvil.hazyforge.io/jev-needs-manager-create=true` — the
  structured outbox hint a Wrapper/manager harness can consume.
  Between `DefaultConfidenceThreshold` (0.5) and the bar: `jevIntent`
  stays `create_agent_request` (plus `jevRawChoice` / `jevConfidence` /
  run annotations) for observability; no needs-* flag, no STATUS_JSON
  in the hint — ask to clarify.
- What fulfillment looks like live: the standing assistant replies by
  emitting that `requestPeer` STATUS_JSON line through the existing runner
  output path (or, when it cannot emit STATUS_JSON, by replying that
  creating an agent requires a manager and asking what the new agent
  should do). A Wrapper/manager harness fulfills it with the existing
  `executeCreateAgent` composition POST; Desktop shows the existing
  `Requested create: <name>` receipt. Peers still cannot create: the hint
  says `Do NOT create...`, the controller strips the skill from peers,
  and Desktop refuses non-Wrapper/manager principals.
- `unclear` (including a sub-threshold `create_agent_request` choice
  below the 0.5 classify floor) carries no request shape and no flag —
  clarification only. A classified create below the destructive bar is
  not `unclear`: observability keeps the class, fulfillment is withheld.
- Unit cover (`internal/runapi/chat_jev_intent_test.go`, `FakeBackend`,
  no network): `TestChatJevCreateAgentFulfillmentSlice` (hint shape +
  message flag + run annotation), `TestChatJevUnclearCreateCarriesNoFulfillment`,
  `TestChatJevNonCreateIntentsCarryNoManagerRequest`, the
  strengthened `TestChatJevCreateAgentHintNeverAuthorizesPeerCreation`,
  plus `TestJevDestructiveActionBarAtFulfillmentSite` and
  `TestChatJevConfidenceFloorAndDestructiveBarLabeledFixtures`.
- Remaining Desktop Wrapper work (not in this slice): watch
  `jevNeedsManagerCreate` on thread detail / the
  `jev-needs-manager-create` run annotation and surface a
  manager-facing affordance (e.g. prefill the existing `CreateAgentPanel`
  or raise a `Requested create` receipt) when the signed-in principal is
  Wrapper/manager; keep the read-only `jevIntent` caption as-is for
  peers. No peer create path. (Shipped next section; `create-agent` itself
  is unchanged by the handoff slice below.)

### `peer_handoff` fulfillment slice (this change)

Same pattern as `create_agent_request`, one intent over: no new tool, API,
or protocol was invented — the coordination surface already exists on both
sides, and this slice wires the classified intent to it.

- Existing surfaces found: in chat, the coordination JSON this thread's
  config allows (`dispatchChatCoordination` in
  `internal/runapi/chat_coordination.go`: `messages` to allowed profiles,
  durable child-thread delivery); on the runner path, the base
  `requestPeer` STATUS_JSON decision the controller already parses
  (`action=requestPeer` plus `peerProfileName`, see
  `internal/controller/agent_run_controller.go`) and Desktop already emits
  and parses (`grokRequestPeerProofPrompt` /
  `parseRequestPeerFromLogLine` in
  `web/desktop/src/wrapper/requestPeer.ts`). No `request=create-agent`
  extension, no new fields.
- What a classified, non-`unclear` `peer_handoff` at or above
  `DestructiveActionConfidenceBar` (0.8) now carries: the `ROUTING_HINT`
  names the exact existing-peer line the harness should emit on the
  runner path (same shape the controller parser and the Desktop parser
  accept):
  `ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","peerProfileName":"<existing-profile>","summary":"<why>"}`
  (fill `peerProfileName` with the existing target profile and `summary`
  from the request; in chat, use the coordination JSON equivalent to an
  allowed profile). The queued user message carries
  `jevNeedsPeerHandoff: true` and the turn's AgentRun carries
  `control.anvil.hazyforge.io/jev-needs-peer-handoff=true` — the
  structured outbox hint a harness can consume. Between the 0.5 classify
  floor and the bar: `jevIntent` stays `peer_handoff` for observability;
  no needs-* flag, no STATUS_JSON in the hint — ask to clarify.
- Peers vs managers: both fulfill through the same existing coordination
  contract — a handoff never creates an agent and never sends outside
  existing paths. The hint says `Never create, spawn, or provision an
  agent for a handoff`; `create-agent` stays Wrapper/manager-only
  (controller strips the skill from peers; Desktop
  `isCreateAgentPrincipal` refuses). `unclear` (including a
  sub-threshold `peer_handoff` choice below the 0.5 classify floor)
  carries no request shape and no flag — clarification only. A
  classified handoff below the destructive bar is not `unclear`:
  observability keeps the class, fulfillment is withheld. Without
  enabled coordination, the harness answers directly or explains the
  handoff needs an enabled coordination target.
- Unit cover (`internal/runapi/chat_jev_intent_test.go`, `FakeBackend`,
  no network): `TestChatJevPeerHandoffFulfillmentSlice` (hint shape +
  message flag + run annotation, and no create path), 
  `TestChatJevUnclearHandoffCarriesNoFulfillment`,  `TestChatJevNonHandoffIntentsCarryNoHandoffFlag`, plus the updated
  `TestChatJevNonCreateIntentsCarryNoManagerRequest` (pins the create
  shape absent on every other intent, handoff included) and the
  destructive-bar labeled fixtures (below-bar handoff keeps class, no
  flag, no STATUS_JSON).
- Desktop: shipped — `EntityChatPage` surfaces a Wrapper/manager-facing
  handoff receipt on `jevNeedsPeerHandoff` user messages (plus equivalent
  existing-peer `requestPeer` STATUS_JSON hints anywhere in the thread).
  The receipt names the target when one is present and deep-links that
  peer's standing conversation via the existing `openAgent` path, else it
  opens the thread's coordination controls. Peers keep the read-only
  `jevIntent` caption only. Handoff never creates; the STATUS_JSON shape
  stays pinned in the Go tests above plus `hack/desktop-jev-peer-handoff.mjs`.
  See "Desktop Wrapper handoff affordance (shipped)" below.

### `tool_run` fulfillment slice (this change)

Same pattern as `create_agent_request` and `peer_handoff`, one intent
over: no new tool, API, or protocol was invented — the tool surface
already exists on both sides, and this slice wires the classified intent
to it.

- Existing surfaces found: the `AgentToolSet` composition layer
  (`api/v1alpha1/agent_tool_set_types.go`: stable tool entries such as
  `kbctl`, selected via profile/run `toolSets` refs plus namespace-global
  sets, materialized into compatible harnesses by the controller and
  recorded per turn as `status.resolvedComposition.toolSetRefs`);
  `runs.create` already accepts `toolSetNames`
  (`internal/runapi/runs_create.go`); the standing chat prompt already
  holds the no-invention boundary (`buildChatPrompt` in
  `internal/runapi/chat_execution.go`: do not claim delivery "unless a
  real tool confirms"). No new STATUS_JSON shape, no new tool-call
  format.
- What a classified, non-`unclear` `tool_run` turn now carries: the
  `ROUTING_HINT` names that existing surface — run the matching
  configured tool from this turn's resolved `AgentToolSet` composition
  through the harness tool step before finalizing; never invent the
  result, never claim a lookup succeeded unless a real tool confirms it,
  never reach for tools outside the configured sets; with no covering
  configured tool, name the needed lookup instead of guessing. The queued
  user message carries `jevNeedsToolRun: true` and the turn's AgentRun
  carries `control.anvil.hazyforge.io/jev-needs-tool-run=true` — the
  structured prompting hint a harness can consume. `tool_run` stays on
  `DefaultConfidenceThreshold` (0.5); the destructive-action bar does
  not apply, so a 0.6 tool classification still arms the prompting flag.
- `unclear` (including a sub-threshold `tool_run` choice) carries no
  tool-first hint and no flag — clarification only. The turn carries
  neither the create nor the handoff request shape.
- Unit cover (`internal/runapi/chat_jev_intent_test.go`, `FakeBackend`,
  no network): `TestChatJevToolRunFulfillmentSlice` (hint shape +
  message flag + run annotation, and no create/handoff shape),
  `TestChatJevUnclearToolRunCarriesNoFulfillment`,
  `TestChatJevNonToolRunIntentsCarryNoToolRunFlag`.
- Desktop in this slice: docs-only. There is no existing tool-run UI to
  extend (`EntityChatPage` has only the read-only `jevIntent` caption —
  which already surfaces `tool_run` — plus the manager-create receipt;
  `tool`-role messages already render under the `Coordination` header),
  so no Desktop code changes here — the prompting shape is pinned in the
  Go tests above. Remaining Desktop work: if a tool affordance is ever
  wanted, watch `jevNeedsToolRun` on thread detail / the
  `jev-needs-tool-run` run annotation and surface existing tool UI (e.g.
  deep-link the covering configured tool set or its last output); every
  principal sees the same read-only `jevIntent` caption until then. Jev
  never generates; Substrate untouched; standing primary.

### Desktop Wrapper affordance (shipped)

`EntityChatPage` consumes the request flag from thread detail:

- Detection: `messageNeedsManagerCreate` in
  `web/desktop/src/wrapper/jevManagerCreate.ts` reads user-message
  metadata `jevNeedsManagerCreate === true` (strict boolean); the same
  module covers the run annotation
  `control.anvil.hazyforge.io/jev-needs-manager-create=true` via
  `runNeedsManagerCreate` for kubectl-found turns.
- Manager path: when `mayShowCreateAffordance` (flag AND
  `isCreateAgentPrincipal`) holds, the user bubble shows a
  `Requested create — needs a manager to fulfill` receipt with a
  `Review in create form` button. The button prefills the existing
  `CreateAgentPanel` (`suggestCreateAgentInput` parses the message text
  with the existing `parseCreateAgentIntent` — no invented names; vague
  requests leave the form blank) and scrolls to it. Fulfillment still
  runs through the existing `executeCreateAgent` composition POST behind
  the panel's Wrapper/manager + composition-write gates.
- Peer path: flag or not, non-Wrapper/manager principals see the
  read-only `jevIntent` caption only — never a create button.
- Cover: `hack/desktop-jev-manager-create.mjs` (wired into
  `make desktop-chat-tests`): strict flag/annotation matching, latest
  flagged message, Wrapper/manager-only gating, parse-without-invention.

### Desktop Wrapper handoff affordance (shipped)

`EntityChatPage` consumes the handoff signal from thread detail,
mirroring the manager-create pattern one intent over:

- Detection: `messageNeedsPeerHandoff` in
  `web/desktop/src/wrapper/jevPeerHandoff.ts` reads user-message
  metadata `jevNeedsPeerHandoff === true` (strict boolean); the same
  module covers the run annotation
  `control.anvil.hazyforge.io/jev-needs-peer-handoff=true` via
  `runNeedsPeerHandoff` for kubectl-found turns. Equivalent harness
  hints are covered too: `handoffStatusTargets` parses existing-peer
  `requestPeer` STATUS_JSON (`action=requestPeer` with `peerProfileName`/
  `summary`) from message content, excluding `request=create-agent`
  variants (those stay on the manager-create affordance).
- Manager path: when `mayShowHandoffAffordance` (flag-or-hint AND
  `isCreateAgentPrincipal`) holds, the bubble shows a
  `Handoff requested — coordinate with an existing peer` receipt with an
  `Open <peer> conversation` button. The button deep-links the named
  peer's standing conversation through the existing `openAgent` path
  (STATUS_JSON target first, else the roster-matched name from
  `suggestPeerHandoffTarget` — never invented; no match opens the
  thread's coordination controls). Fulfillment stays on existing
  coordination: standing chat + WebSocket primary, no new fanout.
- Peer path: flag or hint, non-Wrapper/manager principals see the
  read-only `jevIntent` caption only — never a handoff button, and the
  handoff path never creates teammates (`create-agent` stays
  Wrapper/manager-only via `isCreateAgentPrincipal` + `executeCreateAgent`).
- Cover: `hack/desktop-jev-peer-handoff.mjs` (wired into
  `make desktop-chat-tests`): strict flag/annotation matching, latest
  flagged message, STATUS_JSON hint detection excluding create-agent,
  Wrapper/manager-only gating, suggest-without-invention.

### Observability (threshold tuning)

- Queued user message metadata: `jevIntent`, `jevRawChoice`,
  `jevConfidence`, `jevModel` (serving model), `jevUnclear` — alongside the
  existing `authorKind` keys, visible in thread detail. Classified,
  non-unclear `create_agent_request` turns at or above
  `DestructiveActionConfidenceBar` additionally carry
  `jevNeedsManagerCreate: true`: the structured outbox hint a
  Wrapper/manager harness can act on later. It grants no authority.
  Classified, non-unclear `peer_handoff` turns at or above that bar
  additionally carry `jevNeedsPeerHandoff: true`, and classified,
  non-unclear `tool_run` turns (classify floor only) additionally carry
  `jevNeedsToolRun: true` — the same request/prompting-only shape, one
  flag per intent. Below the destructive bar, create/handoff keep
  `jevIntent` for observability with no needs-* flag.
- Turn AgentRun annotations: `control.anvil.hazyforge.io/jev-intent`,
  `.../jev-confidence`, `.../jev-model` — `kubectl`-visible per turn.
  Classified, non-unclear `create_agent_request` turns at or above the
  destructive bar additionally carry
  `control.anvil.hazyforge.io/jev-needs-manager-create=true` so a
  Wrapper/manager can find actionable turns with `kubectl`; it is a request
  flag, never a create grant. Classified, non-unclear `peer_handoff`
  turns at or above the bar additionally carry
  `.../jev-needs-peer-handoff=true`, and classified, non-unclear
  `tool_run` turns additionally carry `.../jev-needs-tool-run=true` —
  same flag-only shape. `unclear` turns (including sub-threshold
  `create_agent_request`, `peer_handoff`, and `tool_run` choices below
  the 0.5 classify floor) carry none of the three flags; classified
  create/handoff below the destructive bar also carry none.
- The prompt hint itself carries `(Jev intent X, confidence N, model M)`.

### What still needs a live `TYPESAFE_API_KEY` probe

1. Live accuracy/latency numbers per intent against real Anvil traffic
   (`TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message "..."`); CI only
   drives fakes, so no live responses are pinned anywhere.
2. Retune `DefaultConfidenceThreshold` (0.5) and
   `DestructiveActionConfidenceBar` (0.8, applied at
   `jevNeedsManagerCreate` / `jevNeedsPeerHandoff` / STATUS_JSON hints;
   `tool_run` and `chat_reply` stay on the classify floor) from live
   labeled traffic. The FakeBackend labeled-fixture suite already pins
   the starting floors (high-confidence keep-class, near-floor and
   truncated/ambiguous to unclear, below-bar create/handoff classified
   without fulfillment). Then pin the validated serving model ID via
   `chat.jevModel` (chart `api.config.chat.jevModel`, or
   `ANVIL_AGENTS_JEV_MODEL`) instead of tracking `jev-latest`:

```yaml
chat:
  enabled: true
  jevIntentEnabled: true
  jevModel: jev-1.13.0 # validated serving model (live Kind-local e2e, 2026-09-18)
```

```bash
ANVIL_AGENTS_JEV_INTENT=1 ANVIL_AGENTS_JEV_MODEL=jev-1.13.0 \
  go run ./cmd/anvil-agents-api --config <api-config-with-chat.enabled>
```
3. Confirm per-turn p70–p130 overhead stays inside the 70–500ms System One
   envelope on the append path (the 10s cap is a wedge guard, not a
   budget).

### Kind-local runbook (validated model pin, gate via env only)

Kind-local measurement setup that reproduces the live e2e below without
flipping production defaults (`chat.jevIntentEnabled: false`,
`chat.jevModel: ""` stay the shipped defaults in chart values).

- Example config: `examples/live-api/kind-local-api-config.yaml` pins
  `chat.jevModel: "jev-1.13.0"` (the validated serving model) and leaves
  `chat.jevIntentEnabled` off. The gate is enabled per-process only.
- Exact env pair for the measured run (plus the standing gate):

```bash
# 1. API key from the typesafe-jev checkout pattern (never commit the key).
set -a; source ~/CodingFiles/PROJECTS/typesafe-jev/.env; set +a # provides TYPESAFE_API_KEY

# 2. API on alt ports when 18080 is Zitadel: bind 127.0.0.1:18180, issuer on
#    127.0.0.1:18181 (mirror examples/live-api/kind-local-api-config.yaml
#    with bindAddress/issuer port-swapped, or port-forward accordingly).
#    KUBECONFIG must point at Kind; chat.enabled needs PostgreSQL, e.g.
#    eval "$(./hack/kind-chat-postgres.sh)".
ANVIL_AGENTS_STANDING_LIVE=1 \
ANVIL_AGENTS_JEV_INTENT=1 \
ANVIL_AGENTS_JEV_MODEL=jev-1.13.0 \
  go run ./cmd/anvil-agents-api --config examples/live-api/kind-local-api-config.yaml
```

- Quick classifier check (no cluster) before the turn path:

```bash
TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message "create an agent named Scout for research" -model jev-1.13.0
go run ./cmd/jev-probe -fake -message "create an agent named Scout for research"
```

- Verify on a live turn: thread detail carries `jevIntent`,
  `jevRawChoice`, `jevConfidence`, `jevModel`, `jevUnclear` on the queued
  user message metadata (plus `jevNeedsManagerCreate: true` on classified,
  non-unclear `create_agent_request` at or above
  `DestructiveActionConfidenceBar`), and the turn's AgentRun carries
  `control.anvil.hazyforge.io/jev-intent` / `.../jev-confidence` /
  `.../jev-model` annotations (plus
  `.../jev-needs-manager-create=true` on the actionable create path;
  `kubectl get agentrun <turn> -o yaml`).
  Desktop shows the read-only `jevIntent` caption on the user message when
  present; above-bar `create_agent_request` now also carries the
  request-only fulfillment hint (peer-safe `requestPeer` STATUS_JSON
  shape in the prompt plus the structured request flag) for a
  Wrapper/manager to fulfill — peers never create agents.

### Live Kind-local Jev intent e2e validation (2026-09-18)

First live end-to-end validation of the wired hook (Austin, 2026-09-18
~8:36 PM CT, WSL): Kind-local API on `127.0.0.1:18180` with
`ANVIL_AGENTS_STANDING_LIVE=1`, `ANVIL_AGENTS_JEV_INTENT=1`, and
`TYPESAFE_API_KEY` from `~/CodingFiles/PROJECTS/typesafe-jev/.env`.

- `"create an agent named Scout for research"` classified live as
  `create_agent_request` with `jevConfidence=1`, `jevModel=jev-1.13.0`,
  `jevUnclear=false` on the queued user message metadata; firstToken
  ~522ms; the assistant reply followed the manager-only create routing
  hint (request a Wrapper/manager through the existing
  manager-authorization path — no peer creation).
- Truncated `"create"` returned `jevRawChoice=create_agent_request` but
  gated to `jevIntent=unclear` at `jevConfidence=0.42` under the default
  0.5 floor — the confidence gate working as designed.
- Plane boundaries held: standing in-process + WebSocket stayed the primary
  interactive path (Jobs for scouts/batch, Substrate optional), and
  `create-agent` stayed Wrapper/manager-only (classification plus the
  request-only hint — no peer creation).

Serving-model note: the live decision reported `jev-1.13.0` via the
`jev-latest` alias (`jev.DefaultModel`; the turn path passed no model
override at validation time). `jev-1.13.0` is the validated serving model
from this run: pin it with `chat.jevModel: jev-1.13.0` (chart
`api.config.chat.jevModel`, or `ANVIL_AGENTS_JEV_MODEL=jev-1.13.0`) once
thresholds are tuned, instead of tracking `jev-latest`. Empty
`chat.jevModel` (the default) keeps the alias behavior unchanged.

Remaining after this validation: retune `DefaultConfidenceThreshold`
(0.5) and `DestructiveActionConfidenceBar` (0.8) from live labeled
traffic. The FakeBackend labeled-fixture suite and the fulfillment-site
bar (`create_agent_request` / `peer_handoff` needs-* flags and
STATUS_JSON hints withheld below 0.8; `tool_run` and `chat_reply` stay
on the classify floor) are landed; the gate stays deny-by-default via
`ANVIL_AGENTS_JEV_INTENT`. Desktop shows a minimal read-only
`jevIntent` caption on classified user messages (`EntityChatPage`:
intent plus confidence/model when present — display only), including
below-bar create/handoff where the class is kept for observability
without a needs-* flag. The `create_agent_request` fulfillment slice
wires above-bar classification to the existing peer-safe `requestPeer`
STATUS_JSON shape plus the `jevNeedsManagerCreate` /
`jev-needs-manager-create` request flag (see
"`create_agent_request` fulfillment slice" above). The validated model
pin (`chat.jevModel: jev-1.13.0` in the Kind-local example,
`ANVIL_AGENTS_JEV_MODEL` per-process) is landed with the gate still
deny-by-default.
