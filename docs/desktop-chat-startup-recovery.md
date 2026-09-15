# Queued chat startup recovery

On 2026-09-15 the owner reported Delivery Steward's `what do yo do ?` message
waiting for 73 minutes with the send button blocked. The accepted run was
`chat-turn-4b741da1c2910f1bc85c4a303d966ba9cade2898`, created at 12:32:23Z,
with no status or runner. The controller was crash-looping after losing its
leader-election lease during Kubernetes API request timeouts. The API process
remained running. Direct etcd checks found no alarms, corruption, disk pressure,
or lost quorum; this evidence does not justify restarting or deleting etcd data.

## Behavior

- A chat AgentRun that has not established an execution receipt expires after
  five minutes from Kubernetes creation. Deferred inbox time is separate.
  Before expiring, the controller checks Jobs by ownership through its uncached
  reader, including unlabelled and deleting Jobs, and uses an optimistic status
  patch to avoid racing a recorded launch. Existing Jobs, launch attempts,
  running work and terminal records remain protected. Recovery must not launch
  an old expired greeting merely because the controller returns later.
- Desktop distinguishes ordinary waiting, a delayed start, and a running turn
  without recent activity. A two-minute queue or unavailable progress check is
  amber rather than a green working indicator. It does not invent a terminal
  outcome while execution status is unknown.
- Thread reads bound foreground execution recovery to three seconds, then
  return durable messages and turns with `recoveryPending=true` if Kubernetes
  progress cannot be refreshed. Background recovery and execution locks remain
  active. No additional Kubernetes privileges are granted.
- A confirmed startup expiry offers **Retry last message**. It selects the
  original user message by the server's message ID and creates a fresh request.
  Lost response/reload recovery reuses that request ID. The current composer
  draft is preserved, including when it has the same content as the retry.
  Arbitrary failures with possible prior side effects do not receive this action.

These changes do not implement persistent native sessions, automatic peer inbox
wakeups, or cancellation of an already launched harness. Reasoning and retained
runner output remain available separately from verified final replies.

## Validation

`make verify`, focused controller and API recovery regressions, five UI helper
tests and all 21 remote-chat browser scenarios passed. Tests cover existing Job
and launch-receipt races, legacy/mismatched/non-chat identity, terminal stickiness,
API outage with preserved history and locks, delayed queue presentation,
confirmed-expiry retry, lost-response idempotency, and retained composer drafts.
