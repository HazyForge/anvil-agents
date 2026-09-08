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

- Unauthenticated (no OIDC). Authentication is GitHub HMAC-SHA256
  (`X-Hub-Signature-256`).
- `receiverID` is an opaque public id, not a secret.
- Optional path token: if `spec.secretRef.pathTokenKey` is set, requests must
  also send matching `X-Anvil-Trigger-Token`.

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
