# AgentRun CLI

`anvil-agentctl` is the standalone command-line client for Anvil Agents. Its
name is intentionally distinct from Anvil Primaris `anvilctl`: it imports only
the APIs in this repository and does not require Anvil Primaris or Anvil Hub.
Built-in runner images ship the same binary for in-pod status reporting; binary
presence grants no Kubernetes authority.

## Ownership versus Anvil Primaris `anvilctl`

| Concern | Tool | Auth |
| --- | --- | --- |
| Human/admin Kubernetes fleet ops | **`anvil-agentctl`** (this CLI) | kubeconfig + RBAC |
| Application launch gates (`AgentRunControl`) | **`anvil-agentctl control`** | kubeconfig + RBAC |
| Schedule list / suspend / resume / run-now | **`anvil-agentctl schedule`** | kubeconfig + RBAC |
| Chain list / suspend / resume / start | **`anvil-agentctl chain`** | kubeconfig + RBAC |
| Run create / list / logs / debug | **`anvil-agentctl run`** | kubeconfig + RBAC |
| Durable-home auth reauth | **`anvil-agentctl auth`** | kubeconfig + RBAC |
| Manager mutations under Application policy | **Anvil Hub agent-management HTTP API** (private Primaris) | SPIFFE / Hub session |
| Static `.hazyforge/agents` PR proposals | Hub + `AgentConfigurationChange` | Hub / policy-gated SA |

There is **no** `anvilctl agent` command on private Primaris `anvilctl`. Product
managers call Hub; human kube fleet ops use this CLI. Open-source installs
never need Primaris `anvilctl`.

The first client transport talks directly to Kubernetes using the caller's
normal kubeconfig loading rules and RBAC. Build it from a checkout with:

```bash
go build -o ./anvil-agentctl ./cmd/anvil-agentctl
```

Set `KUBECONFIG` normally, or put global overrides before the command:

```bash
anvil-agentctl --kubeconfig ./cluster.yaml --context agents-prod run list -A
```

The optional OIDC AgentRun API remains read-only. A future client transport can
use it for `list`, `get`, and `logs`, but it must not turn that read-only
process into a run-creation or policy-broker service.

The caller needs only the Kubernetes verbs used by each command:

| Command | Kubernetes access |
| --- | --- |
| `run create` | `create` on `agentruns` |
| `run list` | `list` on `agentruns` in the selected scope |
| `run get` | `get` on the selected `agentrun` |
| `run logs` | `get` on the run, referenced Job and Pod, plus `get` on `pods/log` |
| `run debug` | `get` on the run, referenced Job and Pod, plus `list` on Events |
| `auth codex\|openai diagnose` | `get` on secrets and agentdatavolumes; `list` on agentauthsessions |
| `auth codex\|openai reauth` | `create` staging secrets and agentauthsessions; `get` on volumes/secrets/sessions |
| `auth codex\|openai logout` | `create`/`get` agentauthsessions; `get` agentdatavolumes |
| `auth grok\|xai diagnose` | same as codex diagnose for xAI/Grok durable homes |
| `auth grok\|xai reauth` | same as codex reauth; staging key is `GROK_AUTH_JSON` |
| `auth grok\|xai logout` | same as codex logout for Grok durable homes |
| `volume copy` | create an append-only `AgentDataVolumeCopy` (stream source claim to a new volume on a target node) |
| `control list` | `list` on cluster-scoped `agentruncontrols` |
| `control get` | `get` on the selected `agentruncontrol` |
| `control pause` | `create` or `update` on the selected `agentruncontrol` |
| `control resume` | `update` on the selected `agentruncontrol` |
| `schedule list` | `list` on `agentschedules` in the selected scope |
| `schedule get` | `get` on the selected `agentschedule` |
| `schedule suspend` | `update` on the selected `agentschedule` (`spec.suspend=true`) |
| `schedule resume` | `update` on the selected `agentschedule` (`spec.suspend=false`) |
| `schedule run-now` | `update` on the selected `agentschedule` (run-now annotation) |
| `chain list` | `list` on `agentchains` in the selected scope |
| `chain get` | `get` on the selected `agentchain` |
| `chain suspend` | `update` on the selected `agentchain` (`spec.suspend=true`) |
| `chain resume` | `update` on the selected `agentchain` (`spec.suspend=false`) |
| `chain start` | `update` on the selected `agentchain` (chain-start-now annotation) |
| `chain cancel` | `update` on the selected `agentchain` (chain-cancel-instance annotation) |
| `self report` | none (writes local status JSONL / pod log only) |

## Create An Append-Only Run

Create a run from a same-namespace profile and one-off prompt:

```bash
anvil-agentctl run create \
  --namespace agents \
  --generate-name repository-review- \
  --profile repository-review \
  --source-kind ManualRequest \
  --source-name issue-42 \
  --intent observe \
  --prompt "Review issue 42 against the current repository and report evidence."
```

Without `--dry-run`, the command submits exactly one Kubernetes `create`
request. It never applies, patches, or replaces an existing AgentRun. A name
collision fails and must be resolved by choosing a new name, preserving the
append-only execution record.

Use `--name` for an exact identity or `--generate-name` for a unique
server-generated suffix. Exactly one is required. `--namespace`, `--profile`,
`--source-name`, and either `--prompt` or `--prompt-file` are also required.
`--source-kind` defaults to `ManualRequest`; source API version, namespace,
UID, and generation flags remain opaque audit metadata.

Client dry-run needs no cluster connection and can render either YAML or JSON:

```bash
anvil-agentctl run create \
  -n agents \
  --name repository-review-001 \
  --profile repository-review \
  --source-name issue-42 \
  --prompt-file ./prompt.md \
  --dry-run=client \
  -o yaml

printf '%s\n' "Inspect the failing release gate." | \
  anvil-agentctl run create \
    -n agents \
    --generate-name release-gate- \
    --profile release-review \
    --source-name manual-release-check \
    --prompt-file - \
    --dry-run=client \
    -o json
```

`--purpose` accepts `manual`, `adverseSituation`, or
`scheduledHealthCheck`. `--intent` is an optional explicit override accepting
`observe`, `fixTransient`, `proposeChange`, or `cleanup`; omit it to retain the
profile's declared intent.

## List And Inspect Runs

```bash
anvil-agentctl run list -n agents
anvil-agentctl run list -A
anvil-agentctl run list -n agents -o json

anvil-agentctl run get -n agents repository-review-abc12
anvil-agentctl run get -n agents repository-review-abc12 -o yaml
```

When `--namespace` is omitted, these commands use the namespace selected by
the current kubeconfig context. `run get` summarizes phase, backend, intent,
source, child references recorded in status, conditions, structured reports,
composition digests, and terminal errors. Use YAML or JSON output when the
bounded `.status.output` field is needed without the fuller debug view.

## Read Verified Logs

```bash
anvil-agentctl run logs -n agents repository-review-abc12
anvil-agentctl run logs -n agents repository-review-abc12 --tail 1000 --follow
anvil-agentctl run logs -n agents repository-review-abc12 --follow --pod-timeout 3m
```

The command does not select an arbitrary Pod or container. It resolves the
Pod recorded in AgentRun status, verifies the same-namespace
AgentRun-to-Job-to-Pod controller-owner chain and labels, then reads the fixed
`agent` container. The default tail is 200 lines. Use `--tail=-1` for all logs
still retained by Kubernetes. `--follow` waits up to two minutes by default for
the controller to publish a runner Pod and for that Pod's logs to become
available; `--pod-timeout` changes that bound. Only pending or not-yet-created
Pod/log states are retried. Ownership failures return immediately.

Kubernetes opens the Pod log subresource by name and offers no UID
precondition. The CLI verifies the Pod UID and owner chain immediately before
opening logs, but a subject able to replace controller-owned Pods can race that
check and inject log content. Treat Pod replacement authority as log-injection
authority.

## Aggregate Debug Evidence

```bash
anvil-agentctl run debug -n agents repository-review-abc12
```

`run debug` combines:

- AgentRun phase, status conditions, structured reports, composition digests,
  terminal error, and bounded status output;
- the referenced Job only after its namespace, label, controller kind, name,
  and UID match the AgentRun;
- the referenced Pod only after it belongs to that verified Job and contains
  the fixed `agent` container;
- Kubernetes Events for the AgentRun and only those child UIDs that passed
  verification; and
- one likely-cause summary selected from explicit run errors, false
  conditions, container state, Job failure, and warning Events.

This command makes current Kubernetes evidence easier to inspect but is not a
durable transcript. AgentRun status retains bounded output and reports, and
Kubernetes can expire Pod logs and Events. Send logs to a durable external
store when exact historical model and tool-call investigation is required.
Table, summary, and debug views escape terminal control characters from
untrusted status and Event text. `run logs` deliberately preserves the raw log
stream; redirect or inspect it in a non-terminal parser when the runner is not
trusted.

## Provider Auth Maintenance (OpenAI Codex and xAI Grok)

Durable provider homes keep OAuth `auth.json` on an `AgentDataVolume` PVC. The
runner only seeds from the bootstrap Secret when the file is missing or the
operator deliberately changes the opaque seed id. Refreshing a Secret alone does
not overwrite a stale durable login, which is why manager Jobs can keep dying
with 401 after an ExternalSecret or vault update.

| Provider CLI | Aliases | Staging / seed key | Durable path on the volume |
| --- | --- | --- | --- |
| `auth codex` | `openai` | `CODEX_AUTH_JSON` + `CODEX_AUTH_SEED_ID` | `$CODEX_HOME/auth.json` (usually `/codex-home/auth.json`) |
| `auth grok` | `xai`, `grokBuild` | `GROK_AUTH_JSON` + `GROK_AUTH_SEED_ID` | `$GROK_HOME/auth.json` under the Grok home (usually `/opt/anvil/grok-build/.grok/auth.json`) |

### OpenAI Codex

```bash
codex login --device-auth

anvil-agentctl auth codex diagnose \
  -n agents \
  --data-volume hazy-trade-codex-home \
  --bootstrap-secret codex-credentials-seed \
  --auth-file ~/.codex/auth.json

anvil-agentctl auth codex reauth \
  -n agents \
  --data-volume hazy-trade-codex-home \
  --bootstrap-secret codex-credentials-seed \
  --auth-file ~/.codex/auth.json
```

### xAI Grok Build

```bash
# Complete local Grok / xAI OAuth so ~/.grok/auth.json is fresh.
anvil-agentctl auth grok diagnose \
  -n agents \
  --data-volume hazy-trade-grok-home \
  --bootstrap-secret grok-credentials-seed \
  --auth-file ~/.grok/auth.json

anvil-agentctl auth xai reauth \
  -n agents \
  --data-volume hazy-trade-grok-home \
  --bootstrap-secret grok-credentials-seed \
  --auth-file ~/.grok/auth.json
```

For pure **apiKey** mode, mount `XAI_API_KEY` (Grok/Pi/OpenClaw) or provider keys
through `envSecretRefs`. Durable OAuth reauth is only required when the harness
uses the durable `auth.json` home. Diagnose still reports whether an API key key
is present on the bootstrap Secret.

`reauth` refuses bootstrap Secrets that look ExternalSecret-managed so the CLI
does not race ESO. Point `--bootstrap-secret` at a CLI-owned seed Secret, or
omit it and only rewrite the durable home. Credential bytes never appear in the
`AgentAuthSession` spec, status, or CLI args.

Logout clears durable `auth.json`, writes a logout tombstone that blocks secret
reseeding, and does not claim remote session revocation:

```bash
anvil-agentctl auth codex logout \
  -n agents \
  --data-volume hazy-trade-codex-home \
  --confirm-volume hazy-trade-codex-home

anvil-agentctl auth grok logout \
  -n agents \
  --data-volume hazy-trade-grok-home \
  --confirm-volume hazy-trade-grok-home
```

While a non-terminal `AgentAuthSession` targets a volume, new AgentRuns that
mount it stay Pending with reason `AuthSessionActive`. Sessions never kill
active Jobs.

Exit code `3` means diagnose finished and found an unhealthy state. Exit code
`2` is usage; `1` is operational failure.

## Launch Controls

`AgentRunControl` is a cluster-scoped launch gate for one opaque Application
scope. Active, non-expired `Paused` controls are additive: any one of them
blocks new application-scoped AgentRun Jobs. A pause never terminates an
already-created Job.

List and inspect controls:

```bash
anvil-agentctl control list
anvil-agentctl control list --application hazy-trade -o yaml
anvil-agentctl control get hazy-trade -o summary
```

Preview and then apply a bounded pause (server-side apply is not used; the
command creates the control or updates its spec in place):

```bash
anvil-agentctl control pause \
  --application hazy-trade \
  --control-name hazy-trade \
  --for 4h \
  --reason "Maintainer requested a bounded review window" \
  --source-name MDExOlJlZExhYmVsZWRFdmVudDEyMw== \
  --source-url https://github.com/HazyForge/hazy-trade/pull/123 \
  --dry-run client -o yaml

anvil-agentctl control pause \
  --application hazy-trade \
  --for 4h \
  --reason "Maintainer requested a bounded review window"
```

`--control-name` defaults to the Application name and isolates one authority's
hold; use `--for` for a bounded window (default `4h`) or `--indefinite` for an
explicit human-owned hold. `--reason` is required, and `spec.source` records
immutable event or directive metadata for deduplication (authorization still
comes from the authenticated Kubernetes caller, not the source fields).

Resume only the control owned by the issuing authority. Creating an `Allowed`
control cannot override an active, non-expired `Paused` control, so resume
updates the existing control instead:

```bash
anvil-agentctl control resume \
  --application hazy-trade \
  --control-name hazy-trade \
  --reason "Maintainer approved launches after review"
```

`--all-controls` clears every active hold for the Application and is a
human-only break-glass action; a manager or automation must never use it to
erase another authority's pause. A bounded pause recovers automatically at
`spec.expiresAt`. After any mutation, re-run `control list` to verify the
`Paused`/`Allowed` phase and the affected schedule count before treating the
fleet as quiescent.

## Schedules

`AgentSchedule` owns cadence. Application-scoped launch holds (`AgentRunControl`)
block Job launches without suspending the schedule object. Use schedule
suspend when you need the schedule itself stopped independently of the launch
gate.

```bash
anvil-agentctl schedule list -A
anvil-agentctl schedule list -n hazy-trade --application hazy-trade
anvil-agentctl schedule get hazy-trade-production-auditor -n hazy-trade -o summary

anvil-agentctl schedule suspend hazy-trade-backlog-worker-1h \
  -n hazy-trade \
  --reason "Operator paused Hazy Trade agent schedules"

anvil-agentctl schedule resume hazy-trade-backlog-worker-1h \
  -n hazy-trade \
  --reason "Operator resumed Hazy Trade agent schedules"

anvil-agentctl schedule run-now hazy-trade-production-auditor \
  -n hazy-trade \
  --template primary
```

`schedule suspend|resume` require `--reason` and record
`control.anvil.hazyforge.io/pause-reason` plus
`control.anvil.hazyforge.io/pause-changed-at` for audit. `schedule run-now`
writes a new `control.anvil.hazyforge.io/run-now` token (and optional
`control.anvil.hazyforge.io/run-template`) so the controller creates one
replay-safe immediate child without rewriting history.

Static GitOps-owned schedules remain source-of-truth for create/update of
templates and intervals; the CLI mutates live suspend and nudge fields only.

## Chains

`AgentChain` owns **completion-driven** sequential `AgentRun`s (not wall-clock
rotation). See `docs/agent-chain.md`.

```bash
anvil-agentctl chain list -n anvilhub
anvil-agentctl chain get lab-evidence-loop -n anvilhub -o summary
anvil-agentctl chain start lab-evidence-loop -n anvilhub
anvil-agentctl chain cancel lab-evidence-loop -n anvilhub --instance '*'
anvil-agentctl chain suspend lab-evidence-loop -n anvilhub --reason "hold"
anvil-agentctl chain resume lab-evidence-loop -n anvilhub --reason "resume"
```

`chain start` writes a new `control.anvil.hazyforge.io/chain-start-now` token so
the controller begins one instance at step 0. `chain cancel` resolves `*` to the
exact current instance, stops further advancement, and retains Forbid ownership
until the active run becomes terminal without deleting its Job. Step advancement
is controller authority on terminal phases; runners do not create peer runs,
and `purpose=chained` is rejected on public `run create`.

## In-Pod Status Reporting

Inside a runner Job the same binary can record structured status without any
kubeconfig:

```bash
anvil-agentctl self report progress --stage tool-setup --summary "Tools ready."
anvil-agentctl self report needsHuman --stage harness-auth --summary "Re-auth required."
```

This writes the existing JSONL status file and `ANVIL_AGENT_RUN_STATUS_JSON=`
log lines. It never patches `AgentRun/status`. The historical
`anvil-agent-status` shell wrapper remains for compatibility.
