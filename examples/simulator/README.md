# Simulator

Credential-free agents for a local Anvil Agents installation. Start everything
with `make simulator` from the repository root; see
[docs/simulator.md](../../docs/simulator.md).

- `harness/` is the simulated harness image: `sim_agent.py` plus the scripted
  scenarios it plays.
- `seed/library.yaml` is the reusable library: harness, run and volume
  profiles, skill and tool sets, a council, schedules and launch controls.
- `seed/runs.yaml` is one wave of runs. Create it again with
  `hack/simulator.sh wave`.

To try a scenario by hand on any cluster that has the library applied:

```bash
kubectl create -f - <<'EOF'
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRun
metadata:
  generateName: try-
  namespace: agents
spec:
  purpose: manual
  sourceRef: {kind: ManualRequest, name: me}
  profileRef: {name: pr-reviewer}
  prompt: |
    Review a pull request.
    sim-scenario: pr-review
  scope:
    applicationRef: {name: hazy-trade}
EOF
```
