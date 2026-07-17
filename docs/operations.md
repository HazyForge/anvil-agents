# Operations

## Versioning

The packaged chart, controller, and five official runner tags form one release
set. The source chart defaults to locally built `:dev` images; packaging
rewrites all six defaults to the selected `vVERSION` and registry prefix.
Production values should replace every image with an immutable digest. Do not
mix runner versions without testing their payload and status contracts.

Back up all nine custom-resource kinds and relevant PVCs before an upgrade.
Run `helm template` and review CRD changes first. Only one controller may
reconcile the branded API group during a migration or rollback.

`make kind-upgrade-e2e` proves a portable seven-CRD, legacy-object baseline can
be upgraded to the current nine-CRD chart while retaining profiles and runs. It
uses and removes a dedicated disposable Kind cluster. Set
`ANVIL_AGENTS_UPGRADE_FROM_REF` to a reachable historical chart ref when
testing an exact released baseline; the default does not depend on an
intermediate Git object surviving a squash merge.

## Health And Troubleshooting

The controller exposes `/healthz` and `/readyz` on port 8081 and metrics on
8080. Start with:

```bash
kubectl get agentruns,agentschedules,agentdatavolumes -A
kubectl describe agentrun -n <namespace> <name>
kubectl get agentharnessprofiles,agentskillsets -n <namespace>
kubectl get jobs,pods -n <namespace> \
  -l control.anvil.hazyforge.io/agent-run=<name>
```

Controller-owned status reasons distinguish invalid composition refs and
overrides, missing images, credential freshness, launch controls, volume
readiness, and Job outcomes. Inspect `.status.resolvedComposition` to identify
the exact reusable inputs, inherited application scope, and payload digest.
Treat a lost client connection as ambiguous and reattach to the same run.

## Archive And Retention

Postgres archive storage is optional. Configure its URL from a Secret and set
terminal retention only after verifying archives. A terminal CR is pruned only
after successful archival. Archive records can contain prompt and output data;
protect and expire them accordingly.

## Uninstall

A normal Helm uninstall removes the controller/API workloads and RBAC but
retains CRDs because each CRD has `helm.sh/resource-policy: keep`.

```bash
helm uninstall anvil-agents -n anvil-agents-system
kubectl get crd | grep control.anvil.hazyforge.io
```

Deleting CRDs is a separate destructive operation. It deletes all matching
custom resources and may garbage-collect PVCs owned by `AgentDataVolume`.
Export resources and back up data before deliberate permanent removal.

## Registry Mirrors

Set `image.reference` for the controller/API and every `runnerImages` value for
a mirror. A harness-profile or run-level backend image overrides the install
default. Use `hack/build-images.sh --prefix ... --tag ... --push` to publish the
same six components without GitHub Actions; the script refuses dirty pushes by
default.

## Optional Release Workflow

`.github/workflows/publish.yaml` is a release convenience built on the same
scripts. It runs only for a `v*` tag push or a manual rerun of an existing tag,
gates publication on `make verify` and `make kind-e2e`, publishes all six images
with version and commit tags, and pushes the version-coupled chart to the
repository owner's GHCR `charts` namespace. It does not run for `master` and
does not publish `latest`, so an intermediate merge cannot become the default
artifact.
