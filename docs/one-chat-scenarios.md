# One chat scenarios

One chat has to cope with anything a person sends, at any moment: a new task,
"keep going", "how's it going ?" while a session works, "wait no" mid-turn, an
answer to a question it asked, a pasted log, a dictated ramble that caught
someone else talking. This corpus pins how it should behave across every state
it can be in. A run is cheap enough to repeat after every prompt or code
change, so new mistakes show up as regressions instead of surprises.

The messages are written the way the owner really writes to agents. The style
comes from an analysis of their Claude Code, Codex and Grok histories. That
analysis and the raw messages stay private; every message here is synthetic.
Typical traits:

- mostly lowercase, few full stops, `?` after a space;
- typos, and dictated run-ons with `um` and misheard names;
- one-line follow-ups that edit the previous message ("for codex I mean",
  "I meant I do want it fancy").

## Three tiers

| Tier | What runs | Cost | When |
| --- | --- | --- | --- |
| **decide** | The real manager model decides on a seeded state. Nothing executes. | One manager call per step, a few cents | After any change to the manager prompt, the decision schema or inventory shaping |
| **flow** | The real host and manager with a scripted decider and scripted sessions | Free and deterministic | In CI, with the other Go tests |
| **live** | Real harnesses on fixture repos (`examples/onechat/scenarios/*.yaml`) | Dollars, minutes | Before a release, and to approve behavior |

Seeding is synthetic in the decide and flow tiers: sessions, transcripts,
history and states are written into the manager's state directly. No harness
runs to set the stage, so a state such as "a session has been stuck running
for two days" costs nothing to reproduce.

## Taxonomy

Every case is tagged so coverage can be measured as a grid of states × moves.
Unknown tags fail loading.

**States**: what the system looks like when the message arrives.

| State | Meaning |
| --- | --- |
| `fresh` | No sessions, no history |
| `idle` | Sessions exist; nothing running or pending |
| `overlapping` | Several sessions fit the same project (an old draft, a reviewer next to a builder) |
| `session-running` | A session is mid-run; the manager itself is idle |
| `mid-turn` | The manager is mid-turn when the message is typed |
| `asked` | The last turn ended with a question to the person (which one, may I, give me a key) |
| `failed` | The last action failed (cloud 409, local timeout) |
| `cancelled` | The person cancelled the last turn partway |
| `long-history` | The relevant context has scrolled out of the history window |
| `cloud-down` | The cloud backend cannot be reached |
| `stale-running` | A session is marked running but nothing will ever finish it |

**Moves**: what the person does with the message. The wording each move
usually takes is in the suites.

`kickoff`, `continue`, `ship`, `status`, `impatience`, `recall`, `explain`,
`complete-last`, `self-correct`, `scope-add`, `gate`, `pivot`, `stop`,
`push-back`, `question-agent`, `report-action`, `set-rule`, `delegate`,
`authority`, `money`, `short-answer`, `paste`, `noise`, `chatter`,
`life-admin`, `multi-ask`.

**Intents**: the manager's reading of the message. It is the subject routing
design's set plus `stop`, `chatter` and `preference`.

`question`, `amend_running`, `new_work`, `status`, `decide`, `unclear`,
`stop`, `chatter`, `preference`.

**Voice** (optional): `lowercase`, `space-question`, `typo`, `dictation`,
`misheard`, `terse`, `frustrated`.

## Decide suites

`examples/onechat/scenarios/decide/*.yaml`. A suite starts from a **world**
(`scenarios/worlds/*.yaml`), changes it for its state, and lists cases.

```yaml
suite: session-running
state: session-running
world: workbench
seed:                       # applied to the world for every case
  sessions:
    - label: invoice        # an existing label is patched; a new label adds a session
      state: running
      transcript+:          # "+" appends; without it the list is replaced
        - {role: user, ago: 3m, text: "Add a --json flag ..."}
  history+:
    - {role: user, ago: 3m, text: "add a --json flag to the invoice tool"}
  cloud: {error: "sign-in expired"}   # optional: the cloud backend fails
cases:
  - name: how-is-it-going
    moves: [status]
    voice: [space-question]
    say: how is it going ?
    from: ios               # optional client kind
    expect:
      intent: status
      actions: [read_status, answer]   # every action must be one of these
      does:                            # each matcher must match at least one action
        - {action: read_status, session: invoice}
      not: [send, create]
      notSessions: [lisbon]            # no send, read or create aimed at these
      notSetups: [cloud-agy]           # no create from these
    gap: ""                 # non-empty: today's system cannot do this yet
    note: why this is the right call
```

**World and seed**

- A world names the config whose setups the manager sees.
- Sessions have a `label` that expectations use; the runner gives each one a
  stable id.
- `ago` (`3m`, `2h`, `2d`) dates messages, and `updatedAgo` dates sessions,
  relative to the run.
- Merged history and transcripts are ordered by time.
- The manager sees exactly what production shows it: the last ten history
  messages and each session's last six transcript messages, shaped by the
  same code.

**Steps**

- A case is either a single `say` with `expect`, or a list of `steps`.
- A step's optional `after` scripts what happened next before the following
  step: the manager's `answer`, and session patches. Each step then decides
  against a fixed context, so one bad decision cannot cascade.
- Entries without `ago` are stamped at the step's time.

**Matchers**

- A matcher takes `action`, `session` (a label), `setup`, `mentions` and
  `anyOf`. An action matches when every field given matches.
- `mentions` are case-insensitive substrings that must all appear in the
  action's message (`send`, `create`) or text (`answer`, `ask_user`). Use
  words any good handoff would carry, such as the flag name, not phrasing.
- `anyOf` lists alternatives, at least one of which must match the same
  action.

**What gets checked**

- Expectations apply to the effective decision: what the manager would
  actually do after validation. When validation rejects a proposal (sending
  to a running session, say), the effective decision is the manager's
  fallback question, and the report shows both.
- `intent` is checked when the decision carries one.
- `stop` is a future action. A case may expect it only with `gap` set.

**Gaps**

- A case with `gap` documents behavior the system cannot produce yet.
- Gap cases run and are reported separately: "still a gap" or "now passes".
  They never fail the run.
- Closing a gap means deleting the `gap` line in the same change that adds
  the capability.

## Flows

`examples/onechat/scenarios/flows/*.yaml`: a scripted manager (decisions and
answers keyed by message), scripted sessions (delay, reply, failure), and
steps a person can take at any moment:

- start a turn from a client;
- start one while another runs;
- cancel;
- disconnect and reconnect;
- add a second client;
- stop reading;
- refresh sessions;
- make the cloud fail.

Flows check events, final status, session states and timing. They run with
the host's Go tests. Gap flows describe the mid-turn behavior one chat should
have:

- a message typed during a turn is queued and runs next;
- a stop message cancels the turn at once;
- a status question is answered from live state without waiting;
- a cancelled session can take the next message;
- a failed cloud backend does not block local work.

## Capturing real mistakes

The synthetic corpus cannot foresee everything. When one chat routes a real
message wrongly, turn that turn into a case:

```sh
onechat --config FILE scenario capture --turn <turn id> --expect send:invoice --note "the shader is the site" --out ~/.config/onechat/scenarios/captured/
```

The captured case seeds the turn's real inventory and history, so it holds
private data. It lives under `~/.config/onechat/scenarios/` and is never
committed. Captured cases run with the same runner, next to the public corpus.

## Running

```sh
onechat --config examples/onechat/onechat.scenarios.yaml scenario decide \
  --suites examples/onechat/scenarios/decide --out /tmp/onechat-decide [--only 'idle/*'] [--repeat 3]
onechat scenario coverage --suites examples/onechat/scenarios/decide
GOFLAGS=-buildvcs=false go test ./internal/onechat/host/ -run TestScenarioFlows -count=1
```

**What decide writes**

- `report.json`: per step, the raw and effective decision, validation
  errors, each expectation's result, usage and cost.
- `summary.md`: pass rates by state and move, flaky cases, gaps and their
  status.

**Repeating**

- `--repeat N` asks each step N times, because the model is not
  deterministic.
- A case is stable at a pass rate of 0.9 or more over N runs; anything lower
  is a finding.
