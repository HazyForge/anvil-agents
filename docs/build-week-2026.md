# OpenAI Build Week 2026 evidence ledger

This document distinguishes the pre-existing foundation from work eligible for
OpenAI Build Week judging. Repository creation alone is not treated as proof
that imported code was new.

## Official window and entry scope

- Track: Developer Tools
- Submission period: July 13, 2026 at 9:00 a.m. PDT through July 21, 2026 at
  5:00 p.m. PDT
- Pre-event comparison baseline:
  `HazyForge/anvil-primaris@0f5849ae5ada9ac507e50535d0ab2b4485cd5207`
- Standalone repository first commit:
  `0944cbbadfb4da40543064da2eff4592ca356fce`
- Final submission tag and SHA: **must be added after the final scope is
  selected and before Devpost submission**

The binding requirements are the [official rules](https://openai.devpost.com/rules),
not this checklist.

## Prior work

Before the submission period, Anvil Primaris already contained an AgentRun
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
| Submission compliance and no-build Kind proof | merge `e71728e` (PR #15) | Added the prior/new disclosure, Codex/GPT-5.6 collaboration account, judge guide, checksum-pinned non-root prerequisite installer, public-artifact test, video script, and checklist. |

Generated CRDs and copied files are not counted as independent innovation. Draft
or unmerged branches remain excluded unless they are merged into the immutable
submission tag and this ledger is updated. The additions listed above are now
on `master`, but they are not part of release `v0.1.1`; claims about runnable
post-v0.1.1 behavior require a matching final tag and published artifacts.

## Codex and GPT-5.6 collaboration

The primary build thread was run through Codex with recorded model identifier
`gpt-5.6-sol`. Codex performed code archaeology, extracted the runtime, removed
cross-repository type dependencies, implemented and reviewed API/controller
changes, regenerated CRDs, wrote tests and docs, built release automation,
executed Kind validation, and investigated production/release failures. The
entrant reviewed the diffs and retained authority for scope, security, product,
release, and compatibility decisions.

The required Codex Session ID remains **TODO** until `/feedback` is run in the
original core thread and its exact returned ID is copied to Devpost.

The runtime exposes GPT-5.6 as an explicit Codex-harness path. It becomes a
demonstrated meaningful product component only after the authenticated evidence
below is recorded before submission:

- AgentRun namespace/name: **TODO**
- immutable manifest/revision: **TODO**
- verified GPT-5.6 runtime model-ID evidence: **TODO**
- useful output and structured terminal status: **TODO**
- UTC execution time and video timestamp: **TODO**

The credential-free Kind path is intentionally not accepted as GPT-5.6 proof.

## Human decisions

The entrant, not Codex, made and approved these material decisions:

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
- Unmerged branches and features absent from the selected immutable tag are not
  part of the submission.
- Linux arm64, older Kubernetes versions, hostile multi-tenant execution, and
  one-run multi-node compute are not certified capabilities.
- The read-only OIDC API cannot create or mutate AgentRuns.

## Final evidence gate

Before submitting, replace every TODO, merge only intended scope, create an
immutable submission tag, publish a matching chart/controller release if any
post-v0.1.1 runnable feature is claimed, rerun `make verify` and the no-build
Kind test from that tag, and align the repository link, chart/images, demo,
description, and claims to the same selected scope.
