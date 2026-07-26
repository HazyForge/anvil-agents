# Anvil Agents Console

Read-only observer SPA for `anvil-agents-api` AgentRuns.

## Stack

- Vite + React + TypeScript
- Served at `/` by `anvil-agents-api` (API remains under `/api/v1/...`)

## Auth (OIDC PKCE)

Browser sign-in uses **OIDC Authorization Code + PKCE** against the issuer
from `GET /ui-config.json` (no client secret). Access and refresh tokens are
stored in **sessionStorage** for the tab only. After the callback, `code` /
`state` are removed from the URL; tokens are never left in query strings.

## Local development

```bash
# terminal 1: API (with OIDC config pointing at your issuer)
go run ./cmd/anvil-agents-api --config /path/to/config.yaml

# terminal 2: console with /api proxy to :8082
cd web/console
npm install
npm run dev
```

Optional:

- `VITE_DEV_API_PROXY=http://127.0.0.1:8082` — Vite proxy target (default above)
- `VITE_API_BASE=https://agents.example.com` — absolute API origin instead of relative `/api`

## Production build

```bash
# from repo root — SPA only
make console-build

# optional: copy into go:embed tree for a local anvil-agents-api binary
make console-embed
# ... test the binary ...
make console-embed-restore   # put the committed stub back before git commit
```

`make console-build` writes `web/console/dist` (gitignored).

`make console-embed` copies that build into `internal/runapi/consolefs/dist`
for `go:embed`. That path is also the committed stub tree, so local embeds
dirty the worktree. Always run `make console-embed-restore` (or avoid
`console-embed` and use Docker) before committing.

The controller/API Docker image builds the console in a Node stage and embeds
the assets before compiling `anvil-agents-api` without modifying the host tree.

Without a console build, the API still serves a stub page explaining how to
embed assets (source of truth: `internal/runapi/consolefs/stub/`).

### Serve prebuilt assets without recompiling

```yaml
ui:
  staticDir: /path/to/web/console/dist
```

### CORS for the browser console

The API deny-by-default CORS allowlist uses **exact** origins only (no
same-host fallback). When the SPA and API share a public host, configure:

```yaml
api:
  config:
    cors:
      allowedOrigins:
        - https://agents.example.com
```

Browser `fetch()` sends an `Origin` header even for same-host console calls.
Omit the origin and API responses return `origin_denied`.

Local Vite dev (`http://127.0.0.1:5173`) proxies `/api` so the browser Origin
is the Vite origin; either proxy-only (no cross-origin) or add the Vite origin
to `allowedOrigins` when pointing `VITE_API_BASE` at a remote API.

## Screens (Phase 1)

1. **Run board** — dense list, client-side filters, `limit=200`
2. **Run detail** — decision, reports, composition, output
3. **Live stream** — authenticated `fetch()` SSE (not `EventSource`)

## Non-goals

No rerun/pause/approve/merge controls. No Secret inspection. No schedule or
profile management UI.
