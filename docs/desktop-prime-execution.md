# Prime Agent and Desktop execution location

Desktop Chat defaults to **Local WSL** (or this computer when outside WSL), with
**Prime Agent** selected. An explicit saved Primaris selection remains respected.
The location selector appears above the conversation. The local path works
without remote OIDC sign-in; Primaris conversations still require it.

| Location | Harness process | Model and login | Files | History |
| --- | --- | --- | --- | --- |
| Local WSL / this computer | Installed catalog CLI on the Desktop host or selected WSL distro | Native harness configuration and provider login | Persistent local working folder, editable for a new conversation | Browser storage on this device, replayed into later turns |
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
separate work. A local Prime conversation currently has its native tools and
context; it does not automatically receive the remote API's peer allowlist.

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
