# Desktop conversation feedback

The operator wants to chat naturally, send several related or unrelated messages,
and let a selected coordinator decide where work belongs. This change addresses
the immediate feedback problem: a remote turn must visibly acknowledge delivery
and report observed activity while the runner starts and executes.

## UI review

| Severity and location | Before | After | Why |
| --- | --- | --- | --- |
| HIGH, `EntityChatPage.tsx` send path | No user bubble until the HTTP receipt returned | Optimistic user bubble with saving/sending/unconfirmed state; persisted request ID reconciles against receipts | A slow request looks active without claiming delivery before confirmation |
| HIGH, remote conversation activity | Generic queued/running status; useful events hidden behind logs | Public runner preparation, harness/tool activity, quiet periods, transport recovery, completion and failures | Users can see what the system actually reports |
| MEDIUM, conversation navigation | Draft and selected conversation lost across navigation/reload | Tab-local drafts and selection restored per namespace/conversation | Switching conversations preserves unfinished thoughts |
| MEDIUM, new conversation defaults | Alphabetically first profile selected automatically | Explicit agent/harness choice, with the user's new-conversation choice remembered | Prevent accidental selection of an unconfigured profile |
| MEDIUM, native Codex failure | Only Kubernetes Job backoff failure shown | Recognized terminal provider 401 gets canned authentication guidance | Identify the actionable failure without copying sensitive native diagnostics |
| MEDIUM, chat layout | Small monospaced conversation text and narrow column | 15px conversation text, wider layout, collapsed existing settings | Make conversation and activity readable on desktop |

Activity labels use known runner markers and public native event types. The feed
does not copy commands, arguments, results, reasoning, raw errors or model replies
from log lines. Unknown event shapes stay in the optional raw runner view. It
does not simulate progress or promise that a silent runner is doing a particular
task. Recognized native activity currently covers Codex, OpenCode, Grok and AGY;
other harnesses retain shared runner lifecycle feedback.

Initial AgentRun 404s retry while a turn is active. Terminal status remains
authoritative when historical log tails arrive afterward. Replayed older log
timestamps and stable event keys prevent repeated activity. A bounded stream
reset closes the old connection before reconnecting. Raw logs subscribe only
when expanded.

## Product direction and remaining limits

The existing manager's selected harness makes delegation decisions; there is no
separate hidden routing model. Each peer executes its own configured harness.
The current Desktop test profiles use OpenCode; AGY is also available as an
explicit remote harness.

This remains one active turn per conversation/executor. Users can draft a follow-up
while it works, but cannot yet submit a burst into a durable coordinator inbox.
Peer completion appears as a receipt and can be opened; it does not automatically
wake the manager to synthesize a final answer. A natural notes-app request during
this review exposed that gap: the manager promised to return after asking the
reviewer, but no second coordinator turn was scheduled.

The next cohesive feature needs a durable project/channel inbox separate from
execution locks; coordinator decisions to attach, revise, create or cancel work;
a work ledger for overlap detection; and exactly-once worker completion messages
back to the coordinator. Preserve append-only AgentRuns and writable-home locks.
Do not represent simple FIFO queuing as semantic coordination or history replay
as native session steering.

Related product references: [Cursor Projects](https://cursor.com/changelog/projects)
describes a project coordinator and shared context;
[Grok Bot](https://x.ai/news/designing-grok-bot) describes persistent bot identity
and shared conversations; the [Codex app](https://openai.com/index/introducing-the-codex-app/)
organizes parallel agent work by project. These inform the intended workflow,
not a claim of equivalent functionality today.

## Validation and rollout

`make verify` passed, along with all seven native activity parser tests and all
12 isolated browser scenarios. Browser coverage includes a slow create/send,
lost accepted receipts with retries, draft/selection restoration, namespace
isolation, denied-thread recovery, initial stream 404 recovery, public tool
labels, terminal snapshots followed by log replay, and opening raw logs only
on demand. Chart renders passed with API disabled and the complete Primaris
API configuration enabled.

Actual Chrome DevTools MCP drove the signed-in Desktop against Primaris:

- Thread `a67e3a29-f319-4b02-bc13-08a6c8f6d2bd` created run
  `chat-turn-da6bc9534d603b9c3fc984092aff1959e3e90c76`.
- Immediate UI feedback showed `Saving conversation…`; the live stream showed
  preparation, tool checks, `Starting harness`, `Composing answer`, and completion.
- The message was accepted at 19:10:12 UTC, harness started around 19:10:20,
  and the assistant reply was persisted at 19:10:42 (about 30 seconds total).
- The observe-only OpenCode profile declined the requested read-only shell
  time check under its configured instructions. This verifies a real model
  reply and lifecycle events, not live execution of a tool. Native tool labels
  were independently checked with isolated fixtures.
- An unsent draft survived switching to the AGY conversation, returning, and
  reloading. The reload assertion initially ran during auth initialization;
  waiting for the actual conversation confirmed restoration. Only the test
  draft was then cleared through the visible composer.
- Visual inspection confirmed 15px conversation text and visible activity.

The natural manager test was thread `994698be-8cca-42b5-b651-ea39a8f1fa21`,
run `chat-turn-fd573d4317b95315ba3d98cc4b65f6440e083e42`. Its reviewer completed
run `chat-turn-b1f097e65c5155974bdde41f1bd31a3f82160e06`, but the main thread
still had only its initial promise to return. This is direct evidence of the
automatic return gap described above.

Source change `9b8cd7d` and image pin `56d65e0` were pushed to master with the
operator's authorized bypass. Helm revision 13 is deployed with controller/API
digest `sha256:4ecb770c90f0eaa3286032cc7ae534431ca4396d99acbcb050f93d2b87fe38a5`.
Both deployments are 1/1 ready, health/readiness return 200, and the persisted
chat remains readable after rollout. Argo automated sync remains unset. The
local Desktop UI assets were updated in its existing cache without replacing
preferences or credentials. Existing historical Codex failure receipts are
unchanged; the new canned guidance is covered by backend tests and applies
when a future failed turn is reconciled.

Review verdict: Approve the scoped activity/draft improvement. The complete
project coordinator workflow still has HIGH product gaps in its inbox and
automatic return of peer results.

Block
