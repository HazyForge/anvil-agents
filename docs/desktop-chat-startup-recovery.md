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

## Controller recovery findings

Replacing the Pod alone did not recover dispatch: initial watch caches timed
out after two minutes while the leader lease kept renewing. Primaris uses
Kubernetes v1.36.1. Ordinary list reads (55 Jobs, 214 AgentRuns) were fast, and
an external initial-events watch completed. A direct Helm trial of
`KUBE_FEATURE_WatchListClient=false` restored all 16 workers with sustained
lease renewal. This is a verified operational mitigation; the exact cause of
the in-cluster streaming startup stall remains unproven. Keep leader election
and the existing timeout enabled; no etcd restart or data deletion was needed.

Readiness now uses controller-runtime v0.23.3's native controller warmup and
requires every registered controller's watch sources to sync. Followers warm
caches while reconciliation remains leader-only. Liveness remains independent.

The first retry exposed a separate concurrency isolation bug: an unrelated
release-lab run with conflicting explicit/profile application scopes caused
the global concurrency scan to reject valid Delivery Steward work. The scan
now conservatively counts that run against BOTH possible applications and
allows unrelated applications to proceed. The invalid run's own scope remains
rejected; transient scope lookup failures are still propagated.

Both startup expiry and launch-attempt persistence use optimistic locking.
Regressions cover either side winning that race without resurrecting an expired
turn or launching a duplicate. Additional tests exercise real controller-runtime
warmup, follower fencing, failed sync, shutdown, and conflicting-scope isolation.
