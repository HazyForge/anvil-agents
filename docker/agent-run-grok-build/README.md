# Grok Build AgentRun adapter

This image executes backend kind `grokBuild`. The entrypoint combines immutable
prompts, injected skills, context, and the run prompt in a protected temporary
file and invokes `grok --prompt-file`.

```sh
docker build -f docker/agent-run-grok-build/Dockerfile -t anvil-agent-run-grok-build:local .
```

Backend settings are supplied through `ANVIL_GROK_BUILD_*` variables for model,
provider, authentication mode, effort, service tier, profile, command, and
additional arguments. Durable state defaults to `/opt/anvil/grok-build` and
should be backed by a dedicated AgentDataVolume when persistence is required.

The image includes Grok, Go, Helm, kubectl, gh, git, rg, `anvil-agentctl`, the
status helper, feedback helper, and observability helper. It does not initiate
OAuth consent inside the Job. Credentials come from run-selected Secrets:

- **apiKey mode**: mount `XAI_API_KEY`.
- **oauth mode**: durable `$GROK_HOME/auth.json` under the AgentDataVolume home
  (default `/opt/anvil/grok-build/.grok/auth.json`). Operators re-seed with
  `anvil-agentctl auth grok reauth --auth-file ~/.grok/auth.json`.
  `GROK_AUTH_JSON` only seeds when auth is missing or the seed id changes.
