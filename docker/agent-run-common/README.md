# Shared GitHub authentication

All built-in runner images can configure pod-local `gh` and Git HTTPS
authentication before repository checkout. A workload selects exactly one of
these credential adapters through a namespace-local Secret referenced by
`execution.envSecretRefs`:

- static compatibility token: `GH_TOKEN` or `GITHUB_TOKEN`;
- GitHub App: `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, and
  `GITHUB_APP_PRIVATE_KEY`.

GitHub App auth also requires explicit non-secret scope in a policy-reviewed
`AgentHarnessProfile` or `AgentRunProfile` `execution.extraEnv`:

```yaml
- name: ANVIL_GITHUB_APP_REPOSITORY
  value: HazyForge/example-agent
- name: ANVIL_GITHUB_APP_REPOSITORY_ID
  value: "123456789"
- name: ANVIL_GITHUB_APP_PERMISSIONS_JSON
  value: '{"checks":"read","contents":"write","issues":"write","pull_requests":"write","statuses":"read"}'
```

Repository name or positive repository ID is required. Supplying both makes the
runner verify both. When `ANVIL_AGENT_RUN_REPOSITORY` is also set, its exact
owner/repository value must match. The permission object must be non-empty and
is capped to:

| Permission | Accepted token access |
| --- | --- |
| `checks` | `read` |
| `contents` | `read`, `write` |
| `issues` | `read`, `write` |
| `metadata` | `read` |
| `pull_requests` | `read`, `write` |
| `statuses` | `read` |

Run-local `extraEnv` cannot set credential bootstrap names. The controller
rejects attempts to redirect the host, change repository/permission scope,
override timeout, or persist the token in another credential directory.

The App installation must itself select only intended repositories. Bootstrap
requests a token for exactly one repository, verifies that GitHub returned only
that repository and the requested permissions (with optional implicit
`metadata: read`), then configures the pod-local `gh` credential store. The
runner re-execs a sanitized second-stage process so the App key, JWT, and raw
token are absent from both inherited environment and `/proc/1/environ` before
repository checkout, tool setup, or model execution. The scoped token remains
only in the pod-local credential store so authorized `gh` and Git operations
can work.

GitHub App runs must set `timeoutSeconds` from 1 through 3000. Bootstrap also
requires the returned token expiry to cover that runtime plus a five-minute
safety margin. Continuous work uses bounded scheduled AgentRuns; the reusable
App key is deliberately not retained in a refresher process.

Empty or whitespace-only `GH_TOKEN`/`GITHUB_TOKEN` and GitHub App fields skip
bootstrap (fail-open) and print `ANVIL_AGENT_RUN_GITHUB_AUTH_SKIPPED` so a
runner can reach `ANVIL_AGENT_RUN_START` without calling `gh`. `gh auth login`,
`gh auth setup-git`, and the App installation-token HTTP request are bounded
(default 5s, `ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT_SECONDS`). A timeout prints
`ANVIL_AGENT_RUN_GITHUB_AUTH_TIMEOUT`, sanitizes the process environment, and
continues rather than hanging the entrypoint. Mixed token+App input, invalid
hosts, and over-privileged App permissions still fail closed.

Static token and App inputs are mutually exclusive. Over-privileged App input
fails closed. App bootstrap is restricted to `github.com` so a
run-controlled host cannot receive a signed App JWT. Static-token compatibility
supports GitHub Enterprise Server with an exact normalized
`ANVIL_GITHUB_HOST`, deriving `https://HOST/api/v3` rather than accepting a URL.
