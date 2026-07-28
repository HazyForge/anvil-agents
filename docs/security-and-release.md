# Security scans and Anvil Primaris release gates

`anvil-agents` is a **public** repository. Security checks run in GitHub
Actions (public minutes) and as Anvil Primaris **TestContract** suites so
release promotion fails closed without a green scan.

## What runs

| Check | Local | GitHub Actions | Primaris TestContract |
| --- | --- | --- | --- |
| `make verify` / Kind e2e | `make verify`, `make kind-e2e` | `ci.yaml`, publish `verify` job | suite `standalone-operator` |
| govulncheck (module + binary) | `make security` | `security.yml`, publish `security` job | suite `security` |
| gosec | `make security` | `security.yml`, publish `security` job | suite `security` |
| CodeQL | — | `security.yml`, publish | — (GHA Security tab) |
| Dependency review | — | PRs only | — |

## Local

```bash
make verify
make security
```

## GitHub (public Actions)

- **Every PR / push to master:** `ci.yaml` + `security.yml`
- **Weekly schedule:** `security.yml` (cron)
- **Each publish release:** `publish.yaml` runs `verify` + `security` **before**
  GHCR image/chart publish (`needs: [verify, security]`)

## Drive with Anvil Primaris

Repo contracts (checked into Git):

- `.hazyforge/tests.yaml` — TestContract `anvil-agents`
  - `gates.release.suites: [standalone-operator, security]`
- `.hazyforge/release.yaml` — `testGateSuites: [standalone-operator, security]`
- Application `anvil-agents` already points `spec.testContract` at
  `.hazyforge/tests.yaml` (cluster GitOps under
  `.hazyforge/clusters/anvil-primaris/.../manifests/application.yaml`)

On a Primaris build worker with the repo checkout:

```bash
# Security suite only
anvilctl test run --application anvil-agents --suite security

# Full release gate set required by ApplicationRelease
anvilctl test run --application anvil-agents --gate release
```

Local Primaris-driven image release also runs the security gate:

```bash
VERSION=vX.Y.Z make release-primaris-fast   # make security before publish
VERSION=vX.Y.Z make release-primaris       # verify + kind-e2e + security
```

`--skip-verification` / `RELEASE_SKIP_VERIFICATION=true` skips both unit
verify and security (escape hatch only).

An `ApplicationRelease` for application `anvil-agents` must list these gate
suites (or inherit from the TestContract `gates.release`). Missing or failed
security evidence must fail the release.

Deployed-image audits use the Primaris skill
`audit-deployed-security-with-trivy` against cluster image digests — that is
cluster posture, not this module’s source release gate.

## Required GitHub settings (once)

For CodeQL SARIF on a public repo:

1. **Settings → Code security** — enable Code scanning, Dependency graph,
   Dependabot alerts.
2. Default `GITHUB_TOKEN` permissions allow `security-events: write` as set in
   the workflows.

## Failure policy

- `govulncheck` or `gosec` failures block merge (PR), block GHA publish, and
  block local `release-primaris` full/fast.
- Primaris release gates treat suite `security` as required.
- Hot controller cutovers (`make release-primaris-hot`) intentionally skip
  the full release security gate for iteration speed; run `make security`
  before promoting a hot pin into a versioned release.
