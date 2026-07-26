# Agent Frontend Planning

This document captures the product and implementation direction for adding a
frontend to observe what `anvil-agents` runs are doing.

## Goal

Build a read-only **Anvil Agents Console** for `anvil-agents`: a live console
where operators can see queued, running, completed, failed, and human-attention
AgentRuns without using `kubectl`.

The frontend should answer:

> What are the agents doing right now, what did they decide, where are they
> stuck, and what evidence did they produce?

**Primary success criterion (verification):** replace `kubectl` for day-to-day
AgentRun observation against real `hazy-trade` runs.

## Locked Decisions

Decisions refined 2026-07-26:

| Topic | Decision |
| --- | --- |
| Product name | **Anvil Agents Console** |
| Deploy shape | SPA under `web/console/`, served by `anvil-agents-api` (same image/process) |
| Stack | Vite + React + TypeScript |
| Auth v1 | Manual bearer token paste; OIDC Authorization Code + PKCE later |
| Namespace scope | Multi-namespace from day one (UI-level; API remains per-namespace) |
| Phase 1 screens | Run board + run detail + live stream only |
| Filters | Client-side filters + higher list `limit` (up to API max 200) |
| Attention queue (Phase 2) | Prioritize `Failed` + non-empty `error` first (not NeedsHuman-only) |
| Verification sample | Live `hazy-trade` namespace on anvil-primaris |
| Schedules/profiles UI | Not in Phase 1; runs only (source refs still shown on runs) |
| Mutations | Never in this console; policy plane stays external |

## Current Backend Foundation

The repository already includes the optional `anvil-agents-api` process, which
exposes sanitized AgentRun state and live streams through OIDC-protected read
endpoints. On anvil-primaris this is already deployed:

- Deployment: `anvil-agents-system/anvil-agents-system-api`
- HTTPRoute host: `agents.anvil.hazyforge.io`

Existing endpoints:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/namespaces/{namespace}/agent-runs` | List authorized AgentRun summaries (`limit` 1–200, default 50) |
| `GET /api/v1/namespaces/{namespace}/agent-runs/{name}` | Read one sanitized AgentRun summary |
| `GET /api/v1/namespaces/{namespace}/agent-runs/{name}/events` | Stream snapshot, status, log, and terminal Server-Sent Events |
| `GET /healthz` / `GET /readyz` | Health and readiness probes |

Important current boundaries:

- The API is read-only.
- The API has its own ServiceAccount and RBAC boundary from the controller.
- The API must not gain Secret access or mutation verbs.
- The API accepts bearer access tokens only through the `Authorization` header.
- Access tokens in query strings are rejected.
- Browser clients must use authenticated `fetch()` and parse the stream manually
  because native `EventSource` cannot set an `Authorization` header.
- The API verifies the fixed controller-owned `agent` container logs and does
  not allow clients to choose arbitrary Pods, Jobs, or containers.
- Stream events include: `snapshot`, `status`, `log`, `terminal`, plus
  `reset`, `error`, and `complete` for reconnect / limit / token expiry cases.

### Sanitized view fields (what the UI can render)

`AgentRunView` exposes: name, namespace, uid, resourceVersion, createdAt, phase,
backend, intent, source (kind/name/namespace), application, applicationTarget,
job, runnerPod, startedAt, completedAt, conditions, decision, reports,
resolvedComposition, pullRequestURL, error, output (detail only), archived.

Decision fields: `classification`, `action`, `summary`, `residualRisk`.

Report fields include: type, observedAt, level, stage, classification, action,
summary, detail, pullRequestURL, residualRisk, `needsHuman`, `humanFollowUp`.

Human follow-up text lives on **reports**, not on decision. The detail panel
should surface residual risk from decision and human follow-up from the latest
relevant report.

## Product Direction

Product name in UI copy: **Anvil Agents Console**.

The first version is an observer console, not a control panel.

Product promise:

> A compact operational console for every durable agent loop running on the
> cluster.

## MVP Screens

### Phase 1: Run Board

A dense operational list of AgentRuns.

Useful fields:

- Phase: `Pending`, `Running`, `Succeeded`, `Failed`, `NeedsHuman`
- Intent
- Backend: Codex, Pi, OpenCode, Hermes Agent, OpenClaw, Grok Build, custom
- Application and application target
- Created, started, completed, and duration
- Source kind + name (schedule vs ManualRequest child, etc.)
- Pull request URL, when available
- Latest report summary (when present)
- Error indicator (short error text)

Useful filters (client-side in Phase 1):

- Namespace (active namespace; multi-namespace via switcher / multi-select)
- Phase
- Application
- Application target
- Backend
- Source kind / name
- Only running
- Only failed
- Search by run name, intent, application, or error text

List fetch should request a high `limit` (near 200) so client-side filters work
on current production volumes without pagination in Phase 1.

### Phase 1: Run Detail

A detail view for one selected AgentRun.

Sections:

- Header: name, namespace, phase, backend, application / target, started /
  completed, archived status
- Decision panel: action, summary, residual risk, pull request URL
- Human follow-up from reports when present
- Reports timeline
- Conditions
- Resolved composition:
  - selected profile/object identities
  - harness profile
  - skill/tool set identities
  - effective-spec digest
  - mounted-payload digest
- Output/status
- Live log stream (embedded or linked stream view)

### Phase 1: Live Stream View

A terminal-like but readable live view using the existing stream endpoint.

Capabilities:

- Show snapshot events
- Show status changes
- Show log lines
- Show terminal events
- Show reconnect/reset state
- Copy run name
- Toggle follow-latest behavior

### Phase 2: Human Attention Queue

Not required for Phase 1. When added, prioritize for real `hazy-trade` traffic:

1. `Failed` phase with non-empty `error` (dominant current triage surface)
2. `NeedsHuman` phase
3. Report `needsHuman` / non-empty `humanFollowUp`
4. Stale running runs (client-side age threshold)
5. Optionally runs with pull request URLs

Do not make the queue NeedsHuman-only; live sample often has zero NeedsHuman
while Failed/error volume is high (often ExternalSecret freshness failures
before the agent even starts).

## Verification Sample: hazy-trade

Use the live `hazy-trade` namespace on anvil-primaris as the primary verification
target. Observed shape at planning time (~2026-07-26):

| Observation | Implication for UI |
| --- | --- |
| ~130 AgentRuns in one namespace | Default list `limit` 50 is too low; use high limit + client filters |
| Two applications: `hazy-trade-agent-manager` and `hazy-trade` | Application filter is essential on day one |
| Backends: codex + grokBuild | Backend filter is essential |
| Sources: hourly AgentSchedules + many ManualRequest children | Show source kind/name; search must work for `manager-hazy-trade-*` |
| ~half Succeeded / half Failed; NeedsHuman often empty | Attention UX must center Failed + error |
| Many fails: ExternalSecret not fresh | Surface short `error` on the board without opening logs |
| Rich decisions/reports on successful auditor/manager rounds | Decision panel + reports timeline are high value |
| ~18 runs with GitHub PR URLs | PR links on board and detail |
| API host already public | Console can ship to same origin as `agents.anvil.hazyforge.io` |

Useful fixture classes:

- Succeeded auditor with decision + reports + long output
- Failed manager with ExternalSecret error and empty decision
- Succeeded manager/child with `pullRequestURL`
- Schedule-sourced vs ManualRequest-sourced runs

Static loops documented in `HazyForge/hazy-trade` under `.hazyforge/agents/`:

- Manager schedule/profile: `hazy-trade-agent-manager-1h` / `hazy-trade-agent-manager`
- Production auditor schedule/profile: `hazy-trade-production-auditor`

## Visual Direction

The frontend should avoid generic SaaS styling. It should feel like a Hazy Forge
/ Anvil operational console.

Direction:

- Deep black-green or smoked graphite background
- Layered panels with restrained borders
- Emerald/teal live signals
- Amber attention states
- Red failure states
- Compact inspectable data density
- Barlow-style all-caps labels where appropriate
- Operational, readable, and atmospheric rather than decorative

Target feeling:

> Cluster operations + agent telemetry + forge command room.

## Architecture

### Chosen: SPA served by `anvil-agents-api`

Add a frontend app under:

```text
web/console/
```

Build static assets and serve them from the existing `anvil-agents-api` process
(same chart path, same host/origin as the API).

Why:

- One chart deployment path
- One host/origin with simpler CORS
- API and UI versions stay aligned
- Simple first release story

Tradeoff: frontend build tooling joins the repo/release process and frontend
releases ship with the controller/API image.

Deferred options:

- Separate frontend image (independent cadence, more deploy complexity)
- Anvil Hub product surface (eventual integration, too heavy for MVP)

### Multi-namespace from day one

The API remains namespaced in the URL path. The console should:

- Let the operator select or enter one or more authorized namespaces
- Persist the active namespace set in local storage
- Issue list/get/stream calls per selected namespace
- Label every row/detail with namespace

There is no namespace discovery endpoint yet. Do not invent wildcard list calls.
Authorization remains explicit per namespace binding on the access token.
Optional default namespace can come from chart/UI config later.

## Authentication Direction

The API expects a JWT access token in:

```http
Authorization: Bearer <token>
```

### Phase 1 Auth

Manual bearer token paste in the console UI (stored only in browser memory or
local storage for operator convenience). Focus on observation UX first.

### Later Auth

OIDC Authorization Code + PKCE in the frontend.

Non-secret config fields:

- issuer
- client ID
- requested audience
- default / suggested namespaces
- API base path if needed

The API remains a resource server only. It does not log users in, issue tokens,
refresh tokens, or manage sessions.

## Potential API Additions

The current API is enough for Phase 1. Useful later additions:

### Static UI Config Endpoint

```text
GET /ui-config.json
```

Possible non-secret fields: product title, default namespaces, OIDC issuer,
OIDC client ID, requested audience, API base path.

### Server-side List Filters

Query parameters such as `phase`, `application`, `applicationTarget`,
`backend`, `sourceKind`, plus pagination. Client-side filtering is enough while
namespaces stay near a few hundred runs.

### Namespace Discovery

Only if it preserves the explicit authorization model (return namespaces the
token is already allowed to read, never a cluster-wide inventory).

### Optional view enrichments

If the board needs them without detail fetches:

- `purpose` from the AgentRun spec
- latest report summary / needsHuman rolled up on list views

### Durable Log Store Integration

Pod-log streams are not a complete historical archive. Durable replay should
later use Loki or the archive strategy.

## Non-Goals For The First Version

The console must not add mutation controls.

Do not include:

- rerun buttons
- pause/resume controls
- approval buttons
- merge buttons
- repository mutation controls
- policy-broker decisions
- Secret or credential inspection
- schedule/profile management UI (Phase 1+)

Reason: this repository keeps manager authorization, repository mutation, and
product delivery policy outside the controller/API boundary. Future action
workflows go through a separate trusted policy plane.

## Implementation Phases

### Phase 1: Console Skeleton

- Add Vite + React + TypeScript SPA under `web/console/`.
- Serve static assets from `anvil-agents-api`.
- Brand as **Anvil Agents Console**.
- Manual bearer token entry.
- Multi-namespace switcher / multi-select (API calls stay per namespace).
- Run board with high list limit and client-side filters/search.
- Run detail with decision, residual risk, reports, composition, error, output.
- Live stream via authenticated `fetch()` SSE parsing.
- Verify against live `hazy-trade` runs until kubectl is unnecessary for normal
  observation.

### Phase 2: Operator-Quality UX

- Human attention queue prioritizing Failed + error.
- Stronger phase/status visual system.
- Report and decision timeline polish.
- Pull request links and stale-running detection.
- Application/backend/source filter presets useful for hazy-trade.

### Phase 3: Browser OIDC Login

- Authorization Code + PKCE.
- Token refresh/session handling.
- Chart-configured frontend OIDC values.
- Exact CORS origins aligned with the console origin.

### Phase 4: Production Hardening

- Security headers / CSP for static frontend.
- Frontend tests.
- Chart values and documentation.
- Screenshots in docs.
- Release/build workflow support.
- Optional server-side list filters and pagination.

## Remaining Open Questions

1. Exact multi-namespace UX: single active namespace with quick switch, or true
   multi-namespace merged board?
2. Token storage: session-only memory vs localStorage persistence?
3. Console path prefix: `/` vs `/console/` on the API host?
4. When to add `GET /ui-config.json` vs hard-coded defaults in the SPA build?
5. Should list views later expose `purpose` and rolled-up latest report without
   a detail fetch?

## Current Recommendation

Ship a read-only **Anvil Agents Console** SPA served by `anvil-agents-api`,
built with Vite + React + TypeScript, using manual bearer token auth first.
Phase 1 is board + detail + stream with multi-namespace UI selection and
client-side filters. Verify until operators can observe `hazy-trade` AgentRuns
without `kubectl`. Add the Failed-first attention queue and OIDC PKCE after the
core observation loop works.
