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
the approved native harness invocation. The fixed selection gate below verifies
and, when advertised, explicitly selects that existing model and mode for the
native session; it does not provision accounts, keys or provider access.
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


## Fixed model and mode selection

For an authorized native model run, supply both `--expected-model=provider/model`
and `--expected-mode=mode`. They are exact, case-sensitive operator values; HTTP
input cannot override them. `--catalog-ready-timeout` defaults to five seconds
and is bounded to at most thirty seconds. Omitting both preserves the inert and
existing unpinned contract; supplying only one is rejected.

The selection deadline covers queueing, native session creation, idle catalog
updates and explicit selection acknowledgments. A `session/new` result or
`config_option_update` must advertise both required values. If a required value
is not yet advertised, the adapter waits within that deadline without issuing
a prompt or a polling RPC. When an advertised value is not selected, it sends
`session/set_config_option` with the fixed operator value and checks the full
acknowledgment directly from its correlated RPC response. Missing, malformed,
rejected or wrong acknowledgments fail closed; a later catalog notification cannot
repair that response. The latest observed catalog and cancellation are checked again at the native
write boundary before every prompt. There is no provider/model fallback, native
process restart or prompt replay.

During an active prompt, each ordered `config_option_update` must still prove the
required model and mode. Missing/malformed metadata or observed selection drift
fails the turn and closes/reaps the owned connection. Later restoration cannot
erase that violation. Subsequent output is suppressed; a previously streamed
partial reply cannot become a successful AgentRun. Completion also checks the
latest observed catalog. This is observed ACP selection proof, not independent
attestation of a provider's actual billed model.

Cancellation during constrained readiness stops the wait promptly and releases
the rejected native session through the advertised `session/close` surface. A
harness without that close capability is closed and reaped instead. Existing
unpinned setup cancellation keeps its earlier resumable-session behavior.
The transport health endpoint alone does not certify model/mode selection.

OpenCode 2.0.15 can cache its first successful per-directory ACP catalog before
configuration activation finishes. A later ordinary CLI metadata read can expose
the configured OpenAI model while that ACP catalog still lacks it. This gate
rejects or times out on that stale catalog; it does not fabricate readiness or
work around it by generating a model turn. Native catalog readiness/refresh must
be confirmed before any live pilot. Credentials, entitlement and cost are not
proved by a catalog entry or a successful selection acknowledgment.

`python3 hack/acp-selection-e2e/run.py` builds the real API/standing fixture and
actor binaries with inert stdio peers. It proves delayed catalog selection, the
automatic correlated A-to-B-to-A return, wrong model/mode acknowledgments,
timeout and cancellation with zero peer prompts on rejected setup paths. Invalid
acknowledgments followed immediately by correct notifications still dispatch zero
peer prompts. Active model/mode drift, later restoration, malformed/missing
catalog updates and failure after partial output produce one failed peer turn,
one correlated failure explanation and no unverified reply. Set
`ANVIL_SELECTION_EVIDENCE_DIR` outside the temporary runtime to retain receipts.
`--parent-control` uses an unchanged `22d19b76` overlay and proves that the earlier
adapter dispatched one wrong-provider peer prompt where the new assertions
require zero. It reports this reproduced defect as a passing control, not as a
successful corrected behavior or a real harness/model run.
