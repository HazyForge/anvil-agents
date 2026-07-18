# Multi-Harness Schedule

The schedule rotates independent runs between Codex and Pi harness profiles.
Both templates use the same role profile and `evidence-review` skill set; only
the provider runtime, credentials, and execution envelope change. `Queue` and
`maxConcurrentRuns: 1` preserve ordering while each interval selects the next
named template. Change to `Allow` with an explicit cap for parallel lanes.

For heavyweight builds, tests, scans, or indexing, give each harness profile
an honest resource envelope and placement rules, then use `Allow` plus an
application-level concurrency limit above one. Kubernetes can place overlapping
runs on different machines or node pools. One run remains one Pod on one node;
fan out independent work items to use the fleet. See
[`docs/distributed-workloads.md`](../../docs/distributed-workloads.md).

The checked-in manifest is intentionally sequential. A parallel deployment
must raise `AgentSchedule.spec.maxConcurrentRuns` and either the matching
cluster-scoped, administrator-owned `AgentRunControl.spec.maxConcurrentRuns`
or the Helm `applicationMaxConcurrentRuns` fallback. The lowest matching
positive control wins. This example does not create that administrative
control, and changing only the schedule may leave the application lane
serialized.

This is distributed repeat execution, not a shared live conversation. Persist
handoff evidence in Git, an external knowledge service, or intentionally
managed volumes. Create the referenced runner ServiceAccount and provider
Secrets before applying this illustrative example, and pin backend image
digests in production.
