# Agy AgentRun adapter

This image executes backend kind `agy` (Antigravity CLI).
The entrypoint writes the combined prompt to a protected file and passes it via stdin (`-p -` or `--input-format stream-json`), ensuring prompt content is not expanded onto process argv.

Model, execution mode (`print`, `stream-json`), thinking level, and additional arguments come from `spec.harness.backend.agy`.
Agy auth seeding is supported via `AGY_AUTH_JSON` or mounted credentials into `~/.agy/auth.json`.
