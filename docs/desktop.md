# Anvil Agents Desktop

`anvil-desktop` is the local operator surface for workstation harness CLIs
already on a machine and for connecting those clients to a Kubernetes
kubecontext that runs the anvil-agents operator.

It is **not** a second Anvil Agents Console. Cluster observation, composition
library, standing chat, and future per-council chat stay in `web/console`
and are opened as a top-level window. The API's
`Content-Security-Policy: frame-ancestors 'none'` is preserved.

## Why a desktop host

The console is served by `anvil-agents-api` over OIDC. It cannot see the
operator's laptop PATH or kubeconfig. Desktop work that the cluster SPA cannot
do:

- Discover Codex, xAI/Grok, OpenClaw, OpenCode, Hermes, Pi, and similar CLIs
- Show which kubecontexts exist and whether `control.anvil.hazyforge.io/v1alpha1` is installed
- Point `anvil-agentctl` at that context for append-only runs and durable-home auth
- Wrap the existing console without duplicating its screens

Local CLIs are **not** the cluster harness. AgentRuns still use runner images
selected by `AgentHarnessProfile`. The desktop maps a workstation binary to a
backend kind so an operator can diagnose auth (`anvil-agentctl auth codex|grok`)
and then watch the run in the console.

## Layout

| Path | Role |
| --- | --- |
| `cmd/anvil-desktop` | Loopback host + optional Chrome `--app` window |
| `internal/desktop` | Catalog, PATH discovery, kubeconfig, prefs, HTTP |
| `web/desktop` | Vite + React shell (same visual language as the console) |
| `web/desktop/electron` | Optional Electron wrapper that loads the host and opens the console in a second window |

## Chat alignment

- Standing chat is a console + API feature (`/chat` when enabled). The desktop
  Chat page deep-links there once a console origin is saved.
- Per-council chat remains a later design. The desktop does not block on it
  and does not invent a Conversation CRD.

## Security

- Listen address must be loopback.
- Prefs store kubecontext name and console origin only. No bearer tokens, no
  kube user credentials.
- Console URLs cannot include userinfo, query strings, or fragments.
- Version probes run only catalog binaries resolved on PATH, with constant
  argument lists and a two-second timeout.

See `web/desktop/README.md` for run commands.
