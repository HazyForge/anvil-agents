# AgentRun CLI

`anvil-agentctl` is the standalone command-line client for Anvil Agents. Its
name is intentionally distinct from Anvil Primaris `anvilctl`: it imports only
the APIs in this repository and does not require Anvil Primaris or Anvil Hub.

The first client transport talks directly to Kubernetes using the caller's
normal kubeconfig loading rules and RBAC. Build it from a checkout with:

```bash
go build -o ./anvil-agentctl ./cmd/anvil-agentctl
```

Set `KUBECONFIG` normally, or put global overrides before `run`:

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
```

The command does not select an arbitrary Pod or container. It resolves the
Pod recorded in AgentRun status, verifies the same-namespace
AgentRun-to-Job-to-Pod controller-owner chain and labels, then reads the fixed
`agent` container. The default tail is 200 lines. Use `--tail=-1` for all logs
still retained by Kubernetes.

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
