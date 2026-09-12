# Anvil Agents Desktop

**Anvil Agents Desktop** is the workstation OIDC client for Primaris cluster
agents. Chat and Wrapper are the product. Local harness activation is a second
page. It is not a second console and not a Kubernetes UI. The process binary is
`anvil-desktop`.

## What it is

Anvil Agents Desktop listens on loopback only (`127.0.0.1`):

- Defaults `--api-origin` to `https://agents.anvil.hazyforge.io` and reverse-proxies `/api/` and `/ui-config.json`
- Signs in with Authorization Code + PKCE (tokens in `sessionStorage`, never query strings)
- **Chat** (`/chat`): named Primaris agents; the wrapper can POST AgentRunProfiles
- **Wrapper** (`/wrapper`): create_agent and anvil-api (list/get/create runs)
- **Local** (`/local`): second function — activate already-installed Codex, Grok, OpenCode on native PATH or WSL

Register `http://127.0.0.1:1738/auth/callback` on the **Native** OIDC client
(`desktop.oidcClientId` in ui-config). Prefer that id over Console
`oidc.clientId`. The API must already have exact issuer, audience, claim
binding, namespace authorization, and CORS origins; Desktop does not loosen
those rules.

The client is standard OIDC (Authorization Code + PKCE), the same pattern as
`web/console`. Production IdP is **Zitadel**. Kind tests use a local issuer.
Switching is issuer / audience / client id in `ui-config.json` only.

## Run

```bash
# terminal 1 — local host + (optional) stub UI
go run ./cmd/anvil-desktop --listen 127.0.0.1:1738 --api-origin http://127.0.0.1:18080

# terminal 2 — Vite UI with /local, /api, and /ui-config.json proxied
cd web/desktop
npm install
npm run dev
```

Vite's origin is `http://127.0.0.1:5174`. For a real OIDC login, prefer serving
the built SPA from the Go host so the redirect URI is `http://127.0.0.1:1738/auth/callback`:

```bash
make desktop-build
go run ./cmd/anvil-desktop --ui-dir web/desktop/dist --open --api-origin http://127.0.0.1:18080
```

Kind-local anvil-agents API is the default test origin. Do not point this
loopback client at production Zitadel unless that client explicitly lists
`http://127.0.0.1:1738/auth/callback`.

```bash
./hack/run-anvil-desktop.sh --open --detach --api-origin http://127.0.0.1:18080
```

`--open` uses a dedicated Chrome user-data-dir so a previous SPA on
`:1738` is not reused from the shared browser cache. Optional Electron wrap
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
| `POST /local/v1/prefs` | Save `apiOrigin`, `harnessTarget` (`native`/`wsl`), optional `wslDistro` (no tokens) |
| `POST /local/v1/delegate` | Run a catalog CLI on native PATH or via WSL (no OIDC token) |
| `GET /ui-config.json` | Proxied from the API (product title rewritten) |
| `/api/v1/namespaces/…` | Proxied to the API with the caller's `Authorization` header |

Prefs live in `~/.config/anvil-desktop/config.json` as
`{ "apiOrigin": "https://…", "harnessTarget": "wsl" }`. Anvil Agents Desktop
never inspects Secrets, never binds a non-loopback address, and never treats
kubeconfig as the control plane.

Workstation installers: [`docs/desktop-install.md`](../../docs/desktop-install.md).
