# Chat delivery contract

Only `spec.purpose=interactive` AgentRuns participate in a chat mailbox
(queue / steer / interrupt). Fire-and-forget purposes
(`manual`, `adverseSituation`, `scheduledHealthCheck`, `chained`) never receive
queued, steered, or interrupted chat lines.

`AgentRun` stays append-only. New intent creates a new run. The controller does
not PATCH prompt onto a live Job and does not watch Postgres.

## Public create

`anvil-agentctl run create` and OIDC `POST /agent-runs` accept `manual`,
`adverseSituation`, and `scheduledHealthCheck`. They reject:

- `chained` — AgentChain children only
- `interactive` — reserved for the chat session API (not implemented yet)

Kubernetes RBAC can still apply an interactive `AgentRun` so that later session
create can use the same CRD. Schedules, chains, and adverse responders must not
set this purpose.

Create-time identity labels (immutable, never patched onto a live Job):

- `control.anvil.hazyforge.io/chat-thread`
- `control.anvil.hazyforge.io/chat-session`
- `control.anvil.hazyforge.io/council`
- `control.anvil.hazyforge.io/council-role`

## What this is not

- `AgentRunControl` pause/resume is a launch gate. It does not stop a live
  thought.
- Controller `interruptDuplicate` marks overlapping work Failed. It is not
  “stop talking and hear me.”
- Built-in runner images (`grok`, `codex exec`, `opencode run`) are one-shot
  CLIs. They have no generation-interrupt hook. Do not expose a Chat Stop
  control that claims they do.

Mailbox schema, runner TokenReview inbox, sidecar files, and per-adapter
interrupt hooks are later work. See the product map for queue / steer /
interrupt components.
