# AgentRun CLI

`anvil-agentctl` is the standalone command-line client for Anvil Agents. Its
name is intentionally distinct from Anvil Primaris `anvilctl`: it imports only
the APIs in this repository and does not require Anvil Primaris or Anvil Hub.
Built-in runner images ship the same binary for in-pod status reporting; binary
presence grants no Kubernetes authority.

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
| `submit` | `create` on `agentruns` |
| `profile list` | `list` on `agentrunprofiles` in the selected scope |
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

## Submit An Idea To Any Profile

`submit` is the ergonomic idea-ingestion entry point: one prompt, any
same-namespace profile, and the run carries the operator's intent. It wraps
`run create` with sensible defaults so an idea can go straight into the system
without assembling source and name metadata by hand.

```bash
anvil-agentctl submit "Add a dry-run flag to the CLI" \
  -n agents \
  --profile feature-triage
```

The first positional argument is the prompt; `--prompt` and `--prompt-file`
(including `-` for stdin) work exactly like `run create`. `--profile` is
required and can name any AgentRunProfile in the namespace; the controller
resolves the rest of the composition from that profile.

The command fills in the bookkeeping automatically:

- `--generate-name` defaults to `<profile>-`, so the created run gets a unique
  server-generated suffix (`feature-triage-abc12`).
- The run intent defaults to `proposeChange` (an idea is a proposed change);
  override it with `--intent observe|fixTransient|proposeChange|cleanup`.
- The source is recorded as `CLITicket/<slug-of-the-prompt>`, so `run list`
  makes CLI submissions easy to spot. `--source-kind` and `--source-name`
  override the defaults.

After creation the command prints the created run and a ready-to-paste follow
command:

```text
agentrun.control.anvil.hazyforge.io/feature-triage-abc12
Watch progress: anvil-agentctl run logs -n agents feature-triage-abc12 --follow
```

### Turn An Idea Into A Ticket

Pass `--ticket-repository OWNER/REPO` to instruct the run to create a GitHub
issue that captures the idea:

```bash
anvil-agentctl submit "Stream live run events to the console" \
  -n agents \
  --profile feature-triage \
  --ticket-repository HazyForge/anvil-agents
```

This configures the run's `issueTracking` (provider `github`, repository
`OWNER/REPO`, update policy `Triage`) and appends an explicit TICKET REQUEST
block to the prompt. The run searches for an existing issue with the same
intent first and reports the created or matched issue number in its final
status. The selected profile must supply the GitHub tooling (`gh` and a
`GH_TOKEN` credential); `submit` only sets the tracking contract and the
request, it never grants credentials.

Client dry-run renders the full AgentRun without contacting Kubernetes:

```bash
anvil-agentctl submit "Stream live run events" \
  -n agents --profile feature-triage \
  --ticket-repository HazyForge/anvil-agents \
  --dry-run=client -o yaml
```

## Discover Profiles

`profile list` shows the same-namespace AgentRunProfiles that `submit` and
`run create` can target, including each profile's declared intent, backend,
harness profile, and opaque application scope:

```bash
anvil-agentctl profile list -n agents
anvil-agentctl profile list -A
anvil-agentctl profile list -n agents -o json
```

When `--namespace` is omitted it uses the namespace selected by the current
kubeconfig context, matching `run list`.

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
