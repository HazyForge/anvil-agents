---
name: manage-agent-runs
description: Inspect and manage anvil-agents resources with Kubernetes while preserving append-only run history, launch controls, and durable volume safety.
---

# Manage Agent Runs

Use this skill for `AgentRun`, `AgentRunProfile`, `AgentSchedule`,
`AgentRunControl`, `AgentDataVolume`, `VolumeProfile`, and `AdverseSituation`.

1. Read `docs/agent-run.md` and the namespace's repository guidance.
2. Inspect the CRD, resource generation, conditions, controller-owned status,
   child Job, payload ConfigMap, pod events, and relevant logs.
3. Treat application references as opaque keys. Do not assume another control
   plane is installed.
4. Create a new AgentRun for new work. Never rewrite a completed run to replay it.
5. Use a profile for durable defaults and a run prompt for one request.
6. Nudge schedules by changing the
   `control.anvil.hazyforge.io/run-now` annotation token.
7. Use `AgentRunControl` to pause future launches. A pause never terminates an
   active Job.
8. Never delete an AgentDataVolume or PVC as a troubleshooting shortcut.
   Storage may expand but not shrink; cross-namespace mounts are invalid.
9. Configure external adverse watches and their read RBAC explicitly. Agent
   responders remain opt-in.
10. Verify resulting status and child-object identity before reporting success.

Use normal `kubectl get`, `kubectl describe`, and read-only log commands for
inspection. Any mutation must follow the caller's Kubernetes authorization and
the resource's declared intent.
