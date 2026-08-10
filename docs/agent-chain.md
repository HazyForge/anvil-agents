# AgentChain

`AgentChain` is the completion-driven sequential orchestrator for Anvil Agents.
It runs one GitOps-declared linear pipeline of `AgentRun`s: when step N reaches
an allowed terminal phase, the controller creates step N+1 with status-only
handoff.

It does **not** replace:

| Resource | Role |
| --- | --- |
| `AgentSchedule` | Wall-clock cadence for independent runs |
| `AgentCouncil` | Association-only inventory / UI membership |
| `AgentRun` | One Job, one append-only execution record |

## Why a new CRD

`AgentSchedule.runTemplates` Sequential means **rotate on interval ticks**, not
“start B when A succeeds.” Overloading schedule would confuse budgets, identity,
and concurrency. Runner Jobs must not create peer AgentRuns; the controller owns
launch authority.

## Spec sketch

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentChain
metadata:
  name: lab-evidence-loop
  namespace: anvilhub
spec:
  applicationRef: { name: anvil-release-lab }
  concurrencyPolicy: Forbid
  maxInstancesPerDay: 12
  # optional automatic instance starts:
  # startIntervalSeconds: 3600
  steps:
    - name: exercise
      runTemplate:
        profileRef: { name: exerciser }
        prompt: "..."
    - name: monitor
      when:
        previousStep: exercise
        onPhases: [Succeeded]
      runTemplate:
        profileRef: { name: monitor }
        prompt: "..."
      handoff:
        includeDecision: true
        includeLatestReports: true
```

## Starting an instance

- **Manual:** `anvil-agentctl chain start NAME -n NS`  
  (sets annotation `control.anvil.hazyforge.io/chain-start-now` to a unique token;
  instance id is stable for that token so create+status retries stay idempotent)
- **Interval:** `spec.startIntervalSeconds` (+ optional `startInitialDelaySeconds`)
- **Suspend:** `anvil-agentctl chain suspend NAME --reason TEXT -n NS`
- **Cancel advance:** `anvil-agentctl chain cancel NAME -n NS [--instance ID|*]`
  (stops further steps; does not delete the active Job)

Each instance gets a stable `status.activeInstanceId`. Child runs are labeled:

- `control.anvil.hazyforge.io/agent-chain`
- `control.anvil.hazyforge.io/agent-chain-instance`
- `control.anvil.hazyforge.io/agent-chain-step`

Purpose is always `chained`. Historical AgentRuns are **not** garbage-collected
when the chain is deleted (controller owner refs are detached).

## Advancement rules (v1)

1. First step always starts the instance.
2. Later steps require `when.previousStep` to be the **immediate** prior step
   (linear only; no DAG).
3. Default `onPhases` is `[Succeeded]`. Failed prior steps stop the instance.
4. `NeedsHuman` stops advancement (`WaitingHuman`); it does not auto-skip.
5. Optional `onDecisionActions` allowlists prior `status.decision.action`.
6. Handoff is **status text only** (decision, reports, PR URL, optional output
   excerpt). Secrets and service accounts are never copied across steps.
7. Each step’s profile/harness owns its own identity and credentials.
8. `AgentCouncil` is never launch authority—chains reference profiles.

## Security

- Only the controller creates chained runs.
- Step profiles are fixed in GitOps; agents cannot pick arbitrary peers.
- `AgentRunControl` pause for the chain application blocks new launches.
- `maxInstancesPerDay` bounds start storms.

## CLI

```bash
anvil-agentctl chain list -n anvilhub
anvil-agentctl chain get lab-evidence-loop -n anvilhub
anvil-agentctl chain start lab-evidence-loop -n anvilhub
anvil-agentctl chain suspend lab-evidence-loop --reason "hold" -n anvilhub
anvil-agentctl chain resume lab-evidence-loop --reason "resume" -n anvilhub
```

## Non-goals (v1)

- Fan-out / join / full DAG
- Agent-authored dynamic graphs
- Council-driven multi-Job conversations
- External CR event sources (e.g. ApplicationRelease Ready)
- `concurrencyPolicy: Queue` (reserved; use Forbid)

See also `docs/design-roadmap.md` (Workflow Boundary) and the sample
`config/samples/control_v1alpha1_agentchain.yaml`.
