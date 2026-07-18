# Harnesses

One `AgentRun` creates one Job and selects one harness. Install-wide
`runnerImages` values provide defaults; the source chart uses local `:dev`
names and a packaged chart uses its matching `vVERSION`. A profile or run can
override `spec.harness.backend.image`. Production installations should use
image digests.

| Kind | Runtime | Provider fields | Durable home recommended |
| --- | --- | --- | --- |
| `codex` | OpenAI Codex CLI | Codex model, reasoning, approval settings | yes |
| `hermesAgent` | Hermes Agent | provider, auth mode, model | yes |
| `openClaw` | OpenClaw | provider, agent ID, model, thinking | yes |
| `grokBuild` | Grok Build | xAI model, profile, service tier | yes |
| `piAgent` | Pi coding agent | provider, model, thinking, mode | yes |
| `custom` | operator-supplied image | command and args | image-defined |

The model-provider enum currently covers OpenAI/Codex and xAI integrations.
Remote issue context and remote skill files are currently GitHub adapters. They
are optional integration surfaces, not requirements for custom harnesses.

## Common Contract

Every built-in runner receives:

- `ANVIL_AGENT_RUN_PROMPT_FILE`: complete generated prompt.
- `ANVIL_AGENT_RUN_CONTEXT_FILE`: structured run and source context.
- `ANVIL_AGENT_RUN_TOOL_SETUP_FILES`: newline-separated executable setup files.
- `ANVIL_AGENT_RUN_TOOLS_JSON`: names, descriptions, setup paths, and checks.
- `ANVIL_AGENT_RUN_STATUS_LOG_PREFIX`: prefix for JSON status log records.
- provider and backend-specific environment variables.

Setup scripts run after repository preparation and before the runtime starts.
They execute as the container user in the configured workdir. Keep them
idempotent, install only into writable paths, pin downloaded artifacts, and
pass credentials through `envSecretRefs` rather than inline YAML.

## Custom Harness

A custom image should read the prompt and context files, perform one bounded
task, print useful logs, exit nonzero on failure, and emit status JSON. The
small image under `examples/quickstart` is the executable reference contract.
A status line looks like:

```text
ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"inspect","summary":"completed","residualRisk":"none"}
```

The controller also treats Kubernetes Job success or failure as terminal
evidence. Structured reports enrich status; they do not override a failed Job.

## Source Preparation

The built-in images honor their documented repository environment variables,
but v0.1 does not have a provider-neutral repository source object. Supply
repository URL/ref variables through a profile or Secret, or build source
preparation into a custom image. Do not assume that `applicationRef` clones a
repository; it is only scope metadata.
