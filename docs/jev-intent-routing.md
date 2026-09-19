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
  fixed intent set with a confidence gate (default 0.5, mirroring the
  docs' example review floor). Below the floor — or on an unknown choice —
  the decision gates to `unclear` (human/clarification path) while keeping
  the raw model choice visible for observability.
- `cmd/jev-probe` — one-message classifier printing the routing decision
  as JSON. Live with `TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message
  "hello"`; fake (no network) with `-fake` or no key set.
- Unit tests (`internal/jev/*_test.go`) — question/request validation,
  HTTP auth/shape/retry behavior against `httptest`, and FakeBackend-style
  routing deciding each intent plus the confidence gate.
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
  intent is classification only: fulfillment must request a manager (the
  existing manager-authorization path still owns that decision) — peers
  never create agents. Destructive fulfillment must apply a higher
  confidence bar in code at the fulfillment site, not in the router.
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
| `create_agent_request` | hint routes toward requesting a Wrapper/manager through the existing manager-authorization path; names the Wrapper/manager-only boundary | Classification only — peers never create agents |
| `peer_handoff` | soft hint to coordinate only through the existing coordination contract (`requestPeer` delivery); no send outside existing paths; without enabled coordination, answer directly | No new fanout |
| `tool_run` | tool-first hint: run the harness tool step before finalizing, else note the needed lookup | Prompt hint only |
| `unclear` (choice, unknown output, or sub-threshold confidence) | clarification-first hint: ask a brief clarifying question; no destructive/delegating/creating acts | — |

### Observability (threshold tuning)

- Queued user message metadata: `jevIntent`, `jevRawChoice`,
  `jevConfidence`, `jevModel` (serving model), `jevUnclear` — alongside the
  existing `authorKind` keys, visible in thread detail.
- Turn AgentRun annotations: `control.anvil.hazyforge.io/jev-intent`,
  `.../jev-confidence`, `.../jev-model` — `kubectl`-visible per turn.
- The prompt hint itself carries `(Jev intent X, confidence N, model M)`.

### What still needs a live `TYPESAFE_API_KEY` probe

1. Live accuracy/latency numbers per intent against real Anvil traffic
   (`TYPESAFE_API_KEY=... go run ./cmd/jev-probe -message "..."`); CI only
   drives fakes, so no live responses are pinned anywhere.
2. Tune the confidence floor (default 0.5) — and the higher
   destructive-action bar at the fulfillment site — from labeled traffic;
   pin a versioned model ID once thresholds are tuned instead of tracking
   `jev-latest`.
3. Confirm per-turn p70–p130 overhead stays inside the 70–500ms System One
   envelope on the append path (the 10s cap is a wedge guard, not a
   budget).

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
  `create-agent` stayed Wrapper/manager-only (classification only).

Serving-model note: the live decision reported `jev-1.13.0` via the
`jev-latest` alias (`jev.DefaultModel`; the turn path passes no model
override and there is no `chat.jev*` model config field, so no pin is made
here). Follow-up: pin a versioned model ID once thresholds are tuned
instead of tracking `jev-latest` — that needs a new config/API field and is
deliberately out of scope for this docs-only update.

Remaining after this validation: confidence-floor tuning from labeled
traffic (plus the higher destructive-action bar at the fulfillment site),
and Desktop UI surfacing of the intent metadata (`jevIntent`,
`jevConfidence`, `jevModel`, `jevUnclear`).
