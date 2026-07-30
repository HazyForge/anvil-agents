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
| `run list` | `list` on `agentruns` in the selected scope |
| `run get` | `get` on the selected `agentrun` |
| `run logs` | `get` on the run, referenced Job and Pod, plus `get` on `pods/log` |
| `run debug` | `get` on the run, referenced Job and Pod, plus `list` on Events |
| `auth codex\|openai diagnose` | `get` on secrets and agentdatavolumes; `list` on agentauthsessions |
| `auth codex\|openai reauth` | `create` staging secrets and agentauthsessions; `get` on volumes/secrets/sessions |
| `auth codex\|openai logout` | `create`/`get` agentauthsessions; `get` agentdatavolumes |
| `auth codex\|openai verify` | `create`/`get` agentauthsessions; `get` agentdatavolumes |
| `auth grok\|xai diagnose` | same as codex diagnose for xAI/Grok durable homes |
| `auth grok\|xai reauth` | same as codex reauth; staging key is `GROK_AUTH_JSON` |
| `auth grok\|xai logout` | same as codex logout for Grok durable homes |
| `auth openclaw\|claw diagnose` | same as codex diagnose; OpenClaw profile-store summary only |
| `auth openclaw\|claw reauth` | same as codex reauth; staging key is `OPENCLAW_AUTH_PROFILES_JSON`; requires `--agent-id` and `--model-provider` |
| `auth openclaw\|claw logout` | same as codex logout; requires `--agent-id` and `--model-provider` |
| `auth openclaw\|claw verify` | non-mutating session; requires `--agent-id` and `--model-provider`; no staging Secret |
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

## Provider Auth Maintenance (Codex, Grok, OpenClaw)

Durable provider homes keep OAuth material on an `AgentDataVolume` PVC. The
runner only seeds from the bootstrap Secret when the file is missing or the
operator deliberately changes the opaque seed id. Refreshing a Secret alone does
not overwrite a stale durable login, which is why manager Jobs can keep dying
with 401 after an ExternalSecret or vault update.

| Provider CLI | Aliases | Staging / seed key | Durable path on the volume |
| --- | --- | --- | --- |
| `auth codex` | `openai` | `CODEX_AUTH_JSON` + `CODEX_AUTH_SEED_ID` | `$CODEX_HOME/auth.json` (usually `/codex-home/auth.json`) |
| `auth grok` | `xai`, `grokBuild` | `GROK_AUTH_JSON` + `GROK_AUTH_SEED_ID` | `$GROK_HOME/auth.json` under the Grok home (usually `/opt/anvil/grok-build/.grok/auth.json`) |
| `auth openclaw` | `claw` | `OPENCLAW_AUTH_PROFILES_JSON` + `OPENCLAW_AUTH_SEED_ID` | OpenClaw per-agent auth profile store under the strictly registered `agentDir` from volume-owned `openclaw.json`; not a concatenated DB path |

`verify` is append-only and non-mutating: it waits for the volume to be idle and
runs a provider-native status check without staging secrets or seed markers.
`reauth` stages credentials; `logout` and `verify` forbid staging Secret and
`seedID` intent. Every auth-targeted `AgentDataVolume` must set `spec.backend`
explicitly; a `VolumeProfile` supplies storage shape but not backend identity.

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

For pure **apiKey** mode on Grok/Pi, mount `XAI_API_KEY` (or provider keys)
through `envSecretRefs`. Durable OAuth reauth is only required when the harness
uses the durable OAuth home. Diagnose still reports whether an API key key is
present on the bootstrap Secret.

### OpenClaw

OpenClaw is the auth **provider identity**; `spec.authMode` / `--auth-mode`
records `oauth` or `apiKey`. **OAuth is the current operational path.** API-key
profile import is structurally supported (`--auth-mode apiKey` with a valid
version=1 `api_key` profile store) but **no API key is provisioned into any
manifest**. Grok Build `~/.grok/auth.json` is **incompatible** with OpenClaw;
`--auth-file` must be a sanitized OpenClaw profile-store JSON (not
`openclaw.json` and not a SQLite database).

`--agent-id` and `--model-provider` are required for OpenClaw session operations
and must match the harness `openClaw.agentId` and model provider. The provider
binding prevents an unrelated OpenAI OAuth profile from satisfying an xAI auth
receipt. The maintenance Job resolves that agent's exact `agentDir` through a
strict, symlink-safe parse of volume-owned `openclaw.json`; it does not launch
the OpenClaw CLI or mutable plugins while staged credentials are present. It
writes only that agent's canonical auth store through the OpenClaw plugin SDK
(`saveAuthProfileStore`).
The reauth input is the complete replacement store for that selected agent;
include every profile that must remain. It never edits global
`openclaw.json` auth metadata or another agent's SQLite store.

```bash
anvil-agentctl auth openclaw diagnose \
  -n agents \
  --data-volume openclaw-home \
  --agent-id anvil \
  --model-provider xai \
  --auth-file ./openclaw-auth-profiles.json

anvil-agentctl auth claw verify \
  -n agents \
  --data-volume openclaw-home \
  --agent-id anvil \
  --model-provider xai

anvil-agentctl auth openclaw reauth \
  -n agents \
  --data-volume openclaw-home \
  --agent-id anvil \
  --model-provider xai \
  --auth-mode oauth \
  --auth-file ./openclaw-auth-profiles.json

anvil-agentctl auth claw logout \
  -n agents \
  --data-volume openclaw-home \
  --agent-id anvil \
  --model-provider xai \
  --confirm-volume openclaw-home
```

`reauth` refuses bootstrap Secrets that look ExternalSecret-managed so the CLI
does not race ESO. Point `--bootstrap-secret` at a CLI-owned seed Secret, or
omit it and only rewrite the durable home. Credential bytes never appear in the
`AgentAuthSession` spec, status, or CLI args. Diagnose and reauth summarize
profile stores without printing tokens or keys.

Logout clears durable provider auth for the selected agent (or codex/grok home)
and does not claim remote session revocation. Codex/Grok runner tombstones
block their bootstrap-secret reseeding. OpenClaw writes a logout receipt marker,
but its runner does not mount a profile-store bootstrap Secret, so there is no
automatic profile reseed path for that marker to block:

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
