# OpenAI Build Week 2026 provenance ledger

This document distinguishes the pre-existing foundation from work eligible for
OpenAI Build Week review. Repository creation alone is not treated as proof
that imported code was new.

## Official window and entry scope

- Track: Developer Tools
- Event window: July 13, 2026 at 9:00 a.m. PDT through July 21, 2026 at
  5:00 p.m. PDT
- Pre-event comparison baseline:
  `HazyForge/anvil-primaris@0f5849ae5ada9ac507e50535d0ab2b4485cd5207`
- Standalone repository first commit:
  `0944cbbadfb4da40543064da2eff4592ca356fce`
- Final event tag and SHA: **not recorded before the event closed**

This historical ledger records project provenance and evidence boundaries; it
is not a current release checklist.

## Prior work

Before the event window, Anvil Primaris already contained an AgentRun
operator foundation: CRDs and reconciliation, profiles and schedules, PVC-backed
state, adverse-event handling, archive/feedback concepts, and five runner
adapters. The initial standalone commit copied and reorganized much of that
foundation. That prior functionality is context, not newly claimed Build Week
work.

## Meaningful Build Week extensions

| Eligible addition | Commit evidence | What materially changed |
| --- | --- | --- |
| Standalone ownership and dependency severing | `0944cbb`, `42899d4` | Created the independent Go module, chart, CI/release ownership boundary, and removed Anvil Primaris API imports while disclosing that much of the controller, runner, test, and documentation foundation was copied. |
| Reusable local image and release workflow | `1448332`, `726c619`, `e277391` | Unified component builds, digest locks, chart packaging, and fail-closed local publication gates. |
| Provider-neutral read-only OIDC API | `bcb5ae3`, merge `9d5b4f1` | Added a separate-process AgentRun summary and SSE log API with exact issuer/audience, claim binding, CORS, namespace authorization, and read-only RBAC. |
| Composable agent runtimes and skills | `0e63f2d`, `81cf639`, `72de732`, merges `470b495`, `fc5648e`, `383eeb2` | Added reusable harness and skill-set APIs, deterministic resolution, payload/effective digests, overrides, and documentation. |
| Public v0.1.1 release | `726c619`, merge `db5f5f3` | Published the first version-coupled chart and six-image linux/amd64 digest lock. |
| Production cutover and post-release hardening | `e0f87a6`, `e7e9f60` through `cc4bad0` | Added production cutover constraints, exact v0.1.1 deployment pins, archive placement, local release gates, and API/release documentation. These commits are later than the v0.1.1 tag. |
| Provider-neutral adverse evidence | `060af9f` through merge `94d031e` | Replaced provider coupling with immutable adverse signals, narrow read-only discovery, deduplication receipts, and upgrade-safe behavior. |
| Composable external tools and self-development fleet | merge `948226d` (PR #11) | Added `AgentToolSet`, deterministic tool composition, controller and chart wiring, and a repository-local self-development fleet whose schedule remains suspended pending external policy prerequisites. |
| Standalone Kubernetes AgentRun CLI | merge `7741792` (PR #12) | Added Kubernetes-authenticated append-only run creation, ownership-checked status/log diagnosis, tests, and operator documentation without importing Anvil Primaris APIs. |
| PostgreSQL archive chart modes | merge `312c8ce` (PR #13) | Added external, standalone, and CloudNativePG archive modes with retained data-bearing resources, strict archive schema validation, chart tests, and migration/upsert integration coverage. |
| First-class OpenCode harness | merge `a14b0f5` (PR #16) | Added the `openCode` backend, controller/chart/release wiring, a checksum-pinned non-root OpenCode 1.18.3 image, provider-native model selection, contract tests, and a real read-only repository-inspection smoke run. |
| Event provenance and no-build Kind proof | merge `e71728e` (PR #15) | Added the prior/new disclosure, Codex/GPT-5.6 collaboration account, checksum-pinned non-root prerequisite installer, public-artifact test, and video script. |

Generated CRDs and copied files are not counted as independent innovation.
Draft or unmerged branches were excluded from the event-period account. The
additions listed above reached `master`, but they were not part of release
`v0.1.1`; runnable post-v0.1.1 behavior requires a matching release tag and
published artifacts.

## Codex and GPT-5.6 collaboration

The primary build thread was run through Codex with recorded model identifier
`gpt-5.6-sol`. Codex performed code archaeology, extracted the runtime, removed
cross-repository type dependencies, implemented and reviewed API/controller
changes, regenerated CRDs, wrote tests and docs, built release automation,
executed Kind validation, and investigated production/release failures. The
maintainer reviewed the diffs and retained authority for scope, security,
product, release, and compatibility decisions.

The event-specific Codex Session ID was not recorded before the event closed.

An authenticated GPT-5.6 AgentRun was not recorded before the event closed.
The credential-free Kind path is intentionally not presented as GPT-5.6 proof.

## Human decisions

The maintainer, not Codex, made and approved these material decisions:

- make `anvil-agents` the true standalone runtime owner rather than a wrapper;
- preserve `control.anvil.hazyforge.io/v1alpha1` for live owner-reference safety;
- keep `AgentRun` append-only and represent new intent as a new object;
- keep application/target references opaque and policy-plane authority external;
- isolate the OIDC-facing API in a separate read-only process and RBAC boundary;
- use one Kubernetes Job per run and scale independent workloads horizontally;
- make composition, storage, credentials, resource bounds, and placement
  explicit; and
- require repository-owned verification, Kind testing, immutable image pins,
  and human release approval.

## Claims deliberately not made

- The full operator was not created from scratch during Build Week.
- The standalone repository creation date is not presented as the age of every
  line of code.
- The no-credential Kind test does not exercise or prove GPT-5.6.
- Unmerged branches and features absent from the published releases are not
  part of the event-period claims.
- Linux arm64, older Kubernetes versions, hostile multi-tenant execution, and
  one-run multi-node compute are not certified capabilities.
- The read-only OIDC API cannot create or mutate AgentRuns.

## Event closeout

The event closed without a recorded authenticated GPT-5.6 run, final event tag,
or completed submission. Current release claims belong in the release notes and
must be supported by matching source, chart, image, and verification evidence.
