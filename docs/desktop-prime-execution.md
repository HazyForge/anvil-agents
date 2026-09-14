# Prime Agent and Desktop execution location

Desktop Chat defaults to **Local**, with WSL status inside Agent settings and
**Prime Agent** selected. An explicit saved Primaris selection remains respected.
The location selector appears above the conversation. The local path works
without remote OIDC sign-in; Primaris conversations still require it.

| Location | Harness process | Model and login | Files | History |
| --- | --- | --- | --- | --- |
| Local | Installed catalog CLI on the Desktop host or selected WSL distro | Native harness configuration and provider login | Persistent local working folder, editable for a new conversation | Browser storage on this device, replayed into later turns |
| Primaris | AgentRun Job in Kubernetes; Desktop profiles select home-lab nodes | Selected AgentHarnessProfile and existing Secret references | Temporary `/workspace` unless the profile mounts an AgentDataVolume | Durable API messages, replayed into later AgentRuns |

When the Desktop wrapper is itself running in WSL, native process execution is
already inside WSL. A Windows-hosted wrapper uses `wsl.exe` and the chosen distro.
Local execution removes Job scheduling/container startup, but provider latency,
reasoning effort, and tool execution still take time. Neither path currently
keeps a provider-native conversational process warm between human messages.

## Prime runtime

Prime is a distinct `primeAgent` backend, not an alias for Pi. The runner uses
the checksummed official Prime Agent 0.9.4 archive, its native JSON event stream,
and its Python/IPython tools. Its image installs the bundled Python runtime and
skills during the build, then verifies actual native kernel file and shell
operations as UID 10001. Installing only the CLI would leave required tool
dependencies unavailable.

The `desktop-prime` remote harness selects `deepseek/deepseek-v4-flash`, high
thinking, matching the operator's inspected local default. It declares the existing
`deepseek-credentials` reference. Live verification found that its ExternalSecret
cannot synchronize because upstream `secret/deepseek-api-key` is missing; the
remote Prime canary is blocked until that provider credential is restored. The runner maps its declared generic `apiKey`
to the native `DEEPSEEK_API_KEY` environment variable without logging values.
No local Prime auth file is copied. The existing Desktop runtime identity and
Kubernetes permissions remain unchanged.

Local execution invokes `prime-agent --print --mode json --no-session`, leaving
the user's native provider/model and tool configuration in control. Other
installed catalog harnesses can be selected for a new local conversation.

## Delivery and activity contract

Local Chat sends JSON to the loopback wrapper's `/local/v1/chat/stream` endpoint.
The endpoint requires a loopback Host, same-origin browser request and JSON
content type. Desktop OIDC tokens are not forwarded to native/WSL processes.
Working paths are passed as data; no user path or prompt becomes shell code.
Default local workspace creation uses private directory permissions.

The UI immediately adds the user's message, then consumes `started`, bounded
JSONL `stdout`, and `result`/`error` events. Known public events become short
activity labels, including `Running Python`; reasoning, command arguments,
tool results and raw stderr are not rendered as activity or assistant replies.
Prime replies come only from native terminal assistant text blocks. Long turns
retain a bounded stream tail so the initial stdout capture limit does not drop
the final reply.

Navigation controls that would unmount an active local turn are disabled.
Disconnects/timeouts terminate the native process group on Unix. Windows WSL
execution owns a Linux process group and uses a separate bounded cleanup command
to confirm termination. Overlapping turns in the same resolved working folder
are rejected; unconfirmed WSL cleanup quarantines that folder until recovery. An
unconfirmed local turn restored after reload is labeled as unconfirmed and is
never automatically replayed. New execution remains blocked until the user
explicitly confirms the earlier work has stopped. A changed execution target
or distro also requires deliberate folder rebinding. This does not provide durable local execution
or exactly-once execution across browser/process crashes.

Drafting a follow-up during work is supported. Sending bursts into a durable
coordinator inbox, automatically incorporating peer completion in the manager
conversation, and sharing project state between local and remote workers remain
separate work. Signed-in local conversations receive the scoped Anvil tools
described below; this does not create a durable coordinator inbox.

## Runtime evidence, 2026-09-14

Direct Helm revision 15 deployed controller/API
`sha256:db6da8c867b4c873b5438539413e1cdda49aa9cae95019d88a530b052bc841af`
and Prime runner
`sha256:4313de2661a1c3fa94832eaafe91acff7895e3c80584e264bc027016d2489222`.
Both deployments became ready. This is deployment evidence, not a successful
remote Prime model or tool turn: the remote canary cannot start without the
missing upstream DeepSeek credential described above.

The real Desktop local canary launched installed Prime Agent in WSL and reported
agent/turn start, but reached the 300-second Desktop deadline without creating
its requested file. A bounded 60-second connection diagnostic reached a native
assistant `message_start` for DeepSeek V4 Flash, then timed out with no text,
thinking-delta, or tool events. Native source emits this event after the provider
response opens. This establishes a wait for provider stream content; the exact
upstream cause remains unverified. The native container Python kernel did pass
a real file write/read and shell check independently of the model.

The operator explicitly chose to retain local harness defaults, including high
thinking. Desktop does not change the native model, reasoning, or login settings.
An earlier scoped low-thinking diagnostic also timed out and did not alter the
saved configuration. No successful local Prime model/tool turn is claimed.

The local WSL route separately passed a real Codex tool canary: it wrote
`wsl-execution-check.txt`, read `ANVIL-WSL-TOOLS-9182` back and ran `date -u`
(`Mon Sep 14 20:03:33 UTC 2026`). This used the installed native Codex
configuration without changing Prime defaults. Local chat now specifically
requests native JSON events for Codex, Grok and OpenCode while preserving the
legacy delegate endpoint's output contract.

Because remote Prime lacks its upstream credential, the remote
`desktop-assistant` profile retains the tools-capable OpenCode configuration.
Prime remains an explicit remote choice and the local Desktop default.

## Local agent access to Anvil

Local describes execution location. It does not mean the agent is disconnected
from Primaris. With Anvil tools enabled and a valid Desktop sign-in, each local
turn receives an Anvil assistant role and a short-lived tool connection scoped
to the selected namespace. No provider/model/thinking setting is overridden.

The tools can list agent and harness profiles, inspect runs and standing chats,
start append-only runs from profiles, and send messages to an agent's standing
conversation. Both writes require stable request IDs. The agent is instructed to
inspect current work before delegating and to distinguish acceptance from actual
completion. These tools do not edit agent configuration, interrupt runs, read
Secrets, acquire Kubernetes credentials, or send messages to humans.

The browser sends a refreshed access token only to the local Desktop wrapper.
The wrapper retains it in memory for the turn and calls fixed AgentRun API paths
under existing authorization. Native harnesses receive a command invoking
`anvil-desktop agent-tool --session-file ...`; its private file contains only a
random, revocable loopback capability and expiry. It has no OIDC token. The tool
client rejects external destinations, proxies and redirects. Completion or
cancellation revokes the capability, cancels active requests and removes the
private file. API responses remain untrusted data.

The UI says Local, with WSL status inside Agent settings. Anvil tools enabled
means the signed-in connection is configured for the next turn; Anvil connected
appears after the wrapper confirms that turn's tool connection. Tool activity
uses allowlisted labels without exposing commands, credentials or API bodies.

## Connected-agent delivery, 2026-09-14

Direct Helm revision **16** deployed the canonical-agent API from source
`3de461e`, with controller/API digest
`sha256:364f74c34c5c08bb0ab6a73cd5b46f5ccab8122b44a64b6e69be49fc29121cca`.
Both deployments are 1/1 ready and public health/readiness return 200. The remote
Desktop assistant is back on `desktop-opencode-workspace`; the Prime runner pin
is unchanged. Source and pin landed directly on master with Actions skipped.
The local wrapper additionally includes `259f208` for operation deadlines and
stdin-based tool invocation; these local-only changes do not change the API image.

The installed Desktop was driven through Chrome DevTools MCP in the user's
existing signed-in browser. The existing Codex agent kept its ID, prior messages
and working folder through migration/reload. Its live question about Primaris
invoked agent, run and harness tools and returned an accurate answer in about
70 seconds: both `desktop-assistant` and `desktop-reviewer` currently select
`desktop-opencode-workspace`, while older runs retain their original resolved
harnesses. The latest prior Prime run was reported failed and earlier work
succeeded. Profile references and run states were independently compared with
live Kubernetes metadata. This proves actual local-harness access through Anvil,
not just an enabled badge. Prime's native defaults remain unchanged.

An independent Grok review found a shorter discovery HTTP timeout leaking into
tool operations and an underspecified JSON quoting example. The bridge now uses
its operation context deadline and documents JSON stdin. Its proposed Windows
session-path conversion was rejected: the invoked client remains a Windows
executable, so it must receive its native Windows file path. Windows compilation
passed; Windows-to-WSL execution has not been live-verified.

The next live Codex turn sent a single message to `desktop-reviewer` and polled
its returned standing thread. It retrieved the exact reply
`ANVIL_PEER_CONNECTED_914` in about **44 seconds**. The remote run
`chat-turn-04134f162bcde14344f3a8af186d7203997f6b54` independently reached
Succeeded using OpenCode / `opencode/big-pickle` on node `acer`, from
20:32:36Z to 20:32:54Z. The remote agent's own UI displayed the saved request and
reply in standing thread `c0cd6f46-936e-401d-bfc2-6199553e23c4`; switching to
Desktop Assistant, back to Reviewer, and reloading returned the same thread ID
and reply. This is live request/reply retrieval within one local turn, not an
automatic callback after the local turn ends.

Final verification: `make verify`, scoped tool-operation tests, the signed-in
connection browser fixture, and the production Desktop build pass. Prior
PostgreSQL concurrency, remote roster, local recovery, race and visual checks
are recorded in the UI review. Installed screenshots and browser snapshots were
inspected, including Local labels, agent icons, preserved history, tool activity
and the reviewer standing thread.
