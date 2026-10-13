# Native ACP actor adapter

`anvil-actor-acp` can bridge the Anvil actor HTTP endpoint to one persistent
native ACP stdio connection. The existing echo spike remains the default.
This change adds no actor class, chart flags, credentials, Desktop profile,
generation allowlist entry, or deployment.

The adapter completes ACP version 1 initialization once, creates a native
session for each Anvil `sessionId`, and keeps that mapping across prompt
turns. Anvil's current atenet caller sends only `session/prompt`; the adapter
performs native initialization and session setup on its behalf. Explicit HTTP
`initialize` and `session/new` are also supported. Only text prompts and public
`agent_message_chunk` output cross this narrow HTTP surface. Thoughts, tool
traces and user echoes are omitted. Native completion owns the final
`stopReason`; a partial reply followed by an error cannot become success.

HTTP `session/cancel` is a notification forwarded to the active native
session. The prompt stays busy until the harness returns its `cancelled`
completion. A timeout, disconnect, blocked transport write, malformed frame,
or output overflow closes the native connection rather than replaying or
overlapping the unfinished turn. `/readyz` then returns 503. A normal native
RPC error is sanitized and fails that turn without exposing its diagnostics.
The atenet consumer treats `cancelled` as a nontransient failure with no output,
so a cancelled partial reply cannot become a successful AgentRun or retry.
There is no automatic process restart, prompt retry or `session/load`.

Sessions are bounded to 64 per actor process. Requests and native frames are
bounded to 1 MiB, prompt text to 256 KiB, and HTTP output to 512 KiB. Prompt
request IDs are bounded to 128 encoded bytes; terminal frames have reserved
space in the output budget. Prompt timeouts default to two minutes; native session setup is bounded to five
seconds. One prompt per native session is accepted at a time.

The executable, arguments and absolute working directory are operator-owned
CLI settings: `--stdio-command`, repeated `--stdio-arg`, `--workdir`, and
`--turn-timeout`. No HTTP input can choose them or add MCP servers. No client
filesystem/terminal capability is advertised; permission requests receive a
cancelled outcome and other client RPCs are denied. Native harnesses may have
their own capabilities, so this bridge is not a sandbox for an executable.
The child receives only a small workstation environment allowlist, never HTTP
authorization headers, kubeconfig or cluster token environment variables.
Its stderr is discarded and never appears in a public error or reply.

Connecting an actual harness/model requires separate approval and verified
ACP invocation/auth behavior. Existing one-shot Job/ProcessBackend
recipes are not a native ACP session contract. Model selection belongs to
the approved native harness invocation; this adapter does not provision it.
The integration target is harness-neutral native ACP; there is no Grok
dependency. OpenCode documents `opencode acp` as its native JSON-RPC stdio
entrypoint ([OpenCode ACP support](https://opencode.ai/docs/acp/)), making it
a non-Grok candidate after its installed version and existing authorized
model access are verified. This change neither launches it nor installs it.
Codex's existing `exec`/`resume` recipe, OpenClaw, Prime and Antigravity remain
their current Anvil harness paths; no native ACP compatibility is inferred
from those recipes, and no extra adapter/credentials are installed for them.
Desktop `standing-chat` stays on ProcessBackend; the Hazy Trade manager stays
on Job. ATE image/class wiring and Desktop streaming are separate integration
steps. The current `AtenetClient.Generate` aggregates the HTTP stream before
returning; this adapter alone does not change Desktop first-token delivery.

Local verification uses an inert ACP peer over `net.Pipe` and loopback HTTP.
It proves flushing before completion, two prompts on one native session,
busy rejection, cancellation/reuse, failure after partial output, and closed
tool/credential authority. No third-party agent or external model runs.

Protocol references:
[initialization](https://agentclientprotocol.com/protocol/v1/initialization),
[session setup](https://agentclientprotocol.com/protocol/v1/session-setup), and
[prompt turns/cancellation](https://agentclientprotocol.com/protocol/v1/prompt-turn).


## Owned process teardown

Native stdio mode currently requires Linux. The actor is a dedicated supervisor
for one harness, using a private process group and subreaping so detached and
orphaned tool descendants remain owned. Teardown stops deepest descendants before
the harness, waits for owned processes to terminate, and reaps adopted children.
PID start times fence reuse; pre-existing unrelated children are excluded. Client
closure is published only after transport teardown has completed. Kernel teardown
delays retain ownership rather than returning an early completion. Echo mode is
unchanged on other platforms; unsupported native ownership fails before launch.

Regression tests exercise ordinary/detached tool children, context cancellation,
no writes after Close, direct harness reaping, and an unrelated process left alive.
