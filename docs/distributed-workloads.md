# Distributed Workloads

Anvil Agents is useful when agent loops do substantial local work: compiling a
large repository, executing tests, building images, indexing code, evaluating
many independent changes, processing logs, or running custom CPU-, memory-, or
accelerator-heavy tools. The client submits durable intent once; Kubernetes
places each resulting Job on available cluster capacity.

This moves execution away from a single laptop, shell session, or fixed worker.
It also lets a mixed cluster dedicate different machines to different kinds of
agent work while profiles keep those placement and capacity decisions
reusable.

## Scaling Model

One `AgentRun` creates one Kubernetes Job whose Pod runs on one node. Anvil
Agents does not divide one process across nodes. It distributes heavy work by
running multiple independent `AgentRun` objects at the same time, for example:

- compile and test separate repositories or revisions in parallel;
- assign security, correctness, documentation, and release reviews to
  independent lanes;
- process separate shards, packages, environments, or evidence windows;
- run the same backend-neutral skill set through different harnesses;
- send CPU builds, memory-heavy analysis, and custom GPU work to different node
  pools.

If one computation itself requires multiple nodes, expose a distributed build,
batch, database, queue, or inference service to the harness. The AgentRun then
owns and observes one client task against that service.

Distribution is not automatic retry or failover. AgentRun Jobs use
`backoffLimit: 0`; a node, Pod, harness, or tool failure becomes a terminal run
instead of being retried on another worker. Create a new `AgentRun` for another
attempt so each execution remains an append-only record.

## Size And Place Harnesses

Put stable resource and placement requirements in `AgentHarnessProfile`, not
in every run:

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentHarnessProfile
metadata:
  name: codex-large-build
  namespace: agents
spec:
  backend:
    kind: codex
  execution:
    serviceAccountName: agent-runner
    envSecretRefs:
      - name: codex-credentials
    timeoutSeconds: 7200
    resources:
      requests:
        cpu: "4"
        memory: 8Gi
        ephemeral-storage: 20Gi
      limits:
        cpu: "8"
        memory: 16Gi
        ephemeral-storage: 40Gi
    nodeSelector:
      workload.example.io/class: build
    tolerations:
      - key: workload.example.io/dedicated
        operator: Equal
        value: agents
        effect: NoSchedule
```

Resource requests let the Kubernetes scheduler make an honest placement
decision. Limits bound one run so parallel agents do not consume the entire
node. Selectors and required affinity express hard constraints; preferred
pod anti-affinity can encourage compatible runs to spread without making that
placement mandatory. Tolerations should match intentionally tainted agent
worker pools.

A custom harness can request extended resources such as a GPU when the cluster
has the matching device plugin and nodes. Keep those provider- and
machine-specific requirements in a separate harness profile so the same
`AgentRunProfile` and `AgentSkillSet` can move between ordinary and specialized
workers.

## Permit Parallel Lanes

Placement only helps when policy permits more than one active run. Check every
applicable layer:

1. Create multiple `AgentRun` objects, or set an `AgentSchedule` to
   `concurrencyPolicy: Allow` with an explicit `maxConcurrentRuns`.
2. For runs sharing `scope.applicationRef.name`, have a cluster administrator
   set the cluster-scoped `AgentRunControl.spec.maxConcurrentRuns` above one.
   The lowest positive value across every matching control wins.
3. When no control matches, raise the Helm `applicationMaxConcurrentRuns`
   value or `--application-max-concurrent-runs` flag from its default of one.
   Runs without an application scope do not receive this application cap.
4. Give the namespace enough `ResourceQuota` for the intended parallelism.
5. Ensure the target nodes, autoscaler, and storage provisioner can satisfy the
   aggregate requests.

For example, a schedule can allow up to four long-running intervals to overlap:

```yaml
spec:
  intervalSeconds: 900
  concurrencyPolicy: Allow
  maxConcurrentRuns: 4
```

Use `Queue` when preserving every interval matters but capacity must remain
bounded. Use `Forbid` when a newer interval has no value while an older one is
still active. These controls prevent an event burst or slow dependency from
turning useful distribution into an unbounded workload surge.

## Storage And Data Locality

Compute can move more freely than state. Choose storage based on the workload:

- Use per-run ephemeral storage for checkouts and rebuildable scratch data.
- Use independent `AgentDataVolume` claims for caches or durable homes that do
  not need concurrent writers.
- Use network storage, object storage, a database, Git, or another service for
  cross-node handoff and shared knowledge.
- When an `AgentDataVolume` or `VolumeProfile` declares a `nodeSelector`, the
  resolved selector is merged into the AgentRun Pod placement.

PVC and PV topology may separately constrain Kubernetes scheduling. The
operator does not infer an AgentDataVolume node selector from a local-path or
host-local PV; declare it explicitly when the harness must select the same
machine.

Many `ReadWriteOnce` volumes cannot be mounted for concurrent work on different
nodes. A shared home volume can therefore serialize or pin otherwise parallel
runs. Prefer immutable inputs and explicit output publication when throughput
across machines is the goal.

## Operate The Fleet

Start with conservative requests and concurrency, then inspect Pod scheduling,
queue time, run duration, node utilization, evictions, and volume attachment
latency. Increase parallelism only when the cluster has headroom. Use separate
harness profiles for materially different workload classes rather than one
oversized default.

The optional OIDC API exposes run status and bounded live logs to authorized
users without granting Kubernetes credentials. A client disconnect does not
cancel the Job; users can reconnect to the same durable run while the heavy
work continues on its assigned machine.
