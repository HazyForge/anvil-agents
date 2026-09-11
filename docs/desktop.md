# Anvil Agents Desktop

**Anvil Agents Desktop** is a local wrapper agent for the anvil-agents OIDC
API and for harness CLIs already on the machine. The process binary is
`anvil-desktop`. The product is Anvil Agents Desktop — not Anvil Desktop, Anvil
Hub, or Anvil Primaris.

It has **nothing to do with Kubernetes**. There is no kubeconfig, kubectl, or
cluster context picker. Sign in with OIDC, then call the same AgentRun API the
browser console uses.

It exposes three tools:

1. **create_agent** — `POST /api/v1/namespaces/{namespace}/agent-run-profiles`
   (minimal AgentRunProfile; the API stamps `managed-by=anvil-agents-console`)
2. **anvil-api** — standing-chat threads/messages (PR 168) plus list/get AgentRuns
3. **local-harness** — delegate a prompt to Codex, Grok, OpenClaw, OpenCode, or
   another catalog CLI on PATH

The **wrapper** is the entity in Desktop chat: it can spawn more profiles when
asked. The PR 168 echo stub is not the wrapper reply. Cluster observation and
the console library remain console surfaces.

## Why Anvil Agents Desktop

The optional OIDC AgentRun API (`anvil-agents-api`) is a separate process from
the controller. The console SPA cannot see laptop PATH. Desktop does the work
the cluster SPA cannot: discover local CLIs and wrap them with an API client
that reuses the console's OIDC session pattern.

Local CLIs are **not** the cluster harness. AgentRuns still use runner images
selected by `AgentHarnessProfile`. The wrapper never copies the OIDC token into
a CLI argv, environment, or prompt file. Those CLIs keep their own local auth
files (`~/.codex/auth.json`, and so on).

## OIDC session (same as the console)

Desktop loads `{apiOrigin}/ui-config.json` (unauthenticated) for issuer, client
id, audiences, scopes, and feature flags. Login is Authorization Code + PKCE
(S256), the same provider-neutral flow as `web/console`. `state` / `nonce` /
verifier live in `sessionStorage` under `anvil-agents-desktop.*` keys, then are
removed. The access token stays in memory/`sessionStorage`. Redirect is
`{origin}/auth/callback`; after exchange the app strips `code` and `state` from
the address bar.

Production IdP is **Zitadel**. Tests use a **local Kind issuer**, not production
Zitadel. The desktop does not embed Zitadel APIs; switching IdPs is
`ui-config` issuer, audience, and client id only.

The loopback host reverse-proxies `/api/` and `/ui-config.json` to the
configured API origin so the SPA is same-origin. Register
`http://127.0.0.1:1738/auth/callback` on the OIDC client (Kind client for
tests; Zitadel client in production). Vite dev
(`http://127.0.0.1:5174/auth/callback`) needs the same if you sign in there.
Prefer `anvil-desktop --ui-dir` for real login.

AGENTS.md still applies: deny by default, exact issuer/audience/claim binding
and namespace authorization on the API, no Secret access, AgentRuns are
append-only, tokens are never accepted in query strings.

## Layout

| Path | Role |
| --- | --- |
| `cmd/anvil-desktop` | Loopback host + optional Chrome `--app` window |
| `internal/desktop` | Catalog, PATH discovery, API origin prefs, reverse-proxy, local delegate |
| `web/desktop` | Vite + React shell (OIDC login, local inventory, two-tool wrapper) |
| `web/desktop/electron` | Optional single-window Electron wrap of the desktop SPA |

## Security

- Listen address must be loopback.
- Prefs store `apiOrigin` only. No bearer tokens, no kube user credentials.
- API origin must be http(s) with a host and without userinfo, path, query, or fragment.
- The host does not persist tokens and does not log `Authorization`.
- `/api/` proxy is allowlisted to `/ui-config.json` and `/api/v1/namespaces/…`.
- Requests with `access_token` / `id_token` / `refresh_token` query params are rejected.
- Version probes and delegates run only catalog binaries resolved on PATH, with constant argument lists.
- Delegate prompts are capped at 64KiB; prompt files are 0600 temp files and deleted.

See `web/desktop/README.md` for run commands. On a display VM:

```bash
make desktop-run
# or: ./hack/run-anvil-desktop.sh --open --detach --api-origin http://127.0.0.1:18080
```
