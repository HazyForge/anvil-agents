# Pi AgentRun adapter

This image executes backend kind `piAgent`. The entrypoint writes the combined
prompt to a protected file and passes it with Pi's `@path` print-mode contract,
so prompt content is not expanded onto process argv.

Provider, authentication mode, model, thinking, output mode, and session policy
come from `spec.harness.backend.piAgent`. Pi state and provider credentials
should use a dedicated AgentDataVolume.

The image includes Pi, `pi-xai-oauth`, kubectl, gh, git, jq, rg, curl, Python,
the status helper, feedback helper, and observability helper. It does not bundle
another control plane or knowledge-base client.
