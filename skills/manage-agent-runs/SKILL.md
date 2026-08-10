---
name: manage-agent-runs
description: Inspect and manage anvil-agents resources with Kubernetes (anvil-agentctl) while preserving append-only run history, launch controls, durable volume safety, and Codex auth maintenance. Not the Primaris Hub anvilctl agent path.
---

# Manage Agent Runs

Use this skill for `AgentRun`, `AgentRunProfile`, `AgentSchedule`,
`AgentRunControl`, `AgentDataVolume`, `VolumeProfile`, `AgentAuthSession`, and
`AdverseSituation`.

## CLI ownership

| Need | Command |
| --- | --- |
| Pause/resume application launches | `anvil-agentctl control pause\|resume` |
| List/suspend/resume/nudge schedules | `anvil-agentctl schedule …` |
| Create/list/logs/debug runs | `anvil-agentctl run …` |
| Durable home reauth | `anvil-agentctl auth …` |
| Manager Hub mutations (Primaris product only) | `anvilctl agent --hub-url …` (separate skill) |

This skill is the **public runtime** path. Do not use private Primaris
`anvilctl agent` for kube-backed pause, schedule suspend, or inventory without
Hub.

1. Read `docs/agent-run.md`, `docs/cli.md`, and the namespace's repository guidance.
2. Prefer `anvil-agentctl` over raw manifests for run create/list/get/logs/debug,
   Codex auth maintenance, `AgentRunControl` launch gates
   (`anvil-agentctl control list|get|pause|resume`), and schedule ops
   (`anvil-agentctl schedule list|get|suspend|resume|run-now`).
3. Inspect the CRD, resource generation, conditions, controller-owned status,
   child Job, payload ConfigMap, pod events, and relevant logs.
4. Treat application references as opaque keys. Do not assume another control
   plane is installed.
5. Create a new AgentRun for new work. Never rewrite a completed run to replay it.
6. Use a profile for durable defaults and a run prompt for one request.
7. Nudge schedules with `anvil-agentctl schedule run-now NAME` (sets the
   `control.anvil.hazyforge.io/run-now` annotation token). Optionally pass
   `--template` for a named run template.
8. Use `AgentRunControl` to pause future launches (`anvil-agentctl control
   pause --application APP --reason TEXT --for 4h`). A pause never terminates
   an active Job. Record the concrete reason, a bounded `spec.expiresAt`, and
   the immutable event or directive ID in `spec.source`; never renew a pause
   from an unchanged event. Resume only the issuing authority's named control
   (`control resume`); `--all-controls` is a human-only break-glass action.
9. Suspend or resume individual schedules with
   `anvil-agentctl schedule suspend|resume NAME --reason TEXT` when the schedule
   object itself must stop; that is independent of application launch holds.
10. Never delete an AgentDataVolume or PVC as a troubleshooting shortcut.
    Storage may expand but not shrink; cross-namespace mounts are invalid.
11. Configure external adverse watches and their read RBAC explicitly. Agent
    responders remain opt-in.
12. Verify resulting status and child-object identity before reporting success.

## In-pod reporting

Inside the current AgentRun Job, record progress with the shipped binary:

```bash
anvil-agentctl self report progress --stage tool-setup --summary "Tools ready."
anvil-agentctl self report needsHuman --stage harness-auth --summary "Codex auth expired; operator reauth required."
```

Do not patch `AgentRun/status` directly. Do not treat binary presence as cluster
authority.

## Provider 401 / durable home reauth (operator only)

When Codex (OpenAI) or Grok Build (xAI) Jobs fail with `401` / refresh failures
against a durable home:

**OpenAI Codex**

1. `codex login --device-auth`
2. `anvil-agentctl auth codex diagnose -n <ns> --data-volume <volume> [--bootstrap-secret <seed>] --auth-file ~/.codex/auth.json`
3. `anvil-agentctl auth openai reauth -n <ns> --data-volume <volume> --auth-file ~/.codex/auth.json [--bootstrap-secret <cli-owned-seed>]`

**xAI Grok**

1. Refresh local `~/.grok/auth.json` (Grok/xAI OAuth offline).
2. `anvil-agentctl auth grok diagnose -n <ns> --data-volume <volume> [--bootstrap-secret <seed>] --auth-file ~/.grok/auth.json`
3. `anvil-agentctl auth xai reauth -n <ns> --data-volume <volume> --auth-file ~/.grok/auth.json [--bootstrap-secret <cli-owned-seed>]`

Shared rules:

1. Wait for the `AgentAuthSession` to Succeeded. Active consumers of the volume
   must finish first; new runs stay Pending with `AuthSessionActive`.
2. Do not patch ExternalSecret-managed Secrets; use a CLI-owned seed Secret or
   omit bootstrap update and only rewrite durable `auth.json`.
3. For pure apiKey mode (`XAI_API_KEY` / provider keys in `envSecretRefs`), update
   the Secret instead of durable OAuth reauth.
4. Never delete the PVC to "fix" auth.

Use normal `kubectl get`, `kubectl describe`, and read-only log commands for
inspection. Any mutation must follow the caller's Kubernetes authorization and
the resource's declared intent.
