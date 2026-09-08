# External triggers (GitHub webhooks)

`AgentExternalTrigger` is a namespaced CRD that receives signed GitHub webhooks
and creates or annotates `AgentRun` objects the same way an `AgentSchedule`
creates work from a timer.

## Feature gate

Inbound webhooks and the console Library surface are **disabled by default**.

```yaml
api:
  enabled: true
  config:
    externalTriggers:
      enabled: true
```

When disabled, `POST /api/v1/external-triggers/...` returns `404`, and
`GET /ui-config.json` reports `"externalTriggers":{"enabled":false}`.

## URL shape

After the controller assigns `status.receiverID`, the path-only webhook URL is:

```text
POST /api/v1/external-triggers/{namespace}/{name}/{receiverID}
```

The public URL (hostname + path, no secrets) is recorded on
`status.httpRoute.publicURL` when the controller owns a Gateway API HTTPRoute:

```text
https://agents.example.com/api/v1/external-triggers/{namespace}/{name}/{receiverID}
```

- Unauthenticated (no OIDC). Authentication is GitHub HMAC-SHA256
  (`X-Hub-Signature-256`).
- `receiverID` is an opaque public id, not a secret.
- Optional path token: if `spec.secretRef.pathTokenKey` is set, requests must
  also send matching `X-Anvil-Trigger-Token`.

## Gateway API HTTPRoute ownership

Operators should not hand-write an HTTPRoute per trigger. When
`api.config.externalTriggers.enabled` is true **and** Gateway parent config is
present, the controller creates one `gateway.networking.k8s.io/v1` HTTPRoute
in the trigger namespace:

- Name: `{trigger}-webhook` (DNS-label truncated via the same child-name helper
  as Jobs)
- OwnerReference: the `AgentExternalTrigger` (delete garbage-collects the route)
- Path match: `Exact` `/api/v1/external-triggers/{namespace}/{name}/{receiverID}`
- Backend: the `anvil-agents-api` Service
- Parent Gateway refs and hostnames come from install config. Empty
  `api.externalTriggerHTTPRoute.parentRefs` / `hostnames` inherit
  `api.httpRoute`. Each parent must set `sectionName`; hostnames must be exact
  (never wildcards).
- Optional `spec.httpRoute.hostname` overrides the install hostnames for that
  trigger only, still exact and non-wildcard.
- Suspended or Blocked triggers detach the HTTPRoute so it is not left as a
  live public route. The API also rejects those deliveries.
- The chart does not create Gateway objects. HTTPRoute is the owned CRD.
- Cross-namespace backends (trigger namespace ≠ API Service namespace) get a
  chart-managed `ReferenceGrant` for namespaces listed in
  `api.externalTriggerHTTPRoute.backendFromNamespaces`, falling back to
  `api.config.ui.defaultNamespaces`.

Fail closed: if HTTPRoute ownership is enabled and parentRefs or hostnames are
missing or contain wildcards, Helm render fails and the controller does not
create a route.

If the chart API HTTPRoute still matches `PathPrefix /` on the same hostname,
that catch-all can still reach the webhook path at the application layer. The
controller-owned route is the dedicated public webhook route; suspend/block
always delete it, and the API still returns conflict for those phases.

### Install values

```yaml
api:
  enabled: true
  config:
    externalTriggers:
      enabled: true
  httpRoute:
    enabled: true
    parentRefs:
      - name: public
        namespace: gateway-system
        sectionName: https
    hostnames:
      - agents.example.com
  # Optional dedicated webhook parents/hostnames. Empty inherits api.httpRoute.
  externalTriggerHTTPRoute:
    enabled: false
    parentRefs: []
    hostnames: []
    backendFromNamespaces:
      - agents
```

### Generated HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: github-push-operator-webhook
  namespace: agents
  labels:
    app.kubernetes.io/managed-by: anvil-agents
    app.kubernetes.io/name: anvil-agents-external-trigger
    app.kubernetes.io/component: external-trigger-httproute
    control.anvil.hazyforge.io/agent-external-trigger: github-push-operator
  ownerReferences:
    - apiVersion: control.anvil.hazyforge.io/v1alpha1
      kind: AgentExternalTrigger
      name: github-push-operator
      controller: true
spec:
  parentRefs:
    - name: public
      namespace: gateway-system
      sectionName: https
  hostnames:
    - agents.example.com
  rules:
    - matches:
        - path:
            type: Exact
            value: /api/v1/external-triggers/agents/github-push-operator/a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4
      backendRefs:
        - name: anvil-agents-api
          namespace: anvil-agents-system
          port: 8082
```

Status on the trigger records `httpRoute.name`, `httpRoute.publicURL`, and
observed `accepted` / `programmed` conditions. Secret bytes never appear.

## Secret handling

`spec.secretRef` names a same-namespace Secret. The API reads Secret **values**
only in-process to verify signatures. Status, logs, and console responses
expose Secret **name/key only** — never Secret bytes. Fail closed if the Secret
or key is missing.

Default HMAC key: `webhookSecret`.

## Targets

Each trigger has one or more same-namespace targets:

| Kind | Behavior |
|------|----------|
| `AgentRunProfile` | Create one `AgentRun` with `profileRef` set |
| `AgentCouncil` | Create one `AgentRun` per member (`councilDelivery: allMembers`) or only the member whose `role` matches `councilDelivery` |
| `AgentRun` | If the named run exists and is non-terminal, annotate it with the delivery summary (spec is immutable; Jobs are not reopened). If missing or terminal, create a successor copying `profileRef` |

Created runs use:

- `purpose: manual` (no new purpose enum)
- `sourceRef.kind: AgentExternalTrigger`
- `trigger.reason: GitHubWebhookDelivery`
- Bounded prompt from `promptTemplate` placeholders:
  `{{eventType}}`, `{{repository}}`, `{{deliveryID}}`, `{{summary}}`

## Idempotency and limits

- GitHub `X-GitHub-Delivery` is recorded in a bounded `status.seenDeliveryIDs`
  ring. Retries are no-ops.
- `concurrencyPolicy` defaults to `Forbid` (skip while prior trigger-created
  runs are active).
- Optional `maxDeliveriesPerDay` (UTC).

## Payload bounds

Request bodies are capped (256 KiB). Only a short sanitized summary is persisted
on the AgentRun prompt/trigger message — not the full raw webhook body.

## Non-goals

- Non-GitHub sources (Slack, generic HTTP)
- Standing chat / Conversation CRDs
- Merging pull requests or deleting GitOps objects
- Reopening Jobs for live AgentRuns
