# OpenClaw AgentRun adapter

This image executes backend kind `openClaw`. The entrypoint merges prompt
layers into a protected file and invokes `openclaw agent --message-file`, so
mounted context is not exposed on process argv.

Provider, authentication mode, model, thinking, service tier, and local mode
come from `spec.harness.backend`. Durable state, workspace files, and optional
Codex auth should use a dedicated AgentDataVolume.

The image includes OpenClaw, Codex, kubectl, gh, git, rg, the status helper,
feedback helper, and observability helper. Application-specific tools must be
injected or provided by an overlay image.
