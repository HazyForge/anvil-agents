# Anvil Agents self-development fleet

This optional GitOps subtree deploys three Grok 4.5 roles for this repository:
a read-only maintainer scout, a manual approval-gated implementer, and an
independent read-only reviewer. It composes the separately deployed
`knowledge-based` service through `AgentToolSet`; it does not install, own, or
write that service.

The observer schedule is intentionally suspended. Do not enable it until all
of these external prerequisites are verified in the target cluster:

- the installed CRDs and controller include `AgentToolSet`;
- the configured Grok runner is digest-pinned to a build containing the strict
  repository checkout contract from this repository's current `master`;
- `anvil-agents-grok-xai` is produced by the target cluster's secret manager;
- the reader and writer ServiceAccounts exist with token automount disabled
  and only the minimum required permissions;
- `anvil-agents-github-pr-writer` is a repository-scoped GitHub App or machine
  identity that can create branches and draft PRs but cannot bypass review;
- `master` is protected from direct and force pushes, and required checks are
  configured;
- NetworkPolicy permits these pods to reach only DNS, GitHub/xAI as required,
  and `knowledge-base.knowledge-base.svc.cluster.local:8080`;
- the knowledge service has an enforced read-only identity or the known lack
  of server-side authorization is explicitly accepted for observe-only
  canaries;
- a cluster `AgentRunControl` caps this application at one concurrent run.

The target platform owns Argo CD discovery, AppProject allowlists, Secrets,
ServiceAccounts, NetworkPolicies, and policy-broker authorization. A portable
operator repository must not grant those authorities to itself. Register this
exact repository and `.hazyforge/agents` path there, allowing only the
namespaced agent CRDs used by this kustomization.

The knowledge wrapper forwards `KNOWLEDGE_BASE_TOKEN` when present. If the
service gains authentication, the target overlay must add a read-only
knowledge Secret to both harness profiles; this base intentionally references
no credential that the current unauthenticated deployment does not have.

Start with one manually created scout run pinned to a full commit SHA. Confirm
the log reports that exact `git rev-parse HEAD`, performs a real
`knowledge-search`, cites returned note paths, and leaves the repository and
cluster unchanged. Then run the independent reviewer. Keep the implementer
manual-only; its AgentRun must identify one approved issue and inject an exact
40-character `ANVIL_AGENT_RUN_REPOSITORY_REF`.
