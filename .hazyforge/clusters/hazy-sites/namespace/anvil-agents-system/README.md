# anvil-agents on hazy-sites

GitOps contract for installing the standalone Anvil Agents operator on the
`hazy-sites` cluster with two **Grok 4.5** roles:

| Profile | Intent | Cadence (when enabled) | Purpose |
| --- | --- | --- | --- |
| `hazy-sites-grok45-site-healer` | `proposeChange` | every 15m | Fix transient pod/rollout site outages |
| `hazy-sites-grok45-security-scanner` | `observe` | daily | Read-only security posture scan |

Discovery (cluster-local Argo CD):

- Helm values: `.hazyforge/clusters/hazy-sites/namespace/anvil-agents-system/deploy.yaml`
- Raw manifests: `.hazyforge/clusters/hazy-sites/namespace/anvil-agents-system/manifests/`

## Prerequisites

1. `hazy-sites` Kubernetes API healthy (`kubernetesapiready=true` in Omni).
2. Grok OAuth seed Secret `hazy-sites-grok-auth-seed` with `GROK_AUTH_JSON` and
   `GROK_AUTH_SEED_ID` (or reauth onto `AgentDataVolume/hazy-sites-grok-home`
   with `anvil-agentctl auth grok reauth`).
3. `ClusterSecretStore/azurekv-cluster-secret-store` allows namespace
   `anvil-agents-system` (see Anvil Primaris overlay for hazy-sites external-secrets).
4. GHCR read PAT already present as `anvil-primaris-ghcr-read-pat`.

## Enable schedules

Schedules start **suspended**. After a successful manual smoke:

```bash
kubectl --context hazyforge-hazy-sites -n anvil-agents-system \
  patch agentschedule hazy-sites-grok45-site-healer \
  --type=merge -p '{"spec":{"suspend":false}}'
kubectl --context hazyforge-hazy-sites -n anvil-agents-system \
  patch agentschedule hazy-sites-grok45-security-scanner \
  --type=merge -p '{"spec":{"suspend":false}}'
```

Prefer promoting those patches into this GitOps tree once proven.

## Manual smoke

```bash
anvil-agentctl run create \
  -n anvil-agents-system \
  --generate-name site-healer-smoke- \
  --profile hazy-sites-grok45-site-healer \
  --source-kind ManualRequest \
  --source-name manual-smoke \
  --intent proposeChange \
  --prompt 'Smoke: run site-health, report only, no mutations.'

anvil-agentctl run create \
  -n anvil-agents-system \
  --generate-name security-scan-smoke- \
  --profile hazy-sites-grok45-security-scanner \
  --source-kind ManualRequest \
  --source-name manual-smoke \
  --intent observe \
  --prompt 'Smoke: run security-inventory and report top findings only.'
```
