# One chat across local and cloud sessions

`onechat` is a CLI and Go package for a single conversation with a model-backed
manager. It sees your sessions and recent replies, chooses an action, executes
it, then composes an answer from actual outcomes. Every turn produces a private
JSON trace for review. This is separate from Desktop and does not change the
controller, standing recipes, or chat API.

## Sessions, setups and permissions

A **local session** is a harness process in a real folder. Onechat owns its
transcript and records the native session ID for subsequent turns. It discovers
sessions it created, not arbitrary native harness histories or processes.

Local launches use onechat's own recipes:

| Harness | First turn | Resume | Allowed settings |
| --- | --- | --- | --- |
| Claude Code | `claude -p --output-format json --strict-mcp-config --permission-mode <mode> [--allowedTools <list>]` | Same flags plus `--resume <id>` | `permissionMode`: `acceptEdits` (default), `default`, `plan`; `allowedTools`: `Read`, `Grep`, `Glob` or `Bash(<command prefix>:*)` |
| Codex | `codex exec --skip-git-repo-check --json --sandbox <sandbox> -C <workdir> -` | `codex exec resume <id> --skip-git-repo-check --json -c sandbox_mode="<sandbox>" -` | `sandbox`: `read-only` (default), `workspace-write` |

`allowedTools` lets a non-interactive Claude session run its project's checks,
for example `Bash(go test:*)`. Unrestricted `Bash`, `Bash(*)`, prefixes with
shell metacharacters, and every other tool are rejected at config load.

Both receive the prompt on stdin and run in the session workdir. The installed
`codex exec resume --help` does not advertise `--sandbox` or `-C`; resume uses its
supported `-c` override and the process cwd. The initial sandbox is persisted on
the session. A later config edit cannot widen a read-only session; tightening to
read-only is retained. Older resumed sessions without a recorded sandbox run
read-only. Codex replies use the last `agent_message`, and usage comes from
`turn.completed`. Claude's JSON result provides reply, session ID, usage and cost.

Other local harnesses fail at config load with `no safe local recipe yet for
<harness>`. Claude `bypassPermissions`/`auto` and Codex `danger-full-access` are
rejected. Onechat does not reuse container-oriented standing recipes or their
approval-bypass flags. If a supported harness has no resume ID yet, its owned
transcript is supplied as context for the next call.

A **cloud session** is an API chat thread discovered in the configured namespaces.
Onechat reads its transcript and active turns, maps its profile to a setup, and
appends each new message with a UUID request ID. It waits for that exact turn's
assistant reply or failure. A polling timeout retains `running` state: it does
not cancel the cloud execution. Unknown-profile threads are visible for status
but cannot receive work until a matching setup is configured.

Each new cloud session creates an ordinary persona thread:

```json
{"profileName":"the-profile","mode":"persona","title":"New work"}
```

There is no `standing: true` field. Each session gets a distinct thread, even
when several sessions use one profile. HTTP 201 is required. An unexpected 200
is recorded as an error with its receipt, and the returned thread is not adopted
or sent a message. Thread identity is separate from the profile's execution
backend: a persona thread can still use a profile configured for a warm standing
process. Discovery fails explicitly at the API's 200-thread limit because the
current list handler has no pagination.

A **setup** describes a configured work destination: `id`, `location`, `harness`,
optional informational `model`, user-editable `tags` and `notes`, plus local
permission settings or cloud `namespace` and `profile`. Tags and notes express
shared-work preferences and harness strengths. They do not grant authority,
configure tools, or prove capability. Each cloud namespace/profile pair maps to
one setup. Existing thread bindings are persisted. Only `manager.model` controls
a model CLI argument; session models remain in native/profile configuration.

## Decisions

The [embedded manager instructions](../internal/onechat/manager_prompt.md) remain
the operator's unchanged draft. Claude Code `sonnet` is the default manager. It
runs in an empty temporary directory with structured JSON output, manual
permission mode, strict MCP config and no session persistence.

Decide receives setups, the inventory with the last six messages per session
(600 Unicode characters each), the last ten one-chat messages and the new message.
The entire bounded inventory, including transcripts, is retained in TurnTrace.

| Action | Behavior |
| --- | --- |
| `answer` | Answer from available evidence without calling a session. |
| `read_status` | Read a session snapshot without sending work. |
| `send` | Continue an existing idle or failed session. |
| `create` | Create a session for a setup, then send its first message. |
| `ask_user` | Ask for a missing target, intent or authority. |

Decisions contain one to three actions, a short reasoning summary and confidence
from zero to one. References must exist. `answer`/`ask_user` cannot mix with
`send`/`create`. Sends to running sessions and duplicate sends in a decision are
rejected. Local creates need an existing directory under `local.workdirRoots`;
symlinks are resolved first. Existing local sessions are checked again before
sending. Changing a session's setup harness/location cannot redirect its resume.

Invalid output becomes an `ask_user` with validation errors, without retry or
session execution. Compose receives the user message, validated decision and
actual outcomes, and is instructed to claim only what those outcomes show. The
invalid-decision question is kept verbatim. Composition failure retains the real
outcomes and emits an explicit failure. A model summary is not independent proof
that the broader objective is complete.

## Production sign-in

Use [onechat.prod.yaml](../examples/onechat/onechat.prod.yaml):

```sh
GOFLAGS=-buildvcs=false go build -o /tmp/onechat ./cmd/onechat
/tmp/onechat --config examples/onechat/onechat.prod.yaml login
/tmp/onechat --config examples/onechat/onechat.prod.yaml sessions
/tmp/onechat --config examples/onechat/onechat.prod.yaml say "review the invoice tool"
```

`login` reads public `<cloud.baseURL>/ui-config.json`. Issuer, scopes, native
Desktop client ID (falling back to the console client ID), and redirect path come
from that response. No production issuer or client ID is hardcoded. OIDC
metadata is then discovered from the advertised issuer.

Login uses PKCE S256, random state and nonce. It listens on **127.0.0.1 only** at
`cloud.login.port` (default 1738), with the advertised callback path (default
`/auth/callback`). It prints the authorization URL on one line. Open that URL in
your browser and sign in. The listener waits at most ten minutes and consumes
one callback. Wrong state, provider errors and missing codes are rejected.

The code is exchanged with a form-encoded authorization-code grant. The ID token's
signature, issuer, audience, expiry and nonce are verified. The browser receives
"Signed in to one chat. You can close this tab." or a small error page. CLI
completion prints only `signed in, expires <time>` after the initial URL.
If another Desktop process occupies port 1738, login reports the bind failure;
use a registered redirect port that is available.

`<stateDir>/cloud-session.json` stores only `issuer`, `clientId`, `accessToken`,
`refreshToken` and `expiresAt`. Its directory is secured to 0700 and the file is
written atomically with mode 0600. `cloud.sessionFile` overrides this path; `~`
is expanded. Production config uses `~/.config/onechat/cloud-session.json`.
Treat this file as private credentials, and keep it outside fixture workspaces.

Without `cloud.tokenCommand`, the cloud backend uses this session. It refreshes
within two minutes of expiry, before subsequent requests, or after a 401, using
`grant_type=refresh_token` and `client_id`. Rotated credentials are saved atomically.
No session produces ``not signed in; run `onechat login` ``. Tokens are never printed,
logged, included in traces or passed on child argv. Known current/retired access
and refresh tokens are redacted from model inputs, history and traces.

When `cloud.tokenCommand: [argv...]` is configured, it takes precedence, preserving
the simulator workflow. It runs once at startup and again on a 401. Never put
literal credentials in its argv. A 401 retry preserves the same append UUID and
body; other failures are not retried automatically. HTTP redirects are refused.
OIDC endpoints require HTTPS except for loopback HTTP used by local issuers.

## Configuration and CLI

The production example
([onechat.prod.yaml](../examples/onechat/onechat.prod.yaml)) includes local
Claude (with a test-command allowlist), local Codex review and write, and cloud
agy (`desktop-agy`, plan mode) in namespace `anvilhub`. The other production
chat profiles did not answer when this was written: Grok
(`desktop-standing-assistant`) is unpaid and was retired from Primaris on
2026-10-27, OpenCode (`desktop-assistant`) returned empty replies from its
free model, and the DeepSeek observer is missing its credentials Secret. `desktop-agy` keeps one durable home, so it
takes one turn at a time: a concurrent turn gets HTTP 409. Setup notes are
routing preferences, not measurements.

[onechat.scenarios.yaml](../examples/onechat/onechat.scenarios.yaml) is the
same set for scenario runs, with no extra work roots, so local sessions can only
work inside each scenario's copied fixtures.

[onechat.yaml](../examples/onechat/onechat.yaml) remains the simulator example.
Set its API URL to the simulator's actual address. Its cloud profiles must already
exist. Remove `cloud` and cloud setups for local-only use. Native binaries must
be on PATH and authenticated. Relative paths, including fixture paths and token
commands, resolve from the invocation directory; home-prefixed paths expand `~`.

`local.timeout` is a positive Go duration, default `10m`, bounding each local
execution, manager call and cloud wait. Local stdout is capped at 1 MiB. Stderr
is bounded and omitted from traces. Model-facing child environments exclude
Kubernetes credentials, OIDC/API tokens, database URLs and WSL environment
forwarding, matching the standing environment filter without using its recipes.
The trusted token-command helper may use its own authentication environment.

`<stateDir>/onechat.json` stores local sessions/transcripts, one-chat history and
cloud thread-to-setup bindings. State and traces use atomic temporary-file rename
and mode 0600. Cloud transcripts remain authoritative in the API. Local state is
marked running before dispatch so interrupted work cannot silently replay. A
stale running local session needs operator reconciliation; there is no automatic
retry or recovery command yet.

## Scenarios and expectations

```sh
go build -buildvcs=false -o /tmp/onechat ./cmd/onechat
/tmp/onechat --config examples/onechat/onechat.prod.yaml login
/tmp/onechat --config examples/onechat/onechat.scenarios.yaml scenario run examples/onechat/scenarios/01-continue-local-work.yaml --out /tmp/onechat-scenarios
```

Run scenarios that use `cloud-agy` one at a time (see above).

| Scenario | What the manager has to do |
| --- | --- |
| [01-continue-local-work](../examples/onechat/scenarios/01-continue-local-work.yaml) | Send a code change to the local session that owns it; answer a question from a cloud transcript without running anything |
| [02-research-then-apply](../examples/onechat/scenarios/02-research-then-apply.yaml) | Start cloud research, then hand its conclusion to the local coding session |
| [03-cross-harness-review](../examples/onechat/scenarios/03-cross-harness-review.yaml) | Start a read-only Codex review of Claude's work, then send the top finding back to Claude |
| [04-which-one](../examples/onechat/scenarios/04-which-one.yaml) | Summarise every session, then infer which project "the new option" means |
| [05-authority-boundary](../examples/onechat/scenarios/05-authority-boundary.yaml) | Refuse to publish without authority, then follow the narrower instruction |
| [06-ask-when-ambiguous](../examples/onechat/scenarios/06-ask-when-ambiguous.yaml) | Ask when two projects fit, then act on a two-word answer |

Each scenario copies its fixtures, such as the
[invoice fixture](../examples/onechat/fixtures/invoice-cli), into a fresh
`<out>/<name>-state-*/work/<workspace>` directory. Setup messages run directly
through backends before manager turns. Each invocation gets fresh work/history,
including repeated scenario names. It keeps the person's original session file:
`cloud.sessionFile`, or the original config's `<stateDir>/cloud-session.json`.
Credentials are never copied into the scenario directory.

The aggregate report is `<out>/<name>.json`. Repeating a name replaces that report
while retaining old state directories. Only cloud IDs created during that scenario
can enter its inventory, and filtering happens before histories are read. Persona
creation allows fresh threads under existing profiles; no profile resets or
preexisting-thread adoption are needed.

Expectations are checked after execution and never shown to the decider:

```yaml
turns:
  - say: add a --json flag to the invoice tool
    expect: {action: send, session: inv}
  - say: start a new background research task
    expect: {action: create, setup: cloud-agy}
```

`send` requires a scenario session label; `create` requires a setup ID. With several
actions, any successful action of the expected type and target can match.
`check.notes` records its one-based action number and target. Failed expectations
produce a nonzero exit status and preserve the trace. Failed setup/turn execution
also writes a partial report.

## Review page

[hack/onechat-review](../hack/onechat-review) turns scenario reports into the One
Chat Review page: for every turn, what the person said, what the manager saw,
what it decided, what each session did, and the answer, with model, token and
cost figures where they were reported. The behavior in this document was approved
from that page.

```sh
python3 hack/onechat-review/build.py --out /tmp/one-chat-review.html \
  --model "Claude Sonnet" /tmp/onechat-scenarios/*.json
```

Pass the aggregate reports (`<out>/<name>.json`, `onechat.scenario/v1`); the
per-turn traces inside `<name>-state-*` are already included in them. Scenarios
appear in argument order. `--model` labels the manager model on the page.
`build.py` embeds the reports at the `__TRACES__` placeholder in
`template.html`, shortens local work directories to their last two components,
replaces scratch and home directory prefixes, and escapes `</` so report text
cannot end the page's script. It does not remove anything else, so review the
reports before sharing the page: prompts, replies and session transcripts appear
in full.

`template.html` is an Artifact page body (no `<!doctype>` or `<head>`; the
Artifact tool adds them when publishing). Publish the generated file as an
Artifact to share it, or open it directly in a browser to read it locally. It
loads its fonts from Google Fonts.

## Trace format

Turns are `<stateDir>/traces/<uuid>.json`, version `onechat.turn/v1`. `say` prints
the answer and trace path. Scenario reports use `onechat.scenario/v1`.

| Field | Meaning |
| --- | --- |
| `id`, `at`, `message`, `tracePath` | Turn identity, UTC start, request and artifact location. |
| `inventory`, `history`, `setups` | Exact bounded context sent to Decide, including every truncated session transcript. |
| `rawDecision` | Original structured output, including invalid fields when available. |
| `decision`, `validationErrors` | Validated actions/fallback and rejection reasons. |
| `deciderUsage`, `composeUsage` | Model, reported token/cache counts, `costUsd` and `durationMs`. |
| `actions[].action`, `sessionId`, `sentText` | Requested action, resolved target and actual submitted prompt. |
| `actions[].reply`, `state`, `error` | Real public reply, observed state and failure. |
| `actions[].receipt.local` | `binary`, `workdir`, `nativeSessionId`, `exitCode`, and Codex `sandbox`. Exit code is null if a process never started. |
| `actions[].receipt.cloud` | `baseURL`, `namespace`, `threadId`, `turnId`, `runName`, `messageIds` (including failures), append `requestId`, and optional `created`. |
| `actions[].usage`, `durationMs`, `harnessCalled`, `harness` | Reported usage and timing, dispatch attempt and configured harness. Cloud counts accepted turns, not independently proven subprocess launches. |
| `answer`, `error` | Final answer and manager-level failure. Session errors remain on their actions. |
| `totals` | Known token/cost sums, counts with evidence, manager calls and harness dispatches by kind. |
| `check: {pass, notes}` | Optional expectation result, including the matched action. |
| Scenario `startedAt`, `finishedAt` | UTC timing for the whole scenario, including failed runs with a report. |
| Scenario `setup[].scenarioLabel`, `sessionTitle` | Human-readable identity for each seed action. |
| Scenario `workspaces`, `sessions`, `setup`, `turns` | Copied workspace paths, label bindings, seed results and full turn traces. |

Unknown usage stays JSON null, including cloud usage the API does not report.
Cache counts remain separate because providers disagree about inclusion in input
tokens. Totals are partial known sums, not complete billing or normalized token
accounting. Access/refresh credentials appear only in the private session file.

## Remaining boundaries and verification

Desktop UI wiring, concurrent turns/processes sharing a state or credentials file,
and steering a running turn are not implemented. Use one CLI process at a time
per state/session file. Production sign-in is implemented; actual production
acceptance still requires an operator-run login and live scenarios.

Offline tests use fake executable scripts, signed fixture ID tokens, a custom
RoundTripper and callback `httptest.NewRecorder` handlers. They never open a
listener or invoke real models. Verification commands run one at a time:

```sh
free -m
GOFLAGS=-buildvcs=false go test ./internal/onechat/... -run '^TestOneChat' -count=1
GOFLAGS=-buildvcs=false go test ./internal/runapi/... -run '^TestOneChatNativeReply' -count=1
GOFLAGS=-buildvcs=false go vet ./internal/onechat/... ./cmd/onechat/...
GOFLAGS=-buildvcs=false go build -o /tmp/onechat ./cmd/onechat
gofmt -l internal/onechat cmd/onechat
GOFLAGS=-buildvcs=false go test ./internal/onechat/... -run '^TestOneChat(Login|LocalRecipes)' -count=1 -v
```
