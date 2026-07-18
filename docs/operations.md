# Operations

## Versioning

The packaged chart, controller, and five official runner tags form one release
set. The source chart defaults to locally built `:dev` images; packaging
rewrites all six defaults to the selected `vVERSION` and registry prefix.
Production values should replace every image with an immutable digest. Do not
mix runner versions without testing their payload and status contracts.

Back up all seven custom-resource kinds and relevant PVCs before an upgrade.
Run `helm template` and review CRD changes first. Only one controller may
reconcile the branded API group during a migration or rollback.

## Health And Troubleshooting

The controller exposes `/healthz` and `/readyz` on port 8081 and metrics on
8080. Start with:

```bash
kubectl get agentruns,agentschedules,agentdatavolumes -A
kubectl describe agentrun -n <namespace> <name>
kubectl get jobs,pods -n <namespace> \
  -l control.anvil.hazyforge.io/agent-run=<name>
```

Controller-owned status reasons distinguish invalid refs, missing images,
credential freshness, launch controls, volume readiness, and Job outcomes.
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
a mirror. A profile-level backend image overrides the install default. Use
`hack/build-images.sh --prefix ... --tag ... --push` to publish the same six
components without GitHub Actions; the script refuses dirty pushes by default.
