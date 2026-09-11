# Anvil Agents Desktop

**Anvil Agents Desktop** is a local wrapper agent: OIDC sign-in to the
anvil-agents API, plus delegation to workstation harness CLIs. It is not a
second console and not a Kubernetes UI. The process binary is `anvil-desktop`.

## What it is

Anvil Agents Desktop listens on loopback only (`127.0.0.1`):

- Discovers Codex, Grok, OpenClaw, OpenCode, Hermes, Pi, and similar CLIs on PATH
- Reverse-proxies `/api/` and `/ui-config.json` to a configured OIDC API origin
- Signs in with Authorization Code + PKCE (tokens in `sessionStorage`, never query strings)
- Wraps two tools: `anvil-api` and `local-harness`

Register `http://127.0.0.1:1738/auth/callback` on the API's OIDC client. The
API must already have exact issuer, audience, claim binding, namespace
authorization, and CORS origins; Desktop does not loosen those rules.

## Run

```bash
# terminal 1 — local host + (optional) stub UI
go run ./cmd/anvil-desktop --listen 127.0.0.1:1738 --api-origin https://agents.example.com

# terminal 2 — Vite UI with /local, /api, and /ui-config.json proxied
cd web/desktop
npm install
npm run dev
```

Vite's origin is `http://127.0.0.1:5174`. For a real OIDC login, prefer serving
the built SPA from the Go host so the redirect URI is `http://127.0.0.1:1738/auth/callback`:

```bash
make desktop-build
go run ./cmd/anvil-desktop --ui-dir web/desktop/dist --open --api-origin https://agents.example.com
```

`--open` uses Chrome/Chromium `--app=` when available. Optional Electron wrap
(window title **Anvil Agents Desktop**):

```bash
# after anvil-desktop is listening
cd web/desktop
npx electron electron/main.mjs
```

Print inventory without a window:

```bash
go run ./cmd/anvil-desktop --snapshot
```

## Local API

| Endpoint | Purpose |
| --- | --- |
| `GET /healthz` | Liveness of the desktop host |
| `GET /local/v1/snapshot` | Harnesses, API origin health, wrapper tool list |
| `GET /local/v1/api-health` | Unauthenticated probe of `{apiOrigin}/healthz` |
| `POST /local/v1/prefs` | Save `apiOrigin` (no tokens) |
| `POST /local/v1/delegate` | Run a catalog CLI with a prompt (no OIDC token) |
| `GET /ui-config.json` | Proxied from the API (product title rewritten) |
| `/api/v1/namespaces/…` | Proxied to the API with the caller's `Authorization` header |

Prefs live in `~/.config/anvil-desktop/config.json` as `{ "apiOrigin": "https://…" }`.
Anvil Agents Desktop never inspects Secrets, never binds a non-loopback
address, and never treats kubeconfig as the control plane.
