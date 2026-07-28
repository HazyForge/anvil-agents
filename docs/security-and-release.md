# Security scans and Anvil Primaris release gates

`anvil-agents` is a **public** repository. Security checks run in GitHub
Actions (public minutes, visible check runs / artifacts) and as Anvil Primaris
**TestContract** / `release-primaris` steps so release promotion fails closed
and **shows that scans ran**.

## What runs

| Check | Local / Primaris release | GitHub Actions |
| --- | --- | --- |
| `make verify` / Kind e2e | `make verify`, `make kind-e2e` | `ci.yaml`, publish `verify` |
| govulncheck + gosec | `make security` | `security.yml`, publish `security` |
| **Trivy per container** (×7) | `make security-trivy` / `make security-release` | `security.yml` job `trivy / <component>`; publish same matrix |
| CodeQL | — | `security.yml` / publish |
| Dependency review | — | PRs only |
| OpenSSF Scorecard | — | push/schedule/release |

Containers scanned (each is its own GHA check run):

1. `controller` → `anvil-agents`
2. `codex` → `anvil-agent-run-codex`
3. `opencode` → `anvil-agent-run-opencode`
4. `grok-build` → `anvil-agent-run-grok-build`
5. `hermes` → `anvil-agent-run-hermes`
6. `openclaw` → `anvil-agent-run-openclaw`
7. `pi` → `anvil-agent-run-pi`

Trivy fails the gate on **HIGH/CRITICAL** findings (unfixed CVEs ignored by
default so base-image lag does not false-block forever). Reports:

```text
dist/security/trivy/summary.txt     # RESULT=PASS|FAIL (Primaris evidence)
dist/security/trivy/<component>.txt
dist/security/trivy/<component>.json
dist/security/trivy/<component>.sarif
```

## Local

```bash
make verify
make security              # source only (govulncheck + gosec)
make security-trivy        # build + Trivy every container
make security-release      # full Primaris release security gate
```

Shared implementation: `hack/security-trivy-images.sh`.

## GitHub Actions (show people the scans ran)

- **Every PR / push:** `security.yml`
  - Source jobs: govulncheck, gosec, CodeQL
  - **Seven named jobs** `trivy / controller` … `trivy / pi` (matrix)
  - Job summaries + downloadable artifacts + SARIF (Security tab)
  - `security-gate` requires all of the above
- **Publish release:** `publish.yaml` needs `[verify, security, trivy]` so
  GHCR publish cannot proceed without every container scan green

Public repos get free Actions minutes for this volume of matrix builds.

## Drive with Anvil Primaris

Repo contracts:

- `.hazyforge/tests.yaml` — TestContract `anvil-agents`
  - `gates.release.suites: [standalone-operator, security]`
  - suite `security` lanes:
    1. `govulncheck-and-gosec`
    2. `trivy-each-container` → `make security-trivy`
- `.hazyforge/release.yaml` — same suites
- Application `anvil-agents` points `spec.testContract` at `.hazyforge/tests.yaml`

```bash
# Security suite only (source + every container)
anvilctl test run --application anvil-agents --suite security

# Full release gate
anvilctl test run --application anvil-agents --gate release
```

Local Primaris-driven image release runs the same gate before publish:

```bash
VERSION=vX.Y.Z make release-primaris-fast
# → make security-release (source + Trivy ×7)
# → requires dist/security/trivy/summary.txt RESULT=PASS
# → then publish-release / pin deploy.yaml

VERSION=vX.Y.Z make release-primaris   # + Kind e2e
```

`--skip-verification` skips verify **and** security-release (escape hatch only).

Hot controller cutovers (`make release-primaris-hot`) skip the full security
gate for iteration speed. Run `make security-release` before promoting a hot
pin into a versioned release.

Deployed-image audits of *already running* digests use the Primaris skill
`audit-deployed-security-with-trivy` — cluster posture, separate from this
source/release gate.

## Required GitHub settings (once)

1. **Settings → Code security** — Code scanning, Dependency graph, Dependabot.
2. Workflows request `security-events: write` for SARIF upload.

## Failure policy

- govulncheck, gosec, CodeQL, or **any** container Trivy failure blocks merge
  (PR security-gate), GHA publish, Primaris TestContract suite `security`, and
  local `release-primaris` full/fast.
- Reviewers should see seven green `trivy / …` checks (or red with reports).
