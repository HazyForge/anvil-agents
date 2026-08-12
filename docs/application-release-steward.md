# Application release steward

`config/samples/control_v1alpha1_application_release_steward.yaml` packages a
provider-neutral delivery-learning method as an `AgentSkillSet`. Attach it to
application-owned exerciser, monitor, and proposer profiles, then layer the
application's delivery commands, safe experiments, evidence sources, target
policy, repository ownership, and production boundary in local skills and
profiles.

The method optimizes for new independently verified proof, unique findings,
owner-routing latency, verified fixes, and operator-decision latency. It does
not optimize for a PR or release every day. An unchanged exact artifact that
still converges can correctly produce `confirmed-healthy`; three repeated
unchanged fingerprints require a different experiment, backoff, a parked known
issue, or one direction candidate.

## Role result contract

| Role | Classification | Allowed actions |
| --- | --- | --- |
| Exerciser | `exercised` | `candidate-proof`, `health-observed`, `gap-observed`, `finding-observed`, `direction-candidate` |
| Monitor | `audited` | `new-proof`, `confirmed-healthy`, `known-gap`, `new-platform-finding`, `needs-direction`, `no-op` |
| Proposer | `routed` | `opened-pr`, `updated-issue`, `asked-operator`, `no-op` |

Each decision lists all input AgentRun refs processed and the stable proof,
blocker, or question key. Monitor owns the independently verified evidence
outcome. Proposer owns deduplicated routing and is the only role that should
receive a Hotline credential.

## Transport and activation

`AgentChain` is the preferred completion-driven transport when the chain keeps
every step's application identity and policy boundary. A rotating
`AgentSchedule.runTemplates` list advances on time ticks, not completion. When
used as a bridge, every role must process all unhandled prior decisions and
record their refs; matching wall-clock cadence alone is insufficient.

Commit a new write-capable schedule suspended. Prove, serially:

1. exerciser emits a valid role decision within its declared authority;
2. monitor independently reconstructs it without write credentials;
3. proposer deduplicates and routes one controlled fixture finding; and
4. an operator-authorized Hotline transport canary asks and receives one
   harmless reply without manufacturing a product blocker or duplicate.

Only then activate the schedule through the owning GitOps repository. Ensure
the Job timeout leaves explicit headroom beyond the configured Hotline timeout
for evidence gathering and terminal status reporting.

## Application adoption

Start with observation, development, or staging. The application must provide:

- an explicit safe experiment catalog and authority boundary;
- exact artifact, target, test/action, and live observation evidence;
- application, platform, and agent-runtime owner routing;
- cadence and daily budgets with manual canary/recovery headroom; and
- a proposer-only operator question policy.

Production, destructive, costly, credentialed, or money-touching actions remain
application-specific and require their existing explicit authority. Provider-
native behavior stays behind the application's adapters rather than being
flattened into this generic skill.
