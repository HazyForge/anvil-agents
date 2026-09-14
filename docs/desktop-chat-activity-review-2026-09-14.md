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

Review verdict: Approve the scoped activity/draft improvement after validation.
Block a claim that the complete project coordinator workflow is finished: the
inbox and automatic return of peer results remain HIGH product gaps.
