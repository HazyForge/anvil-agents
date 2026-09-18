# Anvil Agents Desktop

**Anvil Agents Desktop** is the workstation OIDC client for Primaris cluster
agents. Chat and Wrapper are the product. Local harness activation is a second
page. It is not a second console and not a Kubernetes UI. The process binary is
`anvil-desktop`.

## What it is

Anvil Agents Desktop listens on loopback only (`127.0.0.1`):

- Defaults `--api-origin` to `https://agents.anvil.hazyforge.io` and reverse-proxies `/api/` and `/ui-config.json`
- Signs in with Authorization Code + PKCE (tokens in `sessionStorage`, never query strings)
- **Chat** (`/chat`): manager harness; baked-in **create-agent** (manager only)
- **Wrapper** (`/wrapper`): Wrapper **create-agent** and anvil-api (list/get/create runs). Peers request create-agent via requestPeer.
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

## Chat-latency capture

Every Desktop Chat send records send → waiting → first token → running →
reply ready timings. The summary goes to `console.debug`; a durable JSONL
sink is opt-in for latency work (no secrets are ever written):

```bash
export ANVIL_CHAT_LATENCY_JSONL=$HOME/CodingFiles/HAZYFORGE/anvil-agents/.runtime/chat-latency.jsonl
./hack/run-anvil-desktop.sh
# or: go run ./cmd/anvil-desktop --chat-latency-jsonl "$ANVIL_CHAT_LATENCY_JSONL" ...
```

The path must be **absolute** (relative/empty disables the sink) and must be
set on the **host** process: the Desktop UI runs in the browser (or Electron
with `nodeIntegration: false`), where `process.env`/Node `fs` are
unavailable, so the renderer POSTs each settled report to the same-origin
loopback endpoint `POST /local/v1/chat-latency` and the host appends one
JSON line. Every line carries `ts` plus whichever of `waitingMs`,
`firstTokenMs`, `sendToFirstTokenMs` (= `firstTokenMs`, the standing-latency
vocabulary), `runningMs`, `replyReadyMs`, `failedMs` were observed, with
`source: "desktop-chat"`, the `threadId` the turn ran on, the `sessionId`
(standing session name from the stream snapshot, when the turn took the
standing path), the `path` tag (`standing` vs `job`), and `error` when the
turn did not deliver. `GET /local/v1/snapshot`
reports `chatLatencyJsonlEnabled: true/false` (the absolute path is never
leaked to the SPA). Both paths are fire-and-forget and never break chat.

Desktop Chat Send prefers the standing WebSocket live turn
(`openChatThreadStream` + live `token` frames, settled from the durable
thread record) and falls back to the existing POST path with the same
idempotency key when the stream is unavailable, the thread stays on the Job
plane, or OIDC denies the call. See
[`docs/standing-inprocess-harness.md`](../../docs/standing-inprocess-harness.md)
("Desktop chat e2e over the standing WebSocket") for the e2e run steps and
the fake-transport CI gate
(`node --experimental-strip-types --test hack/desktop-standing-chat-stream.mjs`).

## Local API

| Endpoint | Purpose |
| --- | --- |
| `GET /healthz` | Liveness of the desktop host |
| `GET /local/v1/snapshot` | Harnesses, API origin health, wrapper tool list |
| `GET /local/v1/api-health` | Unauthenticated probe of `{apiOrigin}/healthz` |
| `POST /local/v1/prefs` | Save `apiOrigin`, `harnessTarget` (`native`/`wsl`), optional `wslDistro` (no tokens) |
| `POST /local/v1/chat-latency` | Append one live chat-latency JSON line to the host's `ANVIL_CHAT_LATENCY_JSONL` sink (204; no-op when unset) |
| `POST /local/v1/delegate` | Run a catalog CLI on native PATH or via WSL (no OIDC token) |
| `GET /ui-config.json` | Proxied from the API (product title rewritten) |
| `/api/v1/namespaces/…` | Proxied to the API with the caller's `Authorization` header |

Prefs live in `~/.config/anvil-desktop/config.json` as
`{ "apiOrigin": "https://…", "harnessTarget": "wsl" }`. Anvil Agents Desktop
never inspects Secrets, never binds a non-loopback address, and never treats
kubeconfig as the control plane.

Workstation installers: [`docs/desktop-install.md`](../../docs/desktop-install.md).
