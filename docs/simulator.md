# Local Simulator

The simulator runs a complete Anvil Agents installation on one machine with
pre-seeded agents that look and behave like real ones, without a model
provider, a repository or any credential. Use it to see working agents in the
console, take screenshots, and exercise scheduling, launch controls, failures
and human follow-up.

```bash
make simulator          # or: hack/simulator.sh up
```

Then open `http://127.0.0.1:18180/` and choose **Sign in**. The test issuer
approves the login at once.

## What runs where

| Piece | Where | Real or simulated |
| --- | --- | --- |
| Controller and CRDs | Kind cluster `anvil-agents-sim` | Real chart and image (`anvil-agents:dev`) |
| AgentRun Jobs | Kind | Real Jobs; the harness inside is simulated |
| `anvil-agents-api` and the console | Host, `127.0.0.1:18180` | Real API binary serving `web/console/dist` |
| OIDC issuer | Host, `127.0.0.1:18181` | `cmd/kind-oidc-issuer` with test-only browser login |

The API and the issuer run on the host because the console signs in through
the browser and the API must reach the same issuer URL to verify tokens; a pod
in Kind cannot reach a host loopback issuer.

TEST ONLY: the issuer approves every login and the API accepts an http issuer
(`allowInsecureIssuer`). Never expose these ports.

## Simulated agents

`examples/simulator/harness` is one small image that honours the custom
harness contract in [harnesses.md](harnesses.md): it reads the mounted prompt
and context, prints its work, emits `progress`, `decision` and `needsHuman`
status reports, and exits nonzero when its scenario fails. Every command and
output it prints is scripted.

Four harness profiles use that image and differ only in log style
(`SIM_PERSONA`): `codex-sim`, `hermes-sim` / `hermes-home-sim` (with a durable
home volume), `opencode-sim` and `pi-sim`.

Scenarios live in `examples/simulator/harness/scenarios/` as JSON. A run picks
one with a `sim-scenario: <name>` line in its prompt:

| Scenario | About | Ends |
| --- | --- | --- |
| `pr-review` | Reviews a pull request, runs tests, comments | Succeeded, about 5 min |
| `release-dry-run` | Plans a release without applying it | Succeeded, about 5 min |
| `security-sweep` | Scans images and a chart | Needs a human, about 5 min |
| `flaky-test-hunt` | Reproduces a flake, fixes it, opens a PR | Succeeded, about 3 min |
| `docs-refresh` | Small doc fix | Succeeded, about 1 min |
| `failing-build` | A build that cannot pass | Failed, about 1 min |
| `incident-triage` | Correlates logs and cluster state | Succeeded, about 14 min |
| `long-migration` | Batch job with a report per batch | Succeeded, about 30 min |

Step kinds: `say`, `think`, `cmd` (with `out`, `ok`, `ms`), `edit`, `report`,
`work` (random plausible commands from a pool for N seconds), `log` and
`exit`. Add a file to add a scenario; `hack/simulator_test.sh` must list its
expected outcome.

## Seed data

`examples/simulator/seed/library.yaml`: namespaces `agents` and
`hazy-trade-agents`, the harness profiles, seven run profiles (`pr-reviewer`,
`test-doctor`, `release-steward`, `security-auditor`, `incident-responder`,
`docs-gardener`, `migration-runner`), skill and tool sets, the
`platform-council`, a durable agent home, two schedules that keep launching
work (`security-sweep` every 15 minutes, `repo-upkeep` every 10), a paused
application (`legacy-billing`) and a concurrency cap for `hazy-trade`.
Launch controls authorize by application, so `hack/simulator.sh` grants the
operator controls on every application the seed names (its `applications`
list; `hack/simulator_test.sh` fails when one is missing).

`examples/simulator/seed/runs.yaml`: one wave of nine runs covering every
outcome, including one held by the pause. `up` starts a wave only when the
`agents` namespace has no runs.

## Commands

```bash
hack/simulator.sh up        # build, install, seed, start API + issuer (idempotent)
hack/simulator.sh wave      # start another wave of runs
hack/simulator.sh status    # list runs
hack/simulator.sh token     # print an access token (curl, Desktop, hack/stream-agent-run.sh)
hack/simulator.sh stop      # stop API + issuer; the cluster keeps running
hack/simulator.sh down      # stop everything and delete the cluster
node hack/simulator-screenshots.mjs   # sign in and capture the main console views
```

`ANVIL_AGENTS_SIM_SPEED=0.2 hack/simulator.sh up` makes every pause five times
shorter. `ANVIL_AGENTS_SIM_SKIP_BUILD=1` reuses images and binaries.
`ANVIL_AGENTS_SIM_CONSOLE_DIR=<dist>` serves another console build, such as a
branch's `web/console/dist`; run `hack/simulator.sh stop` first so `up`
restarts the API with it. State,
logs, the kubeconfig and the issuer key live in `.sim/` (git-ignored); the
user's own kubeconfig is not modified.

## Not simulated

Standing chat (needs PostgreSQL and a live harness), Desktop, external
triggers, adverse signals and the archive are not seeded yet.
