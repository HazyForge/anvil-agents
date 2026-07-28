# Public security program (GitHub Actions)

`anvil-agents` is an **open-source** repository. Security evidence lives in
**public GitHub Actions** so anyone can see what was scanned before they run
images. This is intentionally **independent of Anvil Primaris lifecycle**.

Primaris only **installs** this operator into the home cluster via GitOps:

```text
.hazyforge/clusters/anvil-primaris/namespace/anvil-agents-system/
```

That overlay pins digests and deploys the chart. It does **not** own security
gates, TestContract security suites, or release scan orchestration.

## What people can inspect on GitHub

Workflow: [`.github/workflows/security.yml`](../.github/workflows/security.yml)

| Layer | Public jobs | What it proves |
| --- | --- | --- |
| Source | `govulncheck / module+binary`, `gosec / static analysis`, `codeql / go` | This repo’s Go code |
| Owned deps | `owned-deps / anvil-hotline` | First-party `anvil-hotline` pin used by every runner image |
| Repo deps / configs | `trivy / filesystem+deps`, PR `dependency-review` | go.mod, Dockerfiles, charts, secrets/misconfig |
| Containers | `trivy / controller` … `trivy / pi` (7 named jobs) | Full breadth of operator + agent tooling images |
| Trust | `openssf-scorecard` | OpenSSF scorecard on default branch |
| Gate | `security-gate` | All required jobs green |

Each container job includes a **job summary** (role of the image, Trivy
excerpt) and **downloadable artifacts** + **SARIF** for the Security tab.

### Containers covered

| Component | Image | Role |
| --- | --- | --- |
| controller | `anvil-agents` | Operator + API + console |
| codex | `anvil-agent-run-codex` | Codex runner + anvil-hotline |
| opencode | `anvil-agent-run-opencode` | OpenCode runner + anvil-hotline |
| grok-build | `anvil-agent-run-grok-build` | Grok Build runner + anvil-hotline |
| hermes | `anvil-agent-run-hermes` | Hermes runner + anvil-hotline |
| openclaw | `anvil-agent-run-openclaw` | OpenClaw runner + anvil-hotline |
| pi | `anvil-agent-run-pi` | Pi runner + anvil-hotline |

Trivy fails on **HIGH/CRITICAL** (unfixed CVEs ignored by default).

### Owned dependency pin

Runner Dockerfiles pin:

```dockerfile
ARG ANVIL_HOTLINE_VERSION=v0.1.0
```

The `owned-deps / anvil-hotline` job requires a **single** pin across all
runner Dockerfiles, checks out that public tag, and runs govulncheck + Trivy
filesystem against it.

## When it runs

- Every PR and push to `master` / `main`
- Weekly schedule (public minutes)
- GitHub Releases
- Manual `workflow_dispatch`
- Publish workflow requires security green before GHCR push

## Local mirrors (optional)

Same tools as Actions; not required for Primaris install:

```bash
make security              # govulncheck + gosec
make security-trivy        # build + Trivy every container
make security-all          # full local parity with security-gate
./hack/security-trivy-images.sh --component controller
```

## Primaris install only

```bash
# Build/push images and pin the Primaris overlay (no security gate here)
VERSION=vX.Y.Z make release-primaris-fast

# Apply chart + pinned digests to the cluster
make deploy-primaris
```

Security for consumers remains the green **Actions** run on the tag/commit
they trust—not a Primaris TestRun.

## GitHub settings (once)

1. **Settings → Code security** — Code scanning, Dependency graph, Dependabot.
2. Workflows use `security-events: write` for SARIF upload.

## Failure policy

Any required `security.yml` job failing (including any single container Trivy
job or the owned-deps scan) fails `security-gate` and blocks publish.
