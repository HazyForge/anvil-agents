---
name: manage-agent-runs
description: Inspect and manage anvil-agents resources with Kubernetes while preserving append-only run history, launch controls, durable volume safety, and Codex auth maintenance.
---

# Manage Agent Runs

Use this skill for `AgentRun`, `AgentRunProfile`, `AgentSchedule`,
`AgentRunControl`, `AgentDataVolume`, `VolumeProfile`, `AgentAuthSession`, and
`AdverseSituation`.

1. Read `docs/agent-run.md`, `docs/cli.md`, and the namespace's repository guidance.
2. Prefer `anvil-agentctl` over raw manifests for run create/list/get/logs/debug
   and for Codex auth maintenance.
3. Inspect the CRD, resource generation, conditions, controller-owned status,
   child Job, payload ConfigMap, pod events, and relevant logs.
4. Treat application references as opaque keys. Do not assume another control
   plane is installed.
5. Create a new AgentRun for new work. Never rewrite a completed run to replay it.
6. Use a profile for durable defaults and a run prompt for one request.
7. Nudge schedules by changing the
   `control.anvil.hazyforge.io/run-now` annotation token.
8. Use `AgentRunControl` to pause future launches. A pause never terminates an
   active Job.
9. Never delete an AgentDataVolume or PVC as a troubleshooting shortcut.
   Storage may expand but not shrink; cross-namespace mounts are invalid.
10. Configure external adverse watches and their read RBAC explicitly. Agent
    responders remain opt-in.
11. Verify resulting status and child-object identity before reporting success.

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
