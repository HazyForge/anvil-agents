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
| Auth | `jwt` (ATE default). Issuer: in-cluster kube SA `https://kubernetes.default.svc.cluster.local` **after** the Talos CP patch. Audience: `api.ate-system.svc` |
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

`hack/render-ate-constrained.sh` also emits
`ClusterRoleBinding/oidc-discovery-unauthenticated` (kube default role
`system:service-account-issuer-discovery` bound to
`system:unauthenticated`). That binding is a no-op until the Talos CP patch
turns `--anonymous-auth=true`.

## Austin: Talos CP patch (required; this workstation cannot apply Omni)

The VIP `https://[fdae:41e4:649b:9303::1]:10000` is **Omni's SideroLink
kube-apiserver**, not the Talos maintenance API (50000). Cert is
`CN=kube-apiserver` with that ULA in SAN. It is the string kube stamps into
SA `iss`. It is not a reachable OIDC issuer from workload pods.

Paste **Control Plane** config patch (not workers, not cluster-wide Cilium):

`config/ate-constrained-primaris/talos/12-control-plane-sa-oidc-issuer.yaml`

Same extraArgs: `anvil-primaris` `tools/talos-omni/patches/12-control-plane-sa-oidc-issuer.yaml`
(already listed on the generated `anvil-primaris` ControlPlane template).

Omni UI → cluster anvil-primaris → Control Plane patches, **or**
`omnictl cluster template sync` of that template. This restarts
kube-apiserver. Projected tokens refresh; expect a short 401 window.

Do **not** enable Cilium IPv6 for this. Do **not** expose Talos API :50000.

After apply, all of these must be true before changing ATE live issuer or
flipping generate gates:

```bash
kubectl --context hazyforge-anvil-primaris get --raw /.well-known/openid-configuration
# issuer and jwks_uri both https://kubernetes.default.svc.cluster.local…

kubectl --context hazyforge-anvil-primaris apply -f \
  config/ate-constrained-primaris/oidc-discovery-unauthenticated.yaml

# From an ate-system debug pod (no token): HTTP 200
curl --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt \
  https://kubernetes.default.svc.cluster.local/.well-known/openid-configuration
```

Then point ateapi at the new issuer **without** a full overlay re-render if
possible (re-render rotates the JWT CA; restart `ate-api-server` if you do):

```bash
# Prefer a one-arg patch over helm template re-apply.
kubectl --context hazyforge-anvil-primaris -n ate-system \
  get deploy ate-api-server-deployment -o jsonpath='{.spec.template.spec.containers[0].args}' ; echo
# Replace --client-jwt-issuer=https://[fdae:…]:10000 with
# --client-jwt-issuer=https://kubernetes.default.svc.cluster.local
# and the matching ATE_API_K8SJWT_ISSUER in ConfigMap ate-api-server-envvars.
```

Leave `actorsEnabled` / `generateOnActor` false until a projected token
validates. Then a bounded `acp-spike` smoke is OK. Do not list
`standing-chat`. Do not flip `hazy-trade-agent-manager` off Job.

## Operational gotcha: re-render regenerates the JWT CA

`render-ate-constrained.sh` uses `helm template` with no `--kube-context`, so
the chart's `auth.jwt.bootstrap` `lookup`-based reuse guard never sees the
live cluster and **always** generates a brand-new random CA + `ateapi-tls`
server cert on every render. Re-applying the render therefore rotates the CA
and leaf cert in the `ateapi-ca` ConfigMap and `ateapi-tls` Secret, but the
**already-running** `ate-api-server` pod keeps serving its old in-memory/
`emptyDir` cert (populated once by an init container at pod start) until it
restarts. Anvil (or any client) then fails TLS with
`x509: certificate signed by unknown authority (... candidate authority
certificate "api-ca")` because the CM now holds a CA that does not match the
cert the pod is still serving.

**After every re-render + re-apply of this overlay**, restart the API
server so it picks up the matching secret:

```bash
kubectl --context hazyforge-anvil-primaris -n ate-system \
  rollout restart deployment/ate-api-server-deployment
```

## Verified 2026-09-20: Omni SideroLink VIP is kube-apiserver, not Talos API

`kubectl get --raw /.well-known/openid-configuration` (via the normal,
reachable API server endpoint) returned:

```json
{"issuer":"https://[fdae:41e4:649b:9303::1]:10000","jwks_uri":"https://5.161.127.112:6443/openid/v1/jwks", ...}
```

From `ate-system` on `anvil-primaris-worker-hel1-1`:

| Path | Result |
| --- | --- |
| Workload pod → `[fdae:…]:10000` | `network is unreachable` (Cilium `enable-ipv6=false`; no IPv6 default route) |
| hostNetwork → that VIP:10000 | TLS `CN=kube-apiserver`, SAN includes the ULA **and** `kubernetes.default.svc`. HTTP GET `/.well-known/openid-configuration` is **401 Unauthorized** (anonymous-auth off). Re-probe was 401, not GOAWAY. |
| Pod → `https://kubernetes.default.svc/.well-known/openid-configuration` | **401** anonymous, **200** with SA bearer. JWKS same. |
| Public `https://5.161.127.112:6443` OIDC | Same 401 anonymous / 200 with bearer |

SA tokens still have `iss` equal to the SideroLink URL until the Talos CP
patch is applied. ATE 0.0.8 `ateapi` flags are only `--client-jwt-issuer`,
`--client-jwt-audience`, `--client-jwt-ca-cert` — no JWKS override. Pointing
`issuer` at `kubernetes.default.svc.cluster.local` **before** Talos changes
`--service-account-issuer` would fail `iss` matching.

Fix is the Talos CP patch + unauthenticated discovery binding + ATE issuer
URL above. Do not set Primaris `substrate.actorsEnabled=true` /
`generateOnActor=true` until a real `acp-spike` smoke bind+generate
succeeds end to end.

**Applied 2026-09-21:** Omni CP patch, unauthenticated discovery, and ateapi
issuer are live. Anonymous in-cluster OIDC is HTTP 200; ateapi accepts Anvil
projected SA tokens. The remaining CreateActor blocker was ATE 0.0.8
`actor_template_namespace` (Anvil had only later-ateapi
`actor_template.atespace`). Keep generate gates off until that image is
hot-released and an `acp-spike` smoke succeeds. Do not re-render the live
ATE overlay just to match Git — that rotates the JWT CA.

## RustFS keys

Chart default is `rustfsadmin` / `rustfsadmin`. This overlay **overrides**
those with `SET_AT_APPLY_DO_NOT_COMMIT` in `helm-values.yaml`. The secrets
file (gitignored) must supply rotated keys at render time. Tests fail if
committed files use the chart default password as a value.
