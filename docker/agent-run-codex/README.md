# Codex AgentRun adapter

This image executes backend kind `codex`. It reads mounted prompt and context
files, optional injected skills and tools, then starts `codex exec`.

```sh
docker build -f docker/agent-run-codex/Dockerfile -t anvil-agent-run-codex:local .
```

The shared contract includes:

- `ANVIL_AGENT_RUN_PROMPT_FILE` and `ANVIL_AGENT_RUN_CONTEXT_FILE`
- `ANVIL_AGENT_RUN_SKILL_FILES` and `ANVIL_AGENT_RUN_TOOL_SETUP_FILES`
- `ANVIL_AGENT_RUN_REPOSITORY`, repository URL, and repository ref
- `ANVIL_AGENT_RUN_STATUS_FILE` and `ANVIL_AGENT_RUN_STATUS_TOOL`
- `ANVIL_AGENT_FEEDBACK_TOOL` and optional Discord routing variables
- optional Prometheus, Loki, Tempo, and Grafana endpoint variables
- `SPIFFE_ENDPOINT_SOCKET` and `ANVIL_AGENT_RUN_SPIFFE_ID` when enabled

Codex-specific values include model, reasoning effort, verbosity, service tier,
goal mode, sandbox, and additional arguments. Credentials come from
`spec.harness.execution.envSecretRefs`; non-secret values use `extraEnv`.

The image includes Codex, Go, Helm, kubectl, gh, git, curl, jq, rg, Python,
`anvil-agent-status`, `anvil-agent-feedback`, and `anvil-observability`. It does
not bundle another control-plane CLI. Additional application tools belong in
`spec.harness.tools` or an overlay image.

Repository cloning, tool setup, and GitHub authentication happen before Codex
starts. Tokens are not printed. A missing repository or insufficient push
permission is reported as residual risk unless the run requires it.

Immutable prompts in `static-prompts/` load before profile, event, skill, goal,
and environment overlays. Structured status lines are written to pod stdout so
the controller can own `AgentRun.status`.
