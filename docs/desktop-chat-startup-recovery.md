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
  The later deterministic-recovery extension below also exposes an explicit
  saved-message retry for other terminal failures; these never auto-replay.

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

## Applied rollout

Direct Helm release `anvil-agents-system-chart` revision 23 pins controller/API
`sha256:4997edcd572a69da75722dfa7492cb55a300ac9f6d4e653c7dccdc494c368677`.
Source changes were pushed to master through `4b27f07`; no GitHub Actions or
Argo reconciliation was used for the deployment. `make verify` passed with the
local Go/Node toolchains on PATH and `GOFLAGS=-buildvcs=false` for local build
validation; the canonical image builder supplies image source metadata.

The original abandoned run and first retry
`chat-turn-57a8ee9f4eb19184527868b4b477a0df4d958642` are terminal failures with
zero owned Jobs. A second browser click of **Retry last message** created
`chat-turn-182b359ea8137ba3ffdc1cc112eced4be457a9d6` at 14:16:01Z. Its runner
container started on node `z400` at 14:16:20Z. The exact existing draft remained
in the composer throughout reload and retries. No unsent draft was submitted.

The final retry succeeded at 14:19:13Z (192 seconds after creation). Browser
reload showed the saved assistant answer, **Reply received**, an enabled Send
button and the untouched draft. The completed-turn reasoning/runner panel
opened with retained output. Activity recorded repository preparation starting
14:16:22Z, tool setup 14:17:08Z–14:18:45Z, and harness execution
14:18:45Z–14:19:10Z. This repairs abandoned dispatch and recovery, not the
remaining per-turn setup cost or persistent native-session requirement.

Final UI wording distinguishes an unstarted queue from a known running runner
still preparing tools. Both become amber when delayed; setup activity is not
misrepresented as an unknown launch. Helper tests, final `make verify`, and
live reload/draft/send/debug-panel checks passed. Earlier 21 browser regression
scenarios passed for the full recovery flow; independent review found no
blocking issues.

## Deterministic startup recovery

Accepted chat turns now retain one user message while retrying a controller-
verified no-launch failure. The only automatic replay allowlist is a failed
`ChatStartupDeadlineExceeded` condition for the current resource generation,
with no Job creation attempt, Job reference/UID, Pod reference/UID, or start
receipt. Provider failures, tool output, missing replies, policy rejections,
and ambiguous execution are not automatic replay evidence.

The durable turn keeps its request ID, frozen prompt, profile/harness, scope,
and resource locks. Each retry creates a fresh append-only AgentRun under a
deterministic attempt name. PostgreSQL atomically advances the attempt and
retains previous run receipts; compare-and-swap guards reject stale completion
and UID writers. Two retries use persisted 10-second and 30-second backoffs.
Restarting the API or closing Desktop does not reset the budget or duplicate
the original message. Previously terminal turns stay terminal.

Desktop shows the retry countdown/attempt number, preserves the unsent draft,
and rejects responses from an older attempt even when their execution status
looks more advanced. Earlier startup attempts remain inspectable. Terminal
failures expose an explicit Retry last message action that sends the saved
message with a fresh request ID while retaining a separate composer draft.
This explicit retry is distinct from automatic no-launch recovery.

The Hazy Trade code-slop incident was a runner credential-bootstrap rejection
before inference: repository publication rules no longer matched the write
broker's prerequisites. Repeating that setup cannot repair policy drift.
Its separately owned runner overlay now probes with read-only credentials and
keeps ChatThread conversations available through a read-only broker when
publication prerequisites cannot be established. The native prompt and public
activity explain the reduced GitHub capability; scheduled write lanes retain
their existing checks. No model, persona, branch rule or credential is changed
by this recovery path.

| Before | After | Why |
| --- | --- | --- |
| Generic failed Job ends every chat turn | Retry only proven unlaunched work, with bounded durable attempts | Recover transport/startup failures without replaying tool effects |
| Retry progress could look like stale running state | Compare attempt number before execution phase | Keeps the visible recovery state forward through delayed responses |
| Other terminal failures require retyping | Explicit saved-message retry preserves the unsent draft | Lets the operator retry after fixing configuration |

Verification includes package race tests, actual PostgreSQL 17 retry/concurrency
integration, 24 Desktop browser scenarios, and repository `make verify`.
Fixture tests establish recovery behavior; a native runner reply remains the
required evidence for a live harness repair.


## Native OpenClaw recovery

After the GitHub bootstrap repair, a browser retry reached native OpenClaw and
failed because xAI rejected its saved refresh token. The API now recognizes the
exact terminal native failure and shows **OpenClaw authentication required for
xAI**. Historical failures use the same verified Job/Pod log boundary. Arbitrary
JSON, earlier retry diagnostics, other providers and embedded private strings
cannot supply this classification. An authentication failure does not authorize
automatic replay.

The existing OpenClaw xAI login was renewed in a bounded temporary Helm Job on
the agent's dedicated PVC, with native device authorization in the owner's
browser. The temporary writer was removed before retry. No Grok Build auth was
copied, no API key substituted, and the selected agent remains OpenClaw with
`xai/grok-4.5`. The native login's global default change was restored.

Browser retry `chat-turn-333441c79e2202e9c0bebc7707988e66250eff3e` completed
Succeeded at 16:33:56Z. Its owned native output confirms xAI/Grok4.5 without
fallback and identifies the code-slop auditor, including its current read-only
GitHub limitation. This is provider proof, distinct from correct reply rendering.

During the subsequent API rollout, new controller cache LISTs encountered
HTTP/2 stream resets while the old healthy controller retained its lease.
`DISABLE_HTTP2=true` was applied only to the controller through the Primaris
Helm overlay, retaining `KUBE_FEATURE_WatchListClient=false`, cache warmup and
leader fencing. Helm revision26 completed with the new controller ready, zero
restarts and a renewing lease. This mitigates the observed transport failure;
it does not establish why the Kubernetes API reset those connections.
