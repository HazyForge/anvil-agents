# Knowledge Bases And Tools

Anvil Agents deliberately does not embed a knowledge-base vendor. A knowledge
system is an external capability with four separate concerns:

1. **Connection**: a URL, local CLI, mounted data, or custom runner image.
2. **Identity**: a read-only token or workload identity in a Kubernetes Secret.
3. **Tooling**: an `AgentToolSet` with a small command and stable I/O contract.
4. **Policy**: prompt or injected skill text explaining when and how to use it.

Keeping these layers separate lets the same loop use a Markdown vault, an HTTP
search service, a vector store, or an internal runbook API without changing the
controller.

## HTTP Knowledge Service

`examples/knowledge-service/profile.yaml` defines a concrete, provider-neutral
operation-envelope contract: `POST /v1/operations` with the `search-index`
operation returns JSON. One `AgentToolSet` installs `knowledge-search`, while a
separate `AgentSkillSet` teaches every harness when to call it. A custom harness
must implement the common tool-setup contract to consume it. Separate Codex and
Pi `AgentHarnessProfile` objects inject the same read-only knowledge identity
alongside their own provider credentials. The run profile composes the runtime,
policy, and tool independently.

`examples/knowledge-service/runs.yaml` shows both useful local changes: one run
augments the shared search policy without editing the set, and another swaps
the harness to Pi while keeping the same role and knowledge skill.

Adapt the wrapper to the API you already deploy. The operator does not install
or own that API. Prefer an in-cluster TLS name or an externally verified HTTPS
endpoint. Give the token only read operations; use a separate profile and
explicit approval for writes. A wrapper that only emits read operations is not
a server-side authorization boundary: the service must enforce the split.

An external knowledge API gives independently scheduled Jobs node-neutral
shared context without a multi-writer home volume. Size and rate-limit it for
the expected concurrent run count. It is a shared service, not shared agent
memory; each run still decides what to query and records its own outcome.

## Git-Backed Knowledge

For a small instruction pack, an `AgentSkillSet` skill can use
`sourceRefs.github` to fetch one file from GitHub at run materialization. Pin
`ref` to a commit SHA for repeatability. A private repository token is read
from a same-namespace Secret. This is file injection, not search, and v0.1
supports only the GitHub Contents API for remote skill files.

For a full Markdown vault, either mount an intentionally managed
`AgentDataVolume` or use a custom image that clones a read-only repository and
ships the vault's real indexing CLI. Do not put a live multi-writer Git vault on
a shared RWO PVC and assume the controller provides synchronization.

## MCP And Other Protocols

`AgentToolSet` can install a reviewed MCP launcher or client command, but it
does not model MCP server discovery or provider-native session semantics. A
harness that already supports MCP can configure those details in its image,
home volume, or setup script. Preserve provider-native behavior rather than
hiding incompatible protocols behind one loose field.

## Tool-Use Evidence

A successful setup or verification command proves that a tool was available
before the model started; it does not prove that the model invoked it. Tool
wrappers that need operator-visible evidence should emit redacted lifecycle
markers to stderr while keeping their normal result on stdout:

```text
ANVIL_AGENT_RUN_TOOL_CALL_START name=knowledge-search call_id=RUN-knowledge-search-123
ANVIL_AGENT_RUN_TOOL_CALL_OK name=knowledge-search call_id=RUN-knowledge-search-123 response_bytes=456
```

Use `ANVIL_AGENT_RUN_TOOL_CALL_FAILED` for a terminal invocation failure. Keep
the tool name and opaque correlation ID stable, but never place queries,
arguments, credentials, URLs containing tokens, or response content in the
marker. These lines are a portable log convention, not controller status or a
substitute for provider-native traces. Retain raw logs in an approved external
store when exact historical replay matters; terminal AgentRun status contains
only bounded output.

## Supply-Chain Rules

- Pin tool versions and verify checksums or signatures.
- Never curl an unversioned installer in a production profile.
- Keep setup scripts reviewable and idempotent.
- Treat write access to `AgentToolSet` and the legacy
  `AgentSkillSet.spec.tools` field as code-execution authority because setup
  scripts execute in every consuming Job.
- Bound output size and avoid logging tokens or sensitive note contents.
- Use separate read and write credentials.
- Treat an injected tool as capability, not authorization to use it outside
  the run objective.
