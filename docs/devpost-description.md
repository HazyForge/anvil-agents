# Devpost description draft

Copy this only after replacing bracketed fields and reconciling it with the
immutable submission tag.

## Project name

Anvil Agents — Distributed Codex Workloads on Kubernetes

## Track

Developer Tools

## Description

Developer agents do more than inference: they clone repositories, compile
large codebases, run integration suites, scan dependency graphs, query durable
indexes, and wait on external systems. Those workloads overwhelm a laptop and
are difficult to schedule, observe, reproduce, or stop safely.

Anvil Agents is an open-source Kubernetes operator that turns each independent
agent task into an append-only `AgentRun` and an isolated Job. Operators compose
reusable run profiles, harness profiles, skill sets, credentials, resource
limits, placement, and PVC-backed state. The same control plane can run Codex,
other supported harnesses, or a custom container while preserving a common
payload and structured-status contract. An optional, separately deployed OIDC
API exposes authorized summaries and live logs without granting Secret or
mutation access.

The Build Week extension extracted the runtime from a pre-existing Anvil
Primaris foundation into a standalone open-source owner, removed control-plane
dependencies, added deterministic harness/skill composition, a provider-neutral
read-only OIDC stream, reusable release automation with immutable image locks,
provider-neutral adverse evidence, production cutover hardening, and a free
no-build Kind test. The exact prior/new boundary and commit evidence are in
`docs/build-week-2026.md`.

Codex with GPT-5.6 accelerated the extraction, API/controller implementation,
CRD generation, testing, security hardening, deny-by-default auth tests,
documentation, release automation, Kind validation, and failure diagnosis. The
human entrant chose and approved the ownership, append-only API, security/RBAC,
compatibility, composition, deployment, and release boundaries.

**Evidence gate:** add the following final sentence only after the recorded run
has succeeded and the ledger contains its evidence: “The demo shows an actual
Codex-backed `AgentRun` explicitly selecting the verified GPT-5.6 model ID,
producing useful output, and reaching structured terminal status.”

Judges can test the released operator for free without rebuilding or supplying
credentials on Linux amd64. A non-root prerequisite script installs the pinned,
checksum-verified Kind, kubectl, and Helm binaries; the judge script then
installs the public digest-pinned chart, executes two immutable Jobs, and proves
payload composition, structured status, and PVC persistence.

## Final links

- Repository/tag: [ADD IMMUTABLE URL]
- Public narrated YouTube demo under three minutes: [ADD URL]
- Judge instructions: [ADD TAGGED JUDGING.md URL]
- `/feedback` Codex Session ID: [CONFIRM IN ORIGINAL THREAD]
