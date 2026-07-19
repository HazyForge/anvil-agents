# External Knowledge Service

This example teaches different harnesses the same small read-only tool contract
without embedding or deploying a knowledge vendor in the operator. It expects
an externally managed operation-envelope API at `POST /v1/operations` and sends
only the `search-index` operation. The wrapper uses `curl` and `jq` from the
runner image.

`profile.yaml` defines one backend-neutral `AgentSkillSet`, one independently
composable `AgentToolSet`, Codex and Pi `AgentHarnessProfile` objects, and one
role-oriented `AgentRunProfile`.
`runs.yaml` shows a run-local `Augment` override and an atomic Codex-to-Pi
harness swap that keeps the same role and skill set.

Adapt the setup wrapper to your deployed API, use a real TLS endpoint when the
service leaves the cluster, and replace `knowledge-reader` with a Secret
containing only read authority. The token is optional so the same wrapper can
be canaried against a cluster-local service, but an unauthenticated server does
not enforce read-only access: the wrapper's operation allowlist is only a
client-side guard. Create the separate `codex-credentials` and
`pi-credentials` Secrets required by the runtime profiles.

```bash
kubectl apply -f secret.example.yaml
kubectl apply -f profile.yaml
# Applying runs.yaml starts two real provider-backed AgentRuns.
```

For Hazy Forge, the same pattern can wrap the separately deployed
`knowledge-based` CLI or service while keeping the Markdown vault, access
policy, and credentials outside this open-source operator.
