# Anvil Agents Desktop

**Anvil Agents Desktop** is the local operator UI for workstation harness CLIs
and kubecontext access. It wraps the existing Anvil Agents Console instead of
forking a second cluster UI. The process binary is `anvil-desktop`.

## What it is

Anvil Agents Desktop listens on loopback only (`127.0.0.1`):

- Discovers Codex, Grok, OpenClaw, OpenCode, Hermes, Pi, and similar CLIs on PATH
- Lists kubeconfig contexts and probes `control.anvil.hazyforge.io/v1alpha1`
- Stores the selected context and console origin in `~/.config/anvil-desktop/config.json`
- Opens the cluster console as a **top-level** window (the API sets `frame-ancestors 'none'`, so an iframe cannot wrap it)

Standing chat and future per-council chat stay in `web/console`. Anvil Agents
Desktop deep-links to `/chat` when a console origin is configured. It does not
add a Conversation CRD, Secret access, or AgentRun mutations.

## Run

```bash
# terminal 1 — local API + (optional) stub UI
go run ./cmd/anvil-desktop --listen 127.0.0.1:1738

# terminal 2 — Vite UI with /local proxy
cd web/desktop
npm install
npm run dev
```

Build the SPA into the Go host:

```bash
make desktop-build
go run ./cmd/anvil-desktop --ui-dir web/desktop/dist --open --kubeconfig "$KUBECONFIG"
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
| `GET /healthz` | Liveness |
| `GET /local/v1/snapshot` | Harnesses, kubecontexts, operator probe, console health |
| `POST /local/v1/prefs` | Save kubecontext + console origin (no tokens) |

Anvil Agents Desktop never accepts kube tokens in the UI, never inspects
Secrets, and never binds a non-loopback address.
