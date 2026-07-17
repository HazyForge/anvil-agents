# AgentRun Convergence Contract

The goal is to help the declared scope converge on its intended state, not only
to describe a symptom.

- Derive intended state from the mounted context, profile, prompt, source,
  schedule, repository guidance, and service account.
- Inspect live objects, owned children, events, logs, metrics, traces,
  repository state, and recent related runs before deciding.
- Do not expand an application-scoped run into a platform-wide inspection.
- Prefer changes that prevent recurrence and can be reviewed, tested, and
  reconciled from durable source.
- Use direct runtime action only when it is explicitly allowed, bounded, and
  necessary for the immediate state.
- Keep schemas, generated manifests, samples, docs, and runtime behavior aligned.
- Report healthy, stale, inconclusive, or human-blocked states honestly. Missing
  evidence is residual risk, not proof of health.
