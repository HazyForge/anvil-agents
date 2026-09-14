# Antigravity AgentRun adapter

This image executes backend kind `agy` using Google's native Antigravity CLI
1.2.2. The Dockerfile pins the official release archive and SHA512 separately for
Linux amd64 and arm64, verifies the archive, and checks the installed version.
Automatic updates are disabled so the image digest identifies the executable.

The adapter keeps stdin open until the native result arrives, then closes it.
Closing it immediately can yield an empty response with zero usage. The adapter
sends the protected combined prompt as one native `user` event with
`--input-format stream-json --output-format stream-json`. It never puts the prompt
in argv. `-p -` is not a documented stdin sentinel and is not used. Native events
remain visible in runner logs; stderr remains a separate stream. Completion
requires both exit code zero and a successful native `result` event.

`spec.harness.backend.agy.model` maps to `--model`. The compatibility field
`thinking` maps to native `--effort` and accepts `low`, `medium`, or `high`.
`mode` accepts `print`, `text`, or `stream-json`; all use the native JSON transport
and preserve events. `additionalArgs` appends native CLI arguments. Tool setup
and verification and repository/ref checkout use the standard runner contracts.

This entrypoint executes one turn and closes stdin after receiving its result. It does not implement a
persistent conversation, mid-turn steering, or a harness adapter daemon. Those
capabilities require a process owner that keeps native stdin open and processes
results before submitting subsequent turns.

## Authentication

Use Google's native authentication configuration. Do not seed `~/.agy/auth.json`:
AGY does not document that file, and `AGY_AUTH_JSON` is rejected rather than
pretending a sign-in succeeded. The desktop catalog likewise makes no auth-file
claim. A Zitadel desktop session authenticates the Anvil API, not Google.

The runner's default home is `/opt/anvil/agy/home`, configurable with
`ANVIL_AGY_USER_HOME`. Mount provider-managed configuration and credential storage
there as appropriate for the chosen native authentication method. Google's
interactive account sign-in uses the OS credential store; copying a settings
file alone does not establish a Google session. Enterprise ADC is a separate
provider-supported option that needs its own configured identity.

For pinned CLI 1.2.2, a bounded authenticated container check also verified that
an existing native `$HOME/.gemini/antigravity-cli/antigravity-oauth-token` file
works without mounting the workstation's D-Bus or keyring. Preserve its opaque
provider-owned contents and supply it through an approved server-side credential
mount or `ANVIL_AGY_NATIVE_OAUTH_TOKEN` from an approved environment Secret; never put its contents in YAML or prompts.
The runner writes that opaque bootstrap value only when the native file is
missing, with mode 0600, and unsets the environment variable before running setup
tools or AGY. It preserves existing native files so refreshes are not overwritten.
The check used a read-only token mount. Longer-lived homes may need provider
credential refresh writes, so a successful bounded check does not establish
refresh behavior.

For Google's documented Gemini API-key mode, mount
`$HOME/.gemini/antigravity-cli/settings.json` containing
`{"modelProvider":"gemini"}` and inject `GEMINI_API_KEY` through the existing
server-side secret mechanism. Setting the environment variable alone does not
select that provider. This mode uses the Gemini API and its billing, separately
from an Antigravity account subscription. Never put credentials into the prompt,
image, repository, or desktop OIDC token.

Sources: [headless protocol](https://antigravity.google/docs/cli/headless/),
[installation and authentication](https://antigravity.google/docs/cli/install/),
[official installer](https://antigravity.google/cli/install.sh), and
[stdin sentinel request](https://github.com/google-antigravity/antigravity-cli/issues/582).

## Verification

`docker/agent-run-agy/entrypoint_test.sh` exercises JSON prompt framing, native
flags, tool setup, repository checkout, exit propagation, and terminal-result
checks with a deterministic CLI fixture. It is part of `make verify`.

Build with `hack/build-images.sh --component agy --tag test --load`, then verify
`docker run --rm --entrypoint agy anvil-agent-run-agy:test --version` and
`docker run --rm --entrypoint agy anvil-agent-run-agy:test --help`.
An authenticated provider response is a separate live check; successful fixture
tests or a version command do not establish that credentials work.
