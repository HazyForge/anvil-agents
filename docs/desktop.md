# Anvil Agents Desktop

**Anvil Agents Desktop** is the workstation process that signs in to the
anvil-agents OIDC API and talks to **cluster agents on Anvil Primaris**. The
process binary is `anvil-desktop`. The product is Anvil Agents Desktop — not
Anvil Desktop, Anvil Hub, or a kube UI.

It has **nothing to do with Kubernetes**. There is no kubeconfig, kubectl, or
cluster context picker. The installed executable calls
`https://agents.anvil.hazyforge.io` (Authorization Code + PKCE), then Chat and
Wrapper use the same AgentRun API the browser console uses.

Chat selects an agent, a remote harness, and an optional Manager role. Both roles
use the same persisted chat service. Explicit peer allowlists enable real message
delegation; each recipient executes independently. See [Remote harness chat](standing-chat.md).
The Local page separately discovers installed workstation CLIs on native PATH
or in WSL. Local activation never copies the OIDC token into CLI arguments,
environment variables or prompt files.

An agent's cluster harness — managers included — can be switched from Chat
without kubectl. Open the agent's conversation, expand Conversation details,
and use the Cluster harness picker: it lists same-namespace
`AgentHarnessProfile` objects (name, backend kind, model when set), shows the
harness from `AgentRunProfile.spec.harnessProfileRef`, and PATCHes only that
ref (`PATCH /api/v1/namespaces/{ns}/agent-run-profiles/{name}/harness`).
Skills, tools, and scope are preserved and never rewritten; the next chat turn
resolves the new harness. GitOps-owned or non-console-managed profiles stay
read-only, and the picker requires `composition.writeEnabled` on the API.

The **wrapper** (`anvil-desktop-wrapper`) and **project manager** harnesses
have a baked-in **create-agent** skill/tool: name + description →
`AgentRunProfile` (and a standing-chat thread when chat is enabled). Peers
request create-agent via existing `requestPeer` STATUS_JSON
(`request=create-agent`); they must not create profiles. Desktop shows a
**Requested create** receipt instead of failing silently.

See `skills/create-agent/SKILL.md`.

## Why Anvil Agents Desktop

The optional OIDC AgentRun API (`anvil-agents-api`) is a separate process from
the controller. The installed desktop **process** is an OIDC client of that
API: it signs in and calls cluster agents (Chat / Wrapper). The console SPA
cannot see laptop PATH; the Local page is a **second** function that discovers
already-installed CLIs (including those that only exist inside WSL) without
mixing that into Primaris chat.

Local CLIs are **not** the cluster harness. AgentRuns still use runner images
selected by `AgentHarnessProfile`. Local activation never copies the OIDC token
into a CLI argv, environment, or prompt file. Those CLIs keep their own local
auth files (`~/.codex/auth.json`, and so on).

## OIDC session (same as the console)

Desktop loads `{apiOrigin}/ui-config.json` (unauthenticated) for issuer,
audiences, client id, scopes, and feature flags. Prefer
`desktop.oidcClientId` (Zitadel Native PKCE). Do not stay on the Console
User-Agent `oidc.clientId` once that Native id exists. When the field is
absent, Desktop still signs in with Console and shows a warning. The
Kind/desktop pattern name is `anvil-agents-desktop` (`--oidc-client-id` in
tests). Login is Authorization Code + PKCE (S256), the same provider-neutral
flow as `web/console`. `state` / `nonce` / verifier live in `sessionStorage`
under `anvil-agents-desktop.*` keys, then are removed. The access token stays
in memory/`sessionStorage`. Redirect is `{origin}/auth/callback`
(`http://127.0.0.1:1738/auth/callback` on the installed host); after exchange
the app strips `code` and `state` from the address bar.

Production IdP is **Zitadel**. Tests use a **local Kind issuer**, not production
Zitadel. The desktop does not embed Zitadel APIs; switching IdPs is
`ui-config` issuer, audience, and client id only.

The loopback host reverse-proxies `/api/` and `/ui-config.json` to the
configured API origin so the SPA is same-origin. Register
`http://127.0.0.1:1738/auth/callback` on the **Native** OIDC client (Kind
client `anvil-agents-desktop` for tests; Zitadel Native `anvil_agents_desktop`
in production). Vite dev (`http://127.0.0.1:5174/auth/callback`) needs the
same if you sign in there. Prefer `anvil-desktop --ui-dir` for real login.

AGENTS.md still applies: deny by default, exact issuer/audience/claim binding
and namespace authorization on the API, no Secret access, AgentRuns are
append-only, tokens are never accepted in query strings.

## Layout

| Path | Role |
| --- | --- |
| `cmd/anvil-desktop` | Loopback host + optional Chrome `--app` window |
| `internal/desktop` | Catalog, native/WSL discovery, API origin prefs, reverse-proxy, local delegate |
| `web/desktop` | Vite + React shell (OIDC login, named-agent chat, Wrapper, Local delegator) |
| `web/desktop/electron` | Optional single-window Electron wrap of the desktop SPA |

## Security

- Listen address must be loopback.
- Prefs store `apiOrigin`, `harnessTarget` (`native`/`wsl`), and optional
  `wslDistro` only. No bearer tokens, no kube user credentials.
- API origin must be http(s) with a host and without userinfo, path, query, or fragment.
- The host does not persist tokens and does not log `Authorization`.
- `/api/` proxy is allowlisted to `/ui-config.json` and `/api/v1/namespaces/…`.
- Requests with `access_token` / `id_token` / `refresh_token` query params are rejected.
- Version probes and delegates run only catalog binaries resolved on native PATH
  or via `wsl.exe --exec`, with constant argument lists. WSL distro names are
  allowlisted. The OIDC token is stripped from the `wsl.exe` environment
  (`WSLENV` included).
- Delegate prompts are capped at 64KiB; prompt files are 0600 temp files and deleted
  (WSL file-mode prompts use `/tmp/anvil-desktop-prompt.*` inside the distro).

Workstation installers: `docs/desktop-install.md`. On a display VM:

```bash
make desktop-run
# or: ./hack/run-anvil-desktop.sh --open --detach --api-origin http://127.0.0.1:18080
make desktop-package
```

## Peer/chat reliability

`make desktop-chat-tests` is the continuous drop/stall/wrong-routing +
create-agent allowlist gate (pure parser unit tests, no browser or cluster).
