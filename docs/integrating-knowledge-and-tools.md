# Knowledge Bases And Tools

Anvil Agents deliberately does not embed a knowledge-base vendor. A knowledge
system is an external capability with four separate concerns:

1. **Connection**: a URL, local CLI, mounted data, or custom runner image.
2. **Identity**: a read-only token or workload identity in a Kubernetes Secret.
3. **Tooling**: a small command with a stable input/output contract.
4. **Policy**: prompt or injected skill text explaining when and how to use it.

Keeping these layers separate lets the same loop use a Markdown vault, an HTTP
search service, a vector store, or an internal runbook API without changing the
controller.

## HTTP Knowledge Service

`examples/knowledge-service/profile.yaml` defines a concrete, provider-neutral
contract: `GET /v1/search?q=...` returns JSON and one `AgentSkillSet` teaches
every built-in harness to install and call `knowledge-search`. A custom harness
must implement the common tool-setup contract to consume it. Separate Codex and
Pi `AgentHarnessProfile` objects inject the same read-only knowledge Secret
alongside their own provider credentials. The run profile composes one runtime
with the shared capability.

`examples/knowledge-service/runs.yaml` shows both useful local changes: one run
augments the shared search policy without editing the set, and another swaps
the harness to Pi while keeping the same role and knowledge skill.

Adapt the wrapper to the API you already deploy. Prefer an in-cluster TLS name
or an externally verified HTTPS endpoint. Give the token only read operations;
use a separate profile and explicit approval for writes.

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

MCP is not yet a backend-neutral CRD feature. A harness that already supports
MCP can configure it in its image, home volume, or setup script. Document that
as harness-specific behavior. A future generic context-source API should use
pinned Git/OCI/HTTP artifacts and preserve provider-native semantics rather
than hiding incompatible protocols behind one loose field.

## Supply-Chain Rules

- Pin tool versions and verify checksums or signatures.
- Never curl an unversioned installer in a production profile.
- Keep setup scripts reviewable and idempotent.
- Treat write access to `AgentSkillSet` as code-execution authority because its
  setup scripts execute in every consuming Job.
- Bound output size and avoid logging tokens or sensitive note contents.
- Use separate read and write credentials.
- Treat an injected tool as capability, not authorization to use it outside
  the run objective.
