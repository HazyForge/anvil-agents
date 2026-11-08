# Primaris standing enablement (Desktop snappy path)

See also the Kind-local runbook in [`standing-inprocess-harness.md`](./standing-inprocess-harness.md).

> **Retired on 2026-10-27.** Grok is no longer paid, so the Primaris overlay
> dropped every Grok Build surface:
>
> - the `desktop-standing-grok` harness profiles and `desktop-standing-assistant`
>   run profiles in `anvilhub` and `hazy-trade`;
> - the `standing-cli-grok` and `standing-grok-auth-seed` init containers;
> - the `standing-cli` volume and the `anvil-standing-grok-auth` mount;
> - `GROK_HOME`;
> - the `runnerImages.grokBuild` pin.
>
> `runnerImages.grokBuild: ""` makes any Grok Build AgentRun without its own
> image stop as `NeedsHuman`/`GrokBuildImageNotConfigured`. The release pin
> scripts leave an empty runner image empty. Desktop no longer finds a standing
> profile on Primaris; `desktop-agy` is the working cloud chat. The rest of this
> page records the setup as it was.

Standing ProcessBackend + WebSocket is the **primary** interactive Desktop path
on Primaris as well as Kind-local. Jobs remain the default for scouts/batch.
Desktop standing harnesses select `SubstrateActor` so turns record
`executionRuntime: SubstrateActor` and never create a Job.

Until 2026-10-27 the Primaris overlay
(`.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/deploy.yaml`)
set:

| Flag / object | Purpose |
| --- | --- |
| `api.config.standing.liveEnabled: true` | Opt-in gate + claim/`agentruns/status` RBAC |
| `desktop-standing-grok` harness (`execution.runtime: SubstrateActor`) | Selects the standing Substrate plane for Desktop chat |
| `desktop-standing-assistant` profile | Binds that harness for interactive turns |
| API `initContainers` + `standing-cli` emptyDir | Copies `grok` from the reviewed grok-build runner image onto PATH |
| `standing-tmp` / `standing-home` emptyDirs | Writable `/tmp` + `GROK_HOME` under `readOnlyRootFilesystem` |
| API image `distroless/base` (not `static`) | glibc so the mounted `grok` binary can exec |

**Do not** flip `hazy-trade-agent-manager-grok-build` to `InProcess`: that
harness stays on the Job plane for manager/scout work and keeps its dedicated
RWO OAuth volume. Desktop selects `desktop-standing-assistant` (or the
`desktop-standing-grok` harness override) for the snappy path.

### GROK_HOME auth seed (resolved 2026-09-19)

Do **not** mount Secret `anvil-standing-grok-auth` over the whole `.grok`
directory — that made `GROK_HOME` read-only and Grok failed with
`FS_PERMISSION_DENIED`. The live API uses init `standing-grok-auth-seed` to
`mkdir`/`cp` `auth.json` into a writable emptyDir `GROK_HOME`, plus
`standing-cli-grok` for the CLI binary. Never commit auth bytes.

Smoke (2026-09-19 ~3:17 PM CT): thread
`747f8141-7990-4186-bcac-033685878079`, run
`chat-turn-6ac1c73d1f93b04c7c9d4e0ff51adf4de846c48a` → Succeeded,
`executionRuntime: InProcess`, reply `pong-standing`, `StandingClaimed`, no
Job. Cold append→assistant was ~17.6s.

Desktop → Primaris → open `desktop-standing-assistant` (or switch harness to
`desktop-standing-grok`) → Send; expect `path: "standing"` in
`.runtime/chat-latency.jsonl` with measurable `sendToFirstTokenMs` from the
WebSocket stream (not poll-only). Kind-local signed-in samples already exist;
a Primaris Desktop UI line is still the remaining capture.
