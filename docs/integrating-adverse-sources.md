# Integrating Adverse Sources

An application does not need Anvil Primaris, Anvil Hub, or a Hazy Forge CRD to
open an adverse response stream. The public boundary has two provider-neutral
ingress modes:

- Create immutable `AdverseSignal` objects from an application, alert adapter,
  CI system, or external controller.
- Let the operator watch an exact Kubernetes GVK through an
  administrator-owned structured pull integration.

Both modes append bounded evidence to an `AdverseSituation`. The situation,
not the reporter, owns the optional `AgentRun` responder. A reporter cannot
select a harness, prompt, profile, credential, ServiceAccount, execution
identity, mutation intent, or target namespace.

## Push Signals From Any Application

First, a platform administrator creates the same-namespace destination:

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AdverseSituation
metadata:
  name: checkout-health
  namespace: store
spec:
  groupKey: application/checkout
  buffer:
    quietPeriodSeconds: 900
    dedupeWindowSeconds: 300
    maxEvents: 100
  responders:
    agentRun:
      enabled: false
```

The application or adapter creates a new signal for each observation:

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AdverseSignal
metadata:
  generateName: checkout-provider-timeout-
  namespace: store
spec:
  situationRef:
    name: checkout-health
  sourceRef:
    apiVersion: monitoring.example.io/v1
    kind: Alert
    namespace: store
    name: checkout-provider-timeouts
  sourceURL: https://monitoring.example.invalid/alerts/checkout-provider-timeouts
  dedupeKey: checkout/provider-timeout
  trigger:
    phase: Firing
    reason: ProviderTimeoutRateHigh
    message: The provider timeout rate exceeded the configured threshold.
```

`AdverseSignal.spec` is immutable. Create another signal for new evidence.
`dedupeKey` groups separate deliveries inside the situation's dedupe window;
the signal UID remains the delivery identity, so a controller retry does not
increment counters twice. Reporter timestamps are retained as evidence but do
not control quiet-window timing.

A signal is a trusted assertion that the observation is adverse. Its phase and
condition fields are evidence; the controller does not reclassify them.
Missing destinations remain `Pending` and retry. Invalid legacy objects become
terminal `Rejected`. The controller never auto-creates a signal destination.
If one deduplicated event already has 64 unacknowledged signal deliveries, or a
new event would evict an unacknowledged receipt from the bounded event ring,
new signals remain `Pending` with reason `SituationBusy` until receipt cleanup
frees capacity. Pull ingress and new sequence creation apply the same
backpressure; the controller never evicts an in-flight receipt or double-counts
its retry. A narrow controller finalizer clears any persisted receipt before a
signal deletion completes, preventing abandoned receipts from blocking the
situation.

The minimal reporter role is in
[`examples/adverse-signals/reporter-rbac.yaml`](../examples/adverse-signals/reporter-rbac.yaml).
It grants only `create` on signals. This write-only role cannot read evidence
submitted by other reporters in the namespace. If a reporter must observe
acceptance status, grant `get`, `list`, and `watch` through a separate observer
role only when namespace-wide signal visibility is acceptable. Do not grant
signal status, update, patch, delete, situation mutation, runs, profiles, or
Secrets to an ordinary reporter.

Creating a signal can activate every enabled situation in that namespace.
Kubernetes RBAC cannot restrict a `create` request by
`spec.situationRef.name`, so put applications with different trust levels in
different namespaces or enforce allowed destinations with admission policy.

Signals are append-only audit inputs and have no built-in TTL in v1alpha1.
Choose an external retention policy deliberately; deleting accepted signals
does not delete already buffered situation evidence or an `AgentRun`.

## Pull An Exact Kubernetes Resource

Use structured `adverseSources` when the source already has status in the same
cluster:

```yaml
adverseSources:
  - name: checkout-release
    apiVersion: delivery.example.io/v1
    kind: Release
    resource: releases
    namespaces:
      - store
    objectSelector:
      matchLabels:
        adverse-response: enabled
    situationRef:
      name: checkout-health
      namespace: store
    groupKey: application/checkout
    classifier:
      requireObservedGeneration: true
      observedGenerationPath: status.observedGeneration
      phasePath: status.phase
      conditionsPath: status.conditions
      adversePhases:
        - Failed
        - Blocked
      adverseConditionTypes:
        - Failed
        - ActionRequired
      detailConditionType: Ready
```

The chart passes this contract to the controller and generates `get`, `list`,
and `watch` RBAC for only `delivery.example.io/releases`. `resource` is the
exact plural API resource, while `kind` is the watched object kind. No wildcard
RBAC or consumer API import is required.

`namespaces` and `objectSelector` filter which objects may route. They do not
narrow the chart's ClusterRole. Restricted clusters should render equivalent
Roles and RoleBindings for the configured namespaces and omit the broad
ClusterRole installation.

By default, pull classification requires
`status.observedGeneration == metadata.generation`, recognizes the phases
`Failed`, `Error`, `NeedsHuman`, and `ActionRequired`, and recognizes true
conditions with types `Failed`, `NeedsHuman`, or `ActionRequired`. A false
`Ready` condition supplies details for a negative phase. Custom paths and
values let unrelated APIs use their native status vocabulary. Stale individual
conditions are ignored.

The compatibility flag
`--adverse-source-gvks=apiVersion/kind` remains available. It uses the default
classifier and routes to `adverse-default` in the source namespace; operators
must still add its read RBAC manually. New installations should prefer the
structured form. When both forms name the same GVK, structured integrations
take precedence and the legacy entry is ignored; add an explicit structured
`adverse-default` route if both destinations are intentional.

## Adapter Boundary

An adapter may be a small controller, webhook consumer, monitoring rule
receiver, or CI completion step. Its responsibility ends after normalizing
evidence into `AdverseSignal`, or exposing status for a configured pull watch.
Application authorization, delivery policy, approvals, and product-specific
remediation stay outside this repository. Event messages and source URLs are
untrusted evidence and are never treated as instructions or fetched
implicitly by the operator.
