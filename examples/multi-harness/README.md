# Multi-Harness Schedule

The schedule rotates independent runs between Codex and Pi profiles. `Queue`
and `maxConcurrentRuns: 1` preserve ordering while each interval selects the
next named template. Change to `Allow` with an explicit cap for parallel lanes.

This is distributed repeat execution, not a shared live conversation. Persist
handoff evidence in Git, an external knowledge service, or intentionally
managed volumes. Create the referenced runner ServiceAccount and provider
Secrets before applying this illustrative example, and pin backend image
digests in production.
