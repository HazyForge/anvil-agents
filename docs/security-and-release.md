# Free public security program (GitHub Actions)

`anvil-agents` is open source. Security evidence is **as many free scanners as
practical**, running on **public GitHub Actions minutes** (no paid SaaS, no
Primaris lifecycle coupling). Anyone can open the Actions tab and inspect
check runs, job summaries, artifacts, and the Security tab.

Consumer deployment repositories install this operator from the published OCI
chart and immutable image references. Their cluster-specific release and
promotion workflows are separate from this public security program.

## Free scanners (security.yml)

| Layer | Public job name | Tool (cost) |
| --- | --- | --- |
| Go vulns | `free · govulncheck` | govulncheck (free) |
| Go SAST | `free · gosec` | gosec (free) |
| Go SAST | `free · CodeQL` | GitHub CodeQL free for public repos |
| Lockfiles | `free · OSV-Scanner` | Google OSV (free) |
| Owned dep | `free · owned-deps / anvil-hotline` | govulncheck + Trivy + OSV on pin |
| Repo FS | `free · trivy / filesystem` | Trivy vuln/secret/misconfig + SBOM |
| PR graph | `free · dependency-review` | GitHub free for public repos |
| Console | `free · npm audit / console` | npm audit (free) |
| Dockerfiles | `free · hadolint / Dockerfiles` | Hadolint (free) |
| Helm | `free · checkov / helm` | Checkov OSS (free) |
| Secrets | `free · gitleaks / secrets` | Gitleaks free for public |
| Workflows | `free · zizmor / workflows` | zizmor (free, advisory) |
| Images ×7 | `free · image / <component>` | **Trivy + Grype + CycloneDX SBOM** |
| Trust | `free · OpenSSF Scorecard` | Scorecard free for public |
| Gate | `free · security-gate` | Requires the required jobs green |
| Updates | Dependabot | Free alerts + PRs (gomod, npm, docker, actions, helm) |

### Containers (breadth of tooling)

Each of the seven images gets its **own named free check run**:

| Component | Image |
| --- | --- |
| controller | `anvil-agents` |
| codex | `anvil-agent-run-codex` |
| opencode | `anvil-agent-run-opencode` |
| grok-build | `anvil-agent-run-grok-build` |
| hermes | `anvil-agent-run-hermes` |
| openclaw | `anvil-agent-run-openclaw` |
| pi | `anvil-agent-run-pi` |

Per image: Trivy (HIGH/CRITICAL), Grype (high+), CycloneDX SBOM artifact.

### Owned first-party pin

Runner Dockerfiles pin `ANVIL_HOTLINE_VERSION`. The owned-deps job requires a
single pin and scans that public tag.

## When it runs (all free public minutes)

- Every PR / push to `master` / `main`
- Weekly schedule
- GitHub Releases
- Manual `workflow_dispatch`
- Publish workflow waits on security before GHCR push

## Local optional mirrors

```bash
make security          # govulncheck + gosec
make security-trivy    # Trivy all containers
make security-all      # both
```

## Branch protection tip

Protect `master` with required check: **`free · security-gate`**.

## GitHub settings (once, free)

1. **Code security** — enable Dependency graph, Dependabot alerts, Code scanning.
2. No paid Advanced Security required for a **public** repository.
