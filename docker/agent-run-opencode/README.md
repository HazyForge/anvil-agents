# OpenCode AgentRun adapter

This image executes backend kind openCode with the pinned OpenCode 1.18.3
standalone binary. The entrypoint prepares the repository, injected tools,
immutable instructions, context, and run prompt, then pipes the combined prompt
to the supported non-interactive opencode run command. Prompt text is not
placed in process arguments.

Build the image through the repository-owned mapping:

    ./hack/build-images.sh --component opencode --tag local

Backend settings map to ANVIL_OPENCODE_MODEL, ANVIL_OPENCODE_AGENT,
ANVIL_OPENCODE_VARIANT, ANVIL_OPENCODE_FORMAT, ANVIL_OPENCODE_AUTO,
ANVIL_OPENCODE_PURE, and ANVIL_OPENCODE_ADDITIONAL_ARGS_JSON. Models retain
OpenCode's native provider/model form. JSON event output and pure mode are the
runner defaults.

Provider API keys such as OPENAI_API_KEY, ANTHROPIC_API_KEY, XAI_API_KEY, or
other provider-native variables should come from the harness profile's
envSecretRefs. OPENCODE_AUTH_JSON can seed a complete existing OpenCode auth
object without interactive login; the runner writes it with restrictive
permissions only when the durable auth file is absent. Once seeded, the durable
file remains authoritative so OpenCode can preserve refreshed OAuth state;
provider-native API-key environment variables remain the simpler rotation path.

Durable OpenCode state defaults to /opt/anvil/opencode. The image maps HOME and
the XDG data, config, cache, and state directories below that root, including
the standard XDG data path opencode/auth.json. Attach a dedicated
AgentDataVolume when sessions or OAuth refresh state must persist.

The Auto backend option enables OpenCode's dangerous auto-approval flag. It
does not override explicit deny rules, and it should be enabled only for a
least-privilege Pod with scoped credentials, storage, network access, and
ServiceAccount RBAC. Additional arguments cannot attach to another server,
continue a prior session, share a session, replace the prompt with a command,
change the workdir, replace controller-owned output/model settings, or bypass
the explicit Auto field. The runner allowlists only `--thinking`,
`--print-logs`, `--log-level=DEBUG|INFO|WARN|ERROR`, and a non-empty
`--title=...` form.

The image also includes kubectl, gh, git, curl, jq, rg, Python, the status
helper, feedback helper, and observability helper. OpenCode auto-update checks
are disabled so the runtime stays coupled to the image digest.

## Verified smoke run

On 2026-07-19, the repository-built `anvil-agent-run-opencode:test` image ran
the complete entrypoint against `opencode/big-pickle` with JSON output, pure
mode, auto approval disabled, and repository cloning disabled. The process
exited 0, emitted `OPENCODE_HARNESS_SMOKE_OK`, and produced both
`ANVIL_AGENT_RUN_START` and `ANVIL_AGENT_RUN_COMPLETE` lifecycle markers. This
credential-free model is useful for a plumbing smoke test while OpenCode offers
it, but provider-backed production profiles should select and authenticate an
explicit provider/model.

A second run on merged `master` commit
`e71728ebab55002228a0878c62c81c14bcb46b5d` mounted that exact checkout at
`/workspace` with Docker's read-only bind option, supplied a valid `AgentRun`
context with `observe` intent, and kept OpenCode auto approval disabled. The
model used OpenCode's file-read tool on `README.md` and `Makefile`, correctly
reported the Anvil Agents project name, identified `make verify` as the
required validation target, confirmed OpenCode is a built-in harness, emitted
both lifecycle markers, and exited 0. The read-only filesystem mount enforced
the mutation boundary for this smoke; `observe` intent and disabled auto
approval are policy inputs, not a sandbox on their own.
