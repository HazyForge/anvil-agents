# anvil-agents

`anvil-agents` is the standalone Kubernetes operator for Hazy Forge agent
execution. It owns AgentRun Jobs, reusable profiles, schedules, launch controls,
durable data volumes, volume profiles, adverse streams, and optional terminal
run archives.

The operator intentionally preserves `control.anvil.hazyforge.io/v1alpha1` so
existing AgentRun resources and PVC owner references can be adopted without a
data migration. Application references are opaque scope names; no Anvil
Primaris, Anvil Hub, Application, Repository, AnvilTask, or delivery CRD is
required.

`AgentRunControl.spec.maxConcurrentRuns` is the standalone per-scope policy
surface. The operator-wide flag is only the fallback for scopes without a
matching control.

Images are published from this repository to `ghcr.io/hazyforge/anvil-agents`
and the five `ghcr.io/hazyforge/anvil-agent-run-*` repositories. This repository
is the only build source for those images. GitHub Actions provides bootstrap
publishing; `.hazyforge/artifact-build.yaml` is the cluster-native delivery
contract.

## Resources

- `AgentRun`: append-only execution record that materializes one ConfigMap and
  one Kubernetes Job.
- `AgentRunProfile`: reusable prompt, tool, backend, policy, and execution
  defaults.
- `AgentSchedule`: interval and manual `run-now` child-run creation with
  `Forbid`, `Allow`, and `Queue` concurrency.
- `AgentRunControl`: cluster-scoped pause/allow policy keyed by opaque
  application scope.
- `AgentDataVolume`: durable PVC ownership and expansion-only resize.
- `VolumeProfile`: reusable storage shapes.
- `AdverseSituation`: buffered adverse events and optional AgentRun responders.

## Local validation

```bash
make verify
docker build -t anvil-agents:dev .
```

Install with Helm:

```bash
helm upgrade --install anvil-agents charts/anvil-agents \
  --namespace anvil-agents-system --create-namespace
```

The checked-in Anvil Primaris `.hazyforge` deployment is intentionally staged
with zero replicas and CRD installation disabled. It must not be activated
until the embedded reconciler is stopped and CRD ownership is switched in the
same reviewed cutover.

Configured adverse source watches use `--adverse-source-gvks` values such as
`apps/v1/Deployment`. The operator ServiceAccount needs separate read RBAC for
every configured external GVK; the base chart never grants broad wildcard
access.

Remote GitHub skill sources default to `api.github.com`. GitHub Enterprise API
hosts must be explicitly added through `githubAPI.allowedHosts`; token-bearing
requests require HTTPS and do not follow redirects.

See [docs/agent-run.md](docs/agent-run.md) and
[docs/migration-from-anvil-primaris.md](docs/migration-from-anvil-primaris.md).
