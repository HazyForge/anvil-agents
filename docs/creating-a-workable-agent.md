# Creating a Workable Agent End to End

This guide walks from an empty namespace capability to a scheduled, branch-scoped
AgentRun that can install tools, follow skills, contact a human, and open PRs.

## Concepts in one page

| Object | Owns |
| --- | --- |
| **AgentRunProfile** | Role, scope, standing system prompt, intent, composition refs |
| **AgentHarnessProfile** (optional) | Backend image, SA, secrets, volumes, limits |
| **AgentSkillSet** | When/how instructions (may reference image tools) |
| **AgentToolSet** | Runtime install of tools (`setupScript`, optional OCI initializer, and `verifyCommand`) |
| **AgentSchedule** | Cadence that creates append-only AgentRuns |
| **AgentRun** | One immutable execution (append-only) |

**ToolSets vs skills**

- **ToolSet** = install something at runtime with a setup script or a
  digest-pinned OCI initializer.
- **Skill** = teach when/how to use a tool (including tools already on the image).

**Human communications**

- ToolSet + SkillSet name: `human-comms`
- Binary: `anvil-hotline` (public module `github.com/hazyforge/anvil-hotline`)
- Secret: `agent-feedback-discord` in the run namespace
- **Wait budget** (skill requires full wait; do not cancel early):
  - `ANVIL_HOTLINE_TIMEOUT` or `ANVIL_AGENT_FEEDBACK_TIMEOUT` via profile
    `extraEnv` (e.g. `30m`, `1h`)
  - default if unset: `30m`
- Report progress while waiting. A reply leads to a normal decision after
  authority validation. Emit terminal `needsHuman` only after timeout or hard
  failure, then stop without a later success decision.

**Repository / branch scope** (profile or run `spec.scope.repository`)

```yaml
scope:
  repository:
    name: HazyForge/hazy-trade
    url: https://github.com/HazyForge/hazy-trade.git
    ref: master                    # workspace checkout
    destinationBranch: master      # only allowed PR base
    allowedBranches:               # heads the agent may analyze/use
      - master
      - release/stable
```

Injected env:

- `ANVIL_AGENT_RUN_REPOSITORY`
- `ANVIL_AGENT_RUN_REPOSITORY_URL`
- `ANVIL_AGENT_RUN_REPOSITORY_REF`
- `ANVIL_AGENT_RUN_DESTINATION_BRANCH`
- `ANVIL_AGENT_RUN_ALLOWED_BRANCHES`

## Prerequisites

1. Namespace with AgentRun CRDs and the anvil-agents controller watching it.
2. Runner image digests allowed by any Hub/application policy you use.
3. ServiceAccount + RBAC for the run (read cluster, optional GitHub broker).
4. Secrets in the same namespace:
   - model credentials (`codex-credentials`, `GROK_AUTH_JSON`, etc.)
   - optional `agent-feedback-discord`
   - optional GitHub app/token secret for PR work
5. Image pull secret if the runner image is private.

## Minimal workable agent (checklist)

### 1. ToolSet (only if install is needed)

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentToolSet
metadata:
  name: human-comms
  namespace: my-app
spec:
  tools:
    - name: anvil-hotline
      setupScript: |
        set -eu
        install_dir="${HOME}/.local/bin"
        mkdir -p "${install_dir}"
        export PATH="${install_dir}:${PATH}"
        GOBIN="${install_dir}" CGO_ENABLED=0 \
          go install github.com/hazyforge/anvil-hotline/cmd/anvil-hotline@v0.2.0
      verifyCommand: [bash, -lc, 'export PATH="$HOME/.local/bin:$PATH"; anvil-hotline --help']
```

### 2. SkillSet (usage + domain analysis)

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSkillSet
metadata:
  name: my-analysis
  namespace: my-app
spec:
  skills:
    - name: my-analysis
      content: |
        Analyze evidence first. Propose one bounded change. Cite paths.
```

### 3. Profile

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentRunProfile
metadata:
  name: my-steward
  namespace: my-app
  labels:
    control.anvil.hazyforge.io/ownership: gitops
spec:
  scope:
    applicationRef: { name: my-app }
    repository:
      name: org/repo
      destinationBranch: master
      allowedBranches: [master]
  skillSets:
    refs: [{ name: my-analysis }, { name: human-comms }]
  toolSets:
    refs: [{ name: human-comms }]
  harness:
    intent: proposeChange
    systemPrompt: |
      Follow skills. Stay on allowed branches. PRs base only on destinationBranch.
      Use anvil-hotline when stuck after evidence.
    backend:
      kind: codex
      image: ghcr.io/hazyforge/…@sha256:…
    execution:
      serviceAccountName: my-agent-run
      envSecretRefs:
        - { name: codex-credentials }
        - { name: agent-feedback-discord }
      timeoutSeconds: 3600
```

### 4. Schedule (optional)

```yaml
apiVersion: control.anvil.hazyforge.io/v1alpha1
kind: AgentSchedule
metadata:
  name: my-steward-daily
  namespace: my-app
spec:
  profileRef: { name: my-steward }
  concurrencyPolicy: Forbid
  interval: { everySeconds: 86400 }
  runTemplate:
    purpose: scheduledHealthCheck
    prompt: Run the daily stewardship pass.
```

### 5. Manual smoke run

```bash
anvil-agentctl run create \
  -n my-app \
  --generate-name my-steward-smoke- \
  --profile my-steward \
  --source-kind ManualRequest \
  --source-name manual-smoke \
  --intent observe \
  --prompt "Smoke: list tools, confirm checkout ref, report only."
```

Watch:

```bash
anvil-agentctl run get -n my-app <name>
anvil-agentctl run logs -n my-app <name>
```

Look for:

- `ANVIL_AGENT_RUN_TOOL_SETUP_START` / `TOOL_VERIFY_OK` / `TOOL_SETUP_COMPLETE`
- repository env values
- terminal status JSON via `anvil-agent-status`

## GitOps ownership

Objects labeled `control.anvil.hazyforge.io/ownership: gitops` may only be
mutated by Argo CD in production clusters. Use `ownership: operator` for live
experiments, then promote complete YAML under `.hazyforge/agents/`.

## Safety defaults

- AgentRuns are **append-only** (never edit a past run).
- Prefer draft PRs; no merge from agents unless a separate reviewed policy says so.
- Hotline replies are **information only** — they do not expand RBAC or trading power.
- Put secrets only in Kubernetes Secrets / ExternalSecrets, never in prompts.

## Example in this ecosystem

Hazy Trade ships:

- `human-comms` ToolSet + SkillSet (Discord hotline)
- `hazy-trade-docs-code-steward` profile + daily schedule
- `hazy-trade-commit-docs-analysis` skill

See `.hazyforge/agents/` in `HazyForge/hazy-trade`.
