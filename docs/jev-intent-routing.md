# Jev intent routing (spike)

Status: spike — client + fixed intent router + probe landed; no turn-path
wiring yet. Standing in-process chat plus WebSocket delivery remains the
primary interactive path; Jobs stay the default for scouts/batch; Substrate
stays optional. Direction set by Austin 2026-09-18: use Jev when something
needs decision making at runtime (e.g. decide user intent and route based
on intent) — NOT for chat generation.

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

## Rules this spike holds

- Jev never replaces chat generation; there is no generation path here.
- Substrate is untouched and out of scope.
- `create-agent` stays Wrapper/manager-only. The `create_agent_request`
  intent is classification only: fulfillment must request a manager (the
  existing manager-authorization path still owns that decision) — peers
  never create agents. Destructive fulfillment must apply a higher
  confidence bar in code at the fulfillment site, not in the router.
- No invented live API responses: fakes in CI, live only behind the env key.

## Where the intent hook plugs in (not yet wired)

The future hook is `queueChatTurnInternal` in
`internal/runapi/chat_execution.go`, before `buildChatPrompt` freezes the
execution intent: classify the incoming message (+ small thread tail),
record the decision alongside the turn for observability, and let routing
observe — never rewrite — the durable turn. The standing execution path
itself (`reconcileStandingTurn`) does not change: one append-only AgentRun
per accepted message, frozen intent in, `Succeeded` completion out, peer
fanout through `dispatchChatCoordination` unchanged.

Remaining wiring to the live Desktop path:

1. Call `Router.ClassifyIntent` from the chat-append path behind an
   opt-in gate (env/config, deny by default like `standing.liveEnabled`),
   with Jev failures falling back to today's behavior (never route on a
   guess).
2. Decide what each intent drives: `peer_handoff` → `requestPeer`
   coordination hints, `tool_run` → tool-first prompting, `unclear` →
   clarification copy, `create_agent_request` → manager request flow.
3. Surface the decision (intent + confidence + serving model) in turn
   metadata/observability so thresholds can be tuned against real traffic.
4. Tune the confidence floor (and the higher destructive-action bar) from
   labeled Anvil traffic; pin a versioned model ID once thresholds are
   tuned instead of tracking `jev-latest`.
