# AgentRun Observability

Agent harnesses write their transcript to the `agent` container's standard
output and standard error. The cluster observability plane owns collection,
retention, redaction, and export. This keeps log destination credentials and
failure handling outside the short-lived, potentially untrusted AgentRun Job.

The controller adds collector-neutral Kubernetes labels to every AgentRun Job
and Pod:

| Label | Meaning |
| --- | --- |
| `control.anvil.hazyforge.io/agent-run` | Sanitized AgentRun name |
| `control.anvil.hazyforge.io/agent-run-job` | Harness Job name |
| `control.anvil.hazyforge.io/agent-run-backend` | Harness backend kind |
| `control.anvil.hazyforge.io/agent-run-intent` | Effective execution intent |
| `control.anvil.hazyforge.io/agent-run-source-kind` | Opaque source kind |
| `control.anvil.hazyforge.io/agent-run-source-name` | Opaque source name |
| `control.anvil.hazyforge.io/agent-schedule` | Schedule name, when scheduled |

Collectors can discover these labels without granting the runner additional
Kubernetes access. Keep backend and intent as indexed dimensions when useful.
Run, Job, and source names are high-cardinality identities; prefer OTLP
attributes or Loki structured metadata unless the storage policy explicitly
allows indexing them.

Label values are normalized to the controller's lowercase Kubernetes-safe
form. For example, `openCode` becomes `opencode`, `hermesAgent` becomes
`hermesagent`, and `proposeChange` becomes `proposechange`.

## Grafana Alloy and OTLP

Grafana Alloy can tail Kubernetes Pod logs and bridge them into an OpenTelemetry
pipeline with `otelcol.receiver.loki`. A typical operator-owned flow is:

```text
AgentRun stdout/stderr
  -> Kubernetes Pod log
  -> Alloy loki.source.kubernetes
  -> Alloy otelcol.receiver.loki
  -> OTLP processors/exporter
  -> approved log store
```

Do not add an exporter sidecar merely to capture stdout. A sidecar cannot read
another container's stdout directly, complicates Job completion, and places
export credentials and queues in every run. Do not use the controller's bounded
status log read as a durable exporter; it exists only to recover terminal
status.

`spec.harness.execution.extraEnv` and `envSecretRefs` can configure standard
`OTEL_*` variables for a custom or natively instrumented harness. Those
variables do not convert ordinary CLI output into OTLP. Operators should fix
the allowed destination at the collector or policy plane rather than let
individual runs redirect potentially sensitive transcripts.

Agent logs can contain prompts, source context, tool output, and accidental
credential material. Apply the same retention, access, and redaction controls
used for other sensitive workload logs.
