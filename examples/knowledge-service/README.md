# External Knowledge Service

This example teaches different harnesses the same small read-only tool contract
without embedding a knowledge vendor in the operator. It expects
`GET /v1/search?q=...` to return JSON and reads its bearer token from a
same-namespace Secret.

`profile.yaml` defines one backend-neutral `AgentSkillSet`, Codex and Pi
`AgentHarnessProfile` objects, and one role-oriented `AgentRunProfile`.
`runs.yaml` shows a run-local `Augment` override and an atomic Codex-to-Pi
harness swap that keeps the same role and skill set.

Adapt the setup wrapper to your deployed API, use a real TLS endpoint, and
replace `knowledge-reader` with a secret containing only read authority. The
example Secret contains a placeholder and must not be committed with a real
token. Create the separate `codex-credentials` and `pi-credentials` Secrets
required by the runtime profiles. The wrapper intentionally installs into the
run workdir so it does not need root access.

```bash
kubectl apply -f secret.example.yaml
kubectl apply -f profile.yaml
# Applying runs.yaml starts two real provider-backed AgentRuns.
```

For Hazy Forge, the same pattern can wrap the separately deployed
`knowledge-based` CLI or service while keeping the Markdown vault, access
policy, and credentials outside this open-source operator.
