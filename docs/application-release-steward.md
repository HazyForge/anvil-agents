# Application release steward

`config/samples/control_v1alpha1_application_release_steward.yaml` packages a
provider-neutral delivery-learning method as an `AgentSkillSet`. Attach it to
application-owned exerciser, monitor, and proposer profiles, then layer the
application's delivery commands, safe experiments, evidence sources, target
policy, repository ownership, and production boundary in local skills and
profiles.

`AgentSkillSet` references are namespace-local. The sample `agents` namespace
is illustrative: render a copy into each namespace containing a consuming
`AgentRunProfile`, then reference that namespace-local instance. A profile in
`anvilhub`, for example, cannot consume the sample object in `agents`.

The method optimizes for new independently verified proof, unique findings,
owner-routing latency, verified fixes, and operator-decision latency. It does
not optimize for a PR or release every day. An unchanged exact artifact that
still converges can correctly produce `confirmed-healthy`; three repeated
unchanged fingerprints require a different experiment, backoff, a parked known
issue, or—only when no valuable safe experiment remains and a real decision
blocks progress—one direction candidate. Do not manufacture a product question
to satisfy a canary or activity metric.

## Role result contract

| Role | Classification | Allowed actions |
| --- | --- | --- |
| Exerciser | `exercised` | `candidate-proof`, `health-observed`, `gap-observed`, `finding-observed`, `direction-candidate` |
| Monitor | `audited` | `new-proof`, `confirmed-healthy`, `known-gap`, `new-platform-finding`, `needs-direction`, `no-op` |
| Proposer | `routed` | `opened-pr`, `updated-issue`, `asked-operator`, `no-op` |

Each terminal decision lists the processed count, batch fingerprint, and first
and last input refs; the bounded per-input progress reports are authoritative
for every input ref and proof/blocker key. This avoids truncating the terminal
summary/detail limits on a large batch. Monitor owns the independently verified
evidence outcome. Proposer owns deduplicated routing and is the only role that
should receive a Hotline credential.

For proposer, `steward-input-result` acknowledges durable handling. Emit it only
after the input has an owner-system update/open, keyed operator-question
outcome, or justified no-op. Stop without acknowledging inputs deferred by a
per-round write cap. If a crash occurs after the side effect but before the
report, owner-system search and idempotent update recover it on the next run.

When one run processes multiple inputs, it emits one `progress` report per
input with stage `steward-input-result`, that input's typed classification and
action, AgentRun ref, and fingerprint. It processes at most 80 oldest unhandled
inputs per run, leaving the rest for a later run; the runtime retains 100
reports. On normal completion it then emits one explicit terminal batch
decision. On NeedsHuman it emits only that terminal report. Aggregate monitor precedence is `needs-direction` >
`new-platform-finding` > `new-proof` > `known-gap` > `confirmed-healthy` >
`no-op`; proposer precedence is `asked-operator` > `opened-pr` >
`updated-issue` > `no-op`. This keeps the single terminal decision
machine-readable without losing mixed per-input results.

Before a proposer posts a question, it persists a progress report with stage
`operator-question-claimed`, classification `routed`, action `asked-operator`,
and the stable key in summary and detail, then calls
`anvil-hotline ask --idempotency-key "$questionKey"`. It searches decisions and
reports before claiming; reinvoking the same key searches channel history and
resumes a posted prompt/reply or posts after a pre-post crash. The proposer runs
under an application-wide concurrency limit of one across scheduled and manual
launch paths. Transport nonce enforcement protects concurrent retries; the
AgentRun report is the durable claim, and keyed channel-history recovery resumes
the existing Discord prompt after a caller restart.

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
for evidence gathering and terminal status reporting. Canary assertions must
verify the expected explicit terminal report type, classification, and action;
process exit zero alone is not role-result proof because the current controller
can synthesize a generic completed decision when no explicit decision exists.
Timeout/failure handling must write one terminal `needsHuman` report and exit
zero so the controller consumes it; this is handled terminal status, not a
successful role decision. A nonzero Job is Failed.

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
