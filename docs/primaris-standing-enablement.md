# Primaris standing enablement (Desktop snappy path)

See also the Kind-local runbook in [`standing-inprocess-harness.md`](./standing-inprocess-harness.md).

Standing in-process + WebSocket is the **primary** interactive Desktop path on
Primaris as well as Kind-local. Jobs remain the default for scouts/batch;
Substrate/ATE stays optional.

The Primaris overlay
(`.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml`)
now sets:

| Flag / object | Purpose |
| --- | --- |
| `api.config.standing.liveEnabled: true` | Opt-in gate + claim/`agentruns/status` RBAC |
| `desktop-standing-grok` harness (`execution.runtime: InProcess`) | Selects the standing plane for Desktop chat |
| `desktop-standing-assistant` profile | Binds that harness for interactive turns |
| API `initContainers` + `standing-cli` emptyDir | Copies `grok` from the reviewed grok-build runner image onto PATH |
| `standing-tmp` / `standing-home` emptyDirs | Writable `/tmp` + `GROK_HOME` under `readOnlyRootFilesystem` |
| API image `distroless/base` (not `static`) | glibc so the mounted `grok` binary can exec |

**Do not** flip `hazy-trade-agent-manager-grok-build` to `InProcess`: that
harness stays on the Job plane for manager/scout work and keeps its dedicated
RWO OAuth volume. Desktop selects `desktop-standing-assistant` (or the
`desktop-standing-grok` harness override) for the snappy path.

### Remaining Primaris blocker: GROK_HOME auth seed

The init container copies the CLI only — it never copies credentials. Until
`$GROK_HOME/auth.json` is present in the API pod's `standing-home` volume,
`ProcessBackend` turns fail closed as `InProcessNotWired` (no Job). Seed
options (pick one; never commit auth bytes):

1. Project an operator-owned Secret key to
   `/tmp/anvil-standing-home/.grok/auth.json` via `api.extraVolumes` (preferred
   for the API process; keep it off the manager RWO claim).
2. One-shot `kubectl cp` of a workstation `~/.grok/auth.json` into the live
   API pod path (ephemeral across restarts — use only for a canary).

After seed: Desktop → Primaris → open `desktop-standing-assistant` (or switch
harness to `desktop-standing-grok`) → Send; expect
`path: "standing"` in `.runtime/chat-latency.jsonl` with measurable
`sendToFirstTokenMs`.
