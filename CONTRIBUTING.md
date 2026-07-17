# Contributing

Anvil Agents accepts focused issues and pull requests. Discuss new CRDs,
security boundaries, or compatibility changes in an issue before implementing
them; v1alpha1 still preserves existing wire identifiers intentionally.

## Development

Requirements: Go 1.25, Docker with BuildKit, Helm 3, Bash, and `rg`.

```bash
git checkout -b <type>/<short-topic>
make verify
make images
```

`make verify` regenerates code and CRDs, runs Go tests and builds, checks the
Helm contract, and validates all runner entrypoints. Generated changes belong
in the same commit as their API source change. Build scripts are the canonical
local contract; GitHub Actions is optional automation around them.

Keep changes scoped, add risk-appropriate tests, preserve unrelated work, and
document user-visible behavior. Do not commit credentials, real tokens, live
cluster exports, or proprietary prompt and skill content.

## Pull Requests

Describe the problem, behavior change, compatibility impact, security impact,
and verification performed. Call out CRD, RBAC, storage, identity, image, and
data-retention changes explicitly. Every new integration should keep provider
semantics behind an adapter and include a usable example.

By contributing, you agree that your contribution is licensed under
Apache-2.0.
