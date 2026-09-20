# Constrained Primaris ATE overlay (do not stock-install)

**Do not** `helm upgrade --install` stock Agent Substrate (ATE) 0.0.8
cluster-wide. Stock `atelet` is a privileged DaemonSet with `hostPort` 8085
and 9090, `hostPath` `/var/lib/ateom-gvisor`, and **no Helm `nodeSelector`**.
That would land on every Primaris node.

This overlay is GitOps-shaped **source** in `anvil-agents` (not under
`.hazyforge/clusters/anvil-primaris/namespace/`, which the Primaris
ApplicationSet applies). It is files only until an operator:

1. Renders with rotated RustFS keys.
2. Confirms every DaemonSet/Deployment/StatefulSet/Job is pinned to **one**
   worker (`anvil-primaris-worker-hel1-1`).
3. Confirms generate-on-actor is in the live Anvil image (see
   [`docs/substrate-generate-on-actor.md`](../../docs/substrate-generate-on-actor.md)).

Apply this overlay only after that image is live. Do **not** set Primaris
`substrate.actorsEnabled=true` or `substrate.generateOnActor=true` until an
`acp-spike` smoke bind+generate succeeds. Desktop standing grok stays on API
`ProcessBackend` (`actorClass: standing-chat` is not in the generate
allowlist).

## What this pins

| Knob | Value |
| --- | --- |
| Chart | `oci://ghcr.io/kagent-dev/substrate/helm/substrate` **0.0.8** |
| CRDs | `oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds` **0.0.8** |
| Release / namespace | `substrate` / `ate-system` (TLS ServerName `api.ate-system.svc`) |
| Auth | `jwt` (ATE default). Issuer: Talos SA `https://[fdae:41e4:649b:9303::1]:10000`. Audience: `api.ate-system.svc` |
| Node | `kubernetes.io/hostname=anvil-primaris-worker-hel1-1` on every workload **and** WorkerPool pods |
| Storage | `observability-local` on rustfs + valkey PVCs (hel1-1 has no `hcloud-volumes` CSI topology) |
| Fleet | `ActorTemplate/standing-chat` + `ActorTemplate/acp-spike` + `WorkerPool/warm` in `anvilhub` and `hazy-trade` |
| Anvil client | Get/Create/Resume/Suspend/Pause; generate-on-actor is ACP/HTTP through atenet after Resume, opted in per actorClass |

Valkey stays at **6 replicas** because the 0.0.8 init Job hardcodes pods
`0..5`. There is no anti-affinity, so all six plus RustFS, ateapi,
atecontroller, atenet, atelet, and two warm workers share hel1-1. That is a
capacity hazard, not a license to unpin.

## Render (never apply from CI)

```bash
# Copy the example and rotate keys. Never commit values.secrets.yaml.
cp config/ate-constrained-primaris/values.secrets.yaml.example \
   config/ate-constrained-primaris/values.secrets.yaml

./hack/render-ate-constrained.sh \
  --secrets config/ate-constrained-primaris/values.secrets.yaml \
  > /tmp/ate-constrained-primaris.yaml

# Operator checklist on the render (required before any apply):
# - every DaemonSet/Deployment/StatefulSet/Job has nodeSelector hostname hel1-1
# - hostPorts 8085/9090 only appear on DaemonSet/atelet (that one node)
# - rustfsadmin does not appear as accessKey/secretKey values
# - release name is substrate so Service/api is api.ate-system.svc
```

Apply, if ever, is CRDs then the render — not stock helm install:

```bash
# Apply after generate-on-actor is in the live Anvil image and the render is
# one-node-safe. Leave Anvil actorsEnabled/generateOnActor off until acp-spike
# smoke succeeds.
# helm template substrate-crds oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds --version 0.0.8 | kubectl apply --context hazyforge-anvil-primaris -f -
# kubectl --context hazyforge-anvil-primaris apply -f /tmp/ate-constrained-primaris.yaml
```

`createNamespace: true` so helm template emits `Namespace/ate-system`. Label that
namespace (and `anvilhub` / `hazy-trade`, where WorkerPool ateom pods land)
`pod-security.kubernetes.io/enforce=privileged`. Primaris cluster default is
`baseline:latest`; without the label, warm workers are Forbidden (hostPath +
privileged ateom). Do not stock-install.

After ATE exists, Anvil JWT CA trust is chart `substrate.ca.configMapName=ateapi-ca`
with `substrate.ca.namespace=ate-system` (cross-namespace lookup; the
controller already has ConfigMap get). Projected token:
`substrate.token.projected=true` audience `api.ate-system.svc`. Leave
`substrate.actorsEnabled=false` and `substrate.generateOnActor=false` until
an `acp-spike` smoke bind+generate succeeds. Fleet includes
`ActorTemplate/acp-spike` (ACP echo) and `standing-chat` (Desktop stays on
ProcessBackend).

## RustFS keys

Chart default is `rustfsadmin` / `rustfsadmin`. This overlay **overrides**
those with `SET_AT_APPLY_DO_NOT_COMMIT` in `helm-values.yaml`. The secrets
file (gitignored) must supply rotated keys at render time. Tests fail if
committed files use the chart default password as a value.
