# Kind Quickstart

`run.sh` creates or reuses a Kind cluster, builds the local controller and a
credential-free custom harness, installs the Helm chart, and waits for one run
to succeed. It does not call an LLM or require a provider token.

```bash
./examples/quickstart/run.sh
```

Set `ANVIL_AGENTS_KIND_CLUSTER` to use a different cluster name. The script
leaves the cluster running for inspection. `manifests.yaml` is also a compact
reference for the minimum valid `sourceRef`, harness profile, atomic skill and
tool, ordered sets, canonical profile/run capability selection, runner
ServiceAccount, local override, and run.

On a reused cluster, the script deletes and recreates only its deterministic
`demo-001` run because completed `AgentRun` specs are intentionally immutable.
It also restarts the controller after loading the local `:dev` image so the
existing Deployment cannot keep an older digest behind the same tag.
