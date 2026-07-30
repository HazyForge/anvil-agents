# OpenClaw AgentRun adapter

This image executes backend kind `openClaw`. The entrypoint merges prompt
layers into a protected file and invokes `openclaw agent --message-file`, so
mounted context is not exposed on process argv.

Provider, authentication mode, model, thinking, service tier, and local mode
come from `spec.harness.backend`. Durable state, workspace files, and optional
Codex auth should use a dedicated AgentDataVolume.

## Auth maintenance

OpenClaw is the auth provider identity for `AgentAuthSession` with
`provider: openClaw`. `spec.authMode` records `oauth` (current operational path)
or `apiKey` (structural support for importing a valid OpenClaw `api_key`
profile store later—**no API key is provisioned into manifests**).

Operators use `anvil-agentctl auth openclaw|claw` with required `--agent-id`
matching harness `openClaw.agentId` and `--model-provider` matching the harness
model provider (for example `xai`). Staging key `OPENCLAW_AUTH_PROFILES_JSON`
holds a version=1 profile store (not `openclaw.json`, not a database, and not
Grok Build `~/.grok/auth.json`, which is incompatible). Maintenance Jobs run as
UID/GID/fsGroup `1000`, resolve `agentDir` by strictly parsing the registered
agent entry in volume-owned `openclaw.json` without launching the CLI or
plugins, and write only that agent's canonical auth store through the exported
plugin SDK (`saveAuthProfileStore`). The staged Secret projects only the exact
profile-store key. Unrelated SQLite/config/state is preserved.
Reauth treats the staged document as the complete replacement profile store for
the selected agent; it never mutates global `openclaw.json` auth metadata or a
different agent's store. Seed/logout marker files are maintenance receipts only:
normal OpenClaw AgentRuns do not mount profile-store bootstrap credentials and
do not automatically reseed from them.

The image includes OpenClaw, Codex, kubectl, gh, git, rg, the status helper,
feedback helper, and observability helper. Application-specific tools must be
injected or provided by an overlay image.
