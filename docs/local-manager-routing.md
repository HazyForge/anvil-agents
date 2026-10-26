# Local conversations and the manager

Anvil Agents Desktop's Local Manager uses the same saved conversations as local
Chat. Each record is an Anvil conversation: its own ID, name, transcript, draft,
chosen harness, working folder, and native/WSL binding. This is the layer above
the harness. Continuing one replays its saved transcript into the existing local
execution adapter; it does not claim to resume a native session.

The manager first discovers the Anvil conversation inventory, shortlists relevant
subjects, asks the provider for subject and intent, and applies a separate delivery
policy. The UI shows those stages, candidate probabilities, selected conversation,
and action as they occur. Model confidence describes the classification, not an
execution permission or a guarantee that work succeeded.

## Scope and storage

The current local conversation source is this Desktop browser origin's existing
`anvil-agents-desktop.local-chat.v1` registry. Local Chat and Local Manager share
it. IDs, drafts, history, unresolved work, and runtime bindings survive migration.
Accepted Manager work and pending questions now also live in the Desktop host's
durable store. See [the persistent manager loop](local-manager-loop.md) for
browser-close continuity, recovery, and turn-level verification.
Other browser profiles/origins and conversations created directly inside external
harnesses are not discovered. This feature does not scan native transcript folders.

The wrapper is the continuity boundary. A native session ID, when observed in a
supported public harness event, is provenance only. It is not interchangeable with
the wrapper ID, a remote chat thread ID, or an AgentRun ID. This implementation
does not add a native resume or cross-harness transcript-import adapter.

Remote Manager remains the separately authenticated, PostgreSQL-backed subject
vault described in [subject-vault-routing.md](subject-vault-routing.md). Local
routing does not acquire cluster permissions. A local conversation's existing
opt-in Anvil tool connection uses its signed-in namespace and ordinary per-turn
capability bridge in direct Chat. Host-managed work does not persist that token.

## Decisions and delivery

The decision provider remains `internal/subjectrouting.Provider`. The manager
uses the same confidence and ambiguity gates as remote subject routing, with
bounded recent manager context and age information to help resolve follow-ups.
Named labels and aliases can recall an older conversation outside the recent
candidate window. Only bounded, redacted message and subject context goes to the
provider; working folders, tokens, and full native transcripts do not.

`internal/localrouting` owns the next action independently of the provider:

| Action | What happens |
| --- | --- |
| `read_status` | Read the selected wrapper's saved state without launching a harness. The UI identifies this as saved local state. |
| `continue` | Revalidate the wrapper binding, durably admit work, and send its saved transcript plus this message through the existing local adapter. |
| `new_conversation` | Create an Anvil conversation on the selected available harness, then run its first turn. |
| `general` | Handle an aside in a general conversation rather than opening a project for every message. |
| `wait` | Keep the message unsent. For a known target, the UI can offer explicit queue admission; the host checks its unresolved work and workspace before accepting or executing. |
| `clarify` | Ask the user to select a conversation or resolve unavailable runtime information. No harness is invoked. |

An explicit target choice is recorded as a user choice, without fabricated model
probabilities. The execution path rechecks conversation state before dispatch;
routing alone does not reserve a workspace. A disconnected or uncertain local
execution is not automatically retried. The persistent loop records a pending
decision when it detects failed or unconfirmed work. Direct Chat retains its
browser-bound stream and observes host-owned work guards.

## Run and evaluate

Local automatic classification is opt-in on the Desktop host:

```sh
ANVIL_DESKTOP_SUBJECT_ROUTING=1 anvil-desktop
```

The existing server-side `TYPESAFE_API_KEY` environment configuration supplies the
provider; `ANVIL_DESKTOP_SUBJECT_MODEL` optionally pins the serving model. Do not
put keys in browser storage or frontend environment variables. The host's
classifier key is excluded from delegated native-process environments.
Without a configured provider, the manager presents a manual choice. Scenario
fixtures do not require provider credentials or invoke native harnesses.

The routing bench contains curated multi-turn conversations with independent
accepted target, intent, and action labels. It exercises new work followed by
references, switching projects and harnesses, old named work, status checks,
busy/unconfirmed sessions, runtime mismatches, and ambiguous choices. Later turns
see the state produced by earlier decisions. A wrong new-conversation decision can
therefore affect the follow-up; expected labels do not manufacture that state.

Fixture runs replay separate scripted provider judgments through the real routing
and action policy. They verify orchestration and scenario bookkeeping. Live
provider runs use the same manager path and record actual judgments, probabilities,
latency, and expected-versus-observed results. Fixture pass rates are not model
accuracy. Neither bench mode performs the requested project work.

The built-in suite currently contains nine scenarios and 30 turns. The CLI and
Desktop bench call the same runner:

```sh
go run ./cmd/local-routing-bench -mode fixture -out /tmp/anvil-routing-fixture.json
go run ./cmd/local-routing-bench -mode fixture -scenarios switch-harnesses
# Explicit live provider evaluation, using the process's configured key:
go run ./cmd/local-routing-bench -mode live -model jev-latest -out /tmp/anvil-routing-live.json
```

Output files use private permissions and must not already exist. Live mode skips
the synthetic provider-failure scenario. Each turn identifies whether its judgment
came from a provider, an explicit user choice, or a missing prerequisite; a user
choice is not counted as a model prediction. A failed creation prevents dependent
turns from pretending the expected conversation exists.

Scenario input is the editable JSON in `internal/localrouting/scenarios.json`.
Scripted model outputs live separately in `fixtures.json`, so expected labels do
not control the decision. Exported correction scenarios can be supplied with
`-import PATH` in live mode. Their saved context is reused, while a previous manual
target is cleared so the model has to make a fresh choice.

The loopback API provides `GET /local/v1/manager/bench` for scenario descriptions,
`POST /local/v1/manager/bench` for `{mode, scenarioIds?, scenarios?}`, and
`POST /local/v1/manager/route` for streamed routing. The route returns the selected
action plus the redacted request snapshot and host-observed runtime needed for a
reproducible correction. Both POSTs enforce the existing same-origin JSON boundary.

The integrated browser proof uses the real Desktop HTTP endpoints and harmless
executables in temporary folders. It verifies target selection, isolated transcript
delivery, saved replies, and status requests without execution. The executables
and classifier are explicit fixtures; this is not authenticated provider or
production acceptance.

```sh
ANVIL_LOCAL_MANAGER_BROWSER=1 go test ./internal/desktop -run '^TestLocalManagerBrowser$' -count=1 -v
```

Build `web/desktop` first. The browser driver accepts `PLAYWRIGHT_MODULE` and
`PLAYWRIGHT_CHROMIUM_EXECUTABLE` for an installed Playwright runtime.
