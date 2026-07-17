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

The image includes Grok, Go, Helm, kubectl, gh, git, rg, the status helper,
feedback helper, and observability helper. It does not bundle another control
plane or knowledge-base client. Credentials come from run-selected Secrets;
the adapter does not initiate OAuth consent inside the Job.
