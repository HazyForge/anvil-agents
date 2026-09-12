# Mid-run collaboration (status JSON only)

While your AgentRun Job is still **Running**, you may judge that another agent
should help or that a duplicate run should stop. The controller owns all
AgentRun creation and interruption. **Do not** call the OIDC API, `kubectl`, or
any HTTP endpoint to create or stop runs.

Emit a **decision** through `ANVIL_AGENT_RUN_STATUS_TOOL` (or the status file).
The controller watches `ANVIL_AGENT_RUN_STATUS_JSON` log lines and honors
these actions once per unique request:

## Request a peer (`action`: `requestPeer`)

Use when you need a specialist or parallel helper and continuing alone is wrong.

Required fields:

- `type`: `decision`
- `action`: `requestPeer`
- `summary`: short operator-visible reason
- `peerPrompt`: the peer's task prompt (required)

Optional:

- `peerProfileName`: AgentRunProfile for the peer (defaults to your run's profile)

Example:

```json
{"type":"decision","action":"requestPeer","summary":"Need a reviewer for the API change.","peerProfileName":"grok-reviewer","peerPrompt":"Review the mid-run collaboration API diff for correctness."}
```

Shell helper:

```sh
anvil-agentctl self report decision \
  --action requestPeer \
  --summary "Need a reviewer." \
  --peer-profile-name grok-reviewer \
  --peer-prompt "Review the API diff."
```

## Interrupt duplicate work (`action`: `interruptDuplicate`)

Use when another **AgentRun** in the same namespace is doing the same work and
should stop so effort is not wasted.

Required fields:

- `type`: `decision`
- `action`: `interruptDuplicate`
- `summary`: short operator-visible reason
- `duplicateRunName`: exact AgentRun name to interrupt

Example:

```json
{"type":"decision","action":"interruptDuplicate","summary":"Peer already fixing the same regression.","duplicateRunName":"console-card-abc12"}
```

Shell helper:

```sh
anvil-agentctl self report decision \
  --action interruptDuplicate \
  --summary "Duplicate work on same issue." \
  --duplicate-run-name console-card-abc12
```

Keep collaborating only through status JSON. Do not spawn peers yourself.
