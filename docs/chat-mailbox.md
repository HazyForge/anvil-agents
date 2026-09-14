# Chat mailbox contract (identity, parking, application key)

Live queue / steer / interrupt is designed in the product map. This file is
the **in-repo contract** that landed first from Agent Substrate teachings:
logical identity, bounded parking (store, do not spawn), and a dedicated
concurrency key. It is not Chat Stop.

`purpose=interactive` on the CRD is a separate slice. This package compares the
purpose string so fire-and-forget stays locked without that merge.

## Rules

- Fire-and-forget purposes (`manual`, `adverseSituation`, `scheduledHealthCheck`,
  `chained`) never receive mailbox rows. Chat labels or a `chat:` application
  key on those runs fail the AgentRun.
- extraEnv cannot set `ANVIL_AGENT_RUN`, `ANVIL_AGENT_RUN_UID`,
  `ANVIL_AGENT_RUN_NAMESPACE`, or `ANVIL_CHAT_*`. The controller writes identity
  from the AgentRun object (this UID, not the profile template).
- `ANVIL_AGENT_RUN_UID` is set on the Job when the run has a UID. Chat env is
  injected only when purpose is interactive.
- An utterance is parked (stored). It is not a new `AgentRun` and not a PATCH
  of prompt onto a live Job.
- `kind=interrupt` is stored. It does not stop a generation until a harness
  adapter hook exists. Built-in `grok`, `codex exec`, and `opencode run` have
  no such hook. Chat must not offer Stop.

Dedicated opaque key for later session create:

```text
applicationRef.name = chat:<namespace>/<council-name>
```

Production scheduled work must not use this prefix.

Package: `internal/chatmailbox`.

## What this is not

- `AgentRunControl` pause/resume is a launch gate.
- Controller `interruptDuplicate` marks overlapping work Failed.
- Chat does not execute `requestPeer` / `interruptDuplicate`.
- Mailbox Postgres, runner TokenReview inbox, and sidecar files are later work.
