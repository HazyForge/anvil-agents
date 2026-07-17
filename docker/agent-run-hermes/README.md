# Hermes AgentRun adapter

This image executes backend kind `hermesAgent`. The entrypoint merges prompt
layers into a protected file, then `anvil-hermes-query` loads that file and
invokes Hermes in process. Prompt content is never expanded onto process argv.

`ANVIL_HERMES_ADDITIONAL_ARGS_JSON` accepts supported chat settings such as
model, provider, toolsets, skills, maximum turns, verbosity, and approval mode.
Query arguments are ignored so the mounted prompt remains authoritative.

Keep Hermes profile and provider state on a dedicated AgentDataVolume. Supply
credentials through run-selected Secrets; the adapter does not initiate new
OAuth consent inside the Job.
