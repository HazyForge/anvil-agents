# AgentDataVolume copy / node migration

Local-path and other topology-bound storage pins an `AgentDataVolume` claim to
one node after first consumer. When that node is full (for example `z400` at
99% CPU) but another home-lab node has capacity (`acer`), create a **new**
destination volume and stream the contents across.

Claims are immutable. Anvil never mutates `spec.claimName` in place. Migration
always creates a second `AgentDataVolume` + PVC, copies bytes, then you switch
harness `dataVolumeRefs` through reviewed GitOps.

## CRD: AgentDataVolumeCopy

Append-only maintenance object (same class as `AgentAuthSession`):

- not GitOps desired state
- immutable spec
- blocks AgentRuns that mount the source or destination while non-terminal
- phases: `Pending` → `WaitingForIdle` → `PreparingDestination` → `Streaming` → `Succeeded`/`Failed`

### Stream method

1. Resolve source PVC node from PV `kubernetes.io/hostname` affinity.
2. Ensure destination `AgentDataVolume` exists with the requested `nodeSelector`.
3. Start a **source Job** on the source node that serves `tar | nc` on port 9876.
4. Start a **destination Job** on the destination node that mounts the new claim
   (first consumer binds local-path), connects to the source Service, and extracts.
5. Refuse non-empty destinations unless `allowNonEmptyDestination: true`.

## CLI

```bash
anvil-agentctl volume copy \
  -n hazy-trade \
  --from hazy-trade-backlog-worker-hermes-home \
  --to hazy-trade-backlog-worker-hermes-home-acer \
  --node acer \
  --generate-name hermes-to-acer-
```

Watch:

```bash
kubectl -n hazy-trade get agdvcopy -w
kubectl -n hazy-trade get agentdatavolume hazy-trade-backlog-worker-hermes-home-acer
```

After `Succeeded`:

1. Point the harness `dataVolumeRefs` at the destination volume name in GitOps.
2. Smoke an AgentRun on the new home.
3. Keep the source volume until you deliberately retire it (never delete under load).

## Example object

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentDataVolumeCopy
metadata:
  generateName: hermes-to-acer-
  namespace: hazy-trade
spec:
  sourceRef:
    name: hazy-trade-backlog-worker-hermes-home
  destination:
    name: hazy-trade-backlog-worker-hermes-home-acer
    nodeSelector:
      kubernetes.io/hostname: acer
  method: Stream
  timeoutSeconds: 1800
```

## Safety

- Source and destination names must differ.
- Active AgentRuns on either volume set phase `WaitingForIdle`.
- Concurrent copies that share a volume name wait on each other.
- Destination empty-check protects against accidental clobber.
- Credential bytes are never placed in the copy CR.
