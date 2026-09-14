# Prime Agent runner

Runs native Prime Agent **0.9.4** from the versioned release archive published
by the official installer, with a pinned SHA-256 checksum. Prime is distributed
as release tarballs, not the inherited Pi npm package or an npm `prime-agent`
release. Build through `hack/build-images.sh --component prime`.

The adapter preserves immutable AgentRun instructions, injected skills, tool
setup/verification, repository checkout, context, and the user prompt. It pipes
the combined prompt to `prime-agent --print --mode json --cwd /workspace`;
prompt text and provider credentials never become process arguments. Native
IPython tools remain enabled. The native Python runtime can read/write files,
run shell commands, and use installed Python skills with the Pod's authority.

The image installs the pinned CLI's packaged `prime-agent-runtime`, bundled
Python skills, and declared default Python dependencies into `/opt/prime-kernel`.
`PRIME_AGENT_KERNEL_PYTHON` selects this prepared environment so a new runner
does not need to download Python or bootstrap tools. Prime's optional npm
postinstall can skip failures, so it is replaced by an explicit build step
and an actual nonroot native-kernel file/shell test. Re-run that test without
models, networking, or credentials using:

```sh
docker run --rm --network none --entrypoint node IMAGE /opt/anvil-agent-run/kernel-smoke.mjs
```

`ANVIL_PRIME_PROVIDER`, `ANVIL_PRIME_MODEL`, and `ANVIL_PRIME_THINKING` map to
native selectors without provider/model substitutions. `ANVIL_PRIME_MODE`
supports `json` (default) or `text`; `ANVIL_PRIME_NO_SESSION=true` disables
native session persistence. `ANVIL_PRIME_ADDITIONAL_ARGS_JSON` accepts a JSON
array of `--verbose`, `--no-extensions`, `--no-context-files`, and
`--no-prompt-templates`. Other flags cannot override dedicated execution fields
or inject credentials, prompts, extensions, or tool restrictions.

`ANVIL_PRIME_HOME` defaults to `/opt/anvil/prime`. The adapter maps native
`PRIME_AGENT_CODING_AGENT_DIR` to its `agent` subdirectory,
`PRIME_AGENT_SESSION_DIR` to `sessions`, and `HOME` to `home`. A dedicated
AgentDataVolume can retain this native state; each invocation still starts a
new turn without implicitly resuming another native session.

Inject provider-native environment credentials through a harness's Secret
references. For existing provider-neutral Secrets, declared
`modelProvider=deepseek` and `providerAuthMode=apiKey` map `apiKey` to
`DEEPSEEK_API_KEY` only when the native key is unset, then unset the generic
key before tools start. No auth-file schema or credentials are copied by this
adapter. Other providers retain their native credential mechanisms.

`ANVIL_PRIME_TURN_TIMEOUT_SECONDS` defaults to 280, bounded to 1–86400 seconds;
the enclosing Job should allow startup time beyond that value. JSON completion
requires a native `agent_end` with a final assistant text message and
`stopReason=stop`, plus successful process exit. Errors, aborted turns,
truncated turns, and missing final replies cannot emit Anvil completion.
Native stdout events and tool activity remain intact in runner logs.

Run `docker/agent-run-prime/entrypoint_test.sh` for the fake-CLI contract tests.
