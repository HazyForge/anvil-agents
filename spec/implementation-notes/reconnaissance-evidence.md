# Implementation evidence index

Source baseline: `648aab12c87e617fb59fe50d4cd8db2b50d8a1c8` on
`codex/subject-vault-routing`, inspected 2026-10-26. The first spec adds documents
on top of that baseline. This index links current code and operator documents;
it does not elevate implementation choices into normative requirements.

## Evidence map

| ID | Area | Primary source and supporting detail |
| --- | --- | --- |
| E1 | Run lifecycle and runtime selection | [Run types](../../api/v1alpha1/agent_run_types.go), [controller](../../internal/controller/agent_run_controller.go), [composition resolution](../../internal/controller/agent_run_composition.go), [runtime guide](../../docs/agent-run.md) |
| E2 | Composition and capability ownership | [Composition guide](../../docs/composition.md), [harness types](../../api/v1alpha1/agent_harness_profile_types.go), [composition resolution](../../internal/controller/agent_run_composition.go), [repository ownership](../../AGENTS.md) |
| E3 | Schedules, chains, triggers and adverse evidence | [Schedule types](../../api/v1alpha1/agent_schedule_types.go), [chain controller](../../internal/controller/agent_chain_controller.go), [chain guide](../../docs/agent-chain.md), [webhook guide](../../docs/external-triggers.md), [adverse integration](../../docs/integrating-adverse-sources.md) |
| E4 | API and authority | [Routes](../../internal/runapi/server.go), [run creation](../../internal/runapi/runs_create.go), [composition writes](../../internal/runapi/composition_management.go), [launch controls](../../internal/runapi/controls.go), [log validation](../../internal/runapi/logs.go), [security boundary](../../docs/security.md), [repository rules](../../AGENTS.md) |
| E5 | Remote conversations and coordination | [Chat service](../../internal/runapi/chat.go), [execution/outbox reconciliation](../../internal/runapi/chat_execution.go), [coordination](../../internal/runapi/chat_coordination.go), [subject handlers](../../internal/runapi/chat_subjects.go), [subject store](../../internal/chat/subjects.go), [chat guide](../../docs/standing-chat.md), [subject guide](../../docs/subject-vault-routing.md) |
| E6 | Local wrapper, routing and manager loop | [Local conversation registry](../../web/desktop/src/state/localConversations.ts), [routing](../../internal/localrouting/routing.go), [loop store](../../internal/desktop/loop_store.go), [worker](../../internal/desktop/loop_worker.go), [answer handling](../../internal/desktop/local_loop_answer.go), [workspace guards](../../internal/desktop/loop_workspace.go), [local routing guide](../../docs/local-manager-routing.md), [loop guide](../../docs/local-manager-loop.md) |
| E7 | Storage, maintenance, archive and retention | [Volume copy](../../docs/volume-copy.md), [auth session types](../../api/v1alpha1/agent_auth_session_types.go), [auth controller](../../internal/controller/agent_auth_session_controller.go), [archive controller](../../internal/controller/agent_run_archive.go), [archive guide](../../docs/archive.md), [volume lifecycle](../../docs/agent-run.md) |
| E8 | Standing and actor runtime maturity | [Process backend](../../internal/standing/process.go), [native resume recipes](../../internal/standing/process_resume.go), [standing claims](../../internal/standing/claim.go), [actor lifecycle/generation](../../internal/controller/agent_run_substrate_live.go), [standing development record](../../docs/standing-inprocess-harness.md), [protocol proposal](../../docs/unified-harness-protocol-spec.md), [mailbox boundary](../../docs/chat-delivery.md) |
| E9 | Product gaps and verification entry points | [Roadmap](../../docs/design-roadmap.md), [Makefile](../../Makefile), [security/release gates](../../docs/security-and-release.md), [Desktop guide](../../docs/desktop.md), [local execution guide](../../docs/desktop-prime-execution.md) |
| E10 | Harness strengths and model-selection baseline | [Local descriptors](../../internal/localrouting/types.go), [current routing policy](../../internal/localrouting/routing.go), [catalog and delegation recipes](../../internal/desktop/catalog.go), [local manager controls](../../web/desktop/src/pages/LocalManagerPage.tsx), [backend model fields](../../api/v1alpha1/agent_run_types.go), [local conversation records](../../web/desktop/src/state/localConversations.ts) |

E10 was inspected at `d97dc1b0d5623e773b6c0355145cecd5f52ca1e9` on 2026-10-26
following the operator's shared-work/strength-tags clarification. Local routing
selects subject/intent, validates runtime availability and uses a manually selected
default harness for new work. It has no user-strength or media-model ranking.
Claude Code is discoverable but lacks a local wrapper delegation recipe. Remote
backend model fields do not establish a common local setup schema; the model
reported on a classification decision identifies the classifier, not the executor.

## Interpretation and discrepancies

**Observed:** Some older explanatory documents describe only the original Job
plane or an earlier read-only API. Where they conflict with current source and
repository rules, this spec describes the narrower evidenced behavior:

| Earlier wording or implication | Current interpretation |
| --- | --- |
| Every AgentRun creates one Job | True of the default Job plane. Explicit InProcess and SubstrateActor selections have separate paths and gates. |
| API and Console never write resources | Gated run creation, composition mutation and launch controls exist; access remains explicitly authorized. |
| API never reads a Secret | Ordinary reads/chat do not gain Secret access. Opt-in signed webhook verification has a narrow same-namespace exception. |
| Remote chat always launches a cold Job | Default chat uses run/outbox semantics; optional standing process recipes can resume native sessions. Neither all harnesses nor all locations share this capability. |
| The early standing design has no live process implementation | The chronological development note contains earlier slices; current process backend and resume code supersede those slice descriptions. |
| Interactive mailbox / unified protocol implies native steering | Mailbox runtime hookup and connected protocol transport remain separate from the implemented turn execution and in-memory protocol library. |
| Schedules or councils imply arbitrary workflow graphs | Schedules rotate independent runs, councils describe workforce, and AgentChain implements linear completion sequencing. |
| Any assistant reply proves goal completion | Local verification currently proves process success plus a public reply; domain outcome verification needs additional evidence. |

The entry-point README is corrected with this draft. Historical/implementation
documents are retained with their narrower context; this is not a repository-wide
rewrite or a claim that every old paragraph is current.

## Verification evidence and its limits

**Observed source evidence:** The repository contains lifecycle, ownership,
authorization, persistence/replay, coordination, runtime, and browser scenarios.
Relevant entry points include:

- [Local loop backend scenarios](../../internal/desktop/loop_test.go) and
  [actual-host browser flow](../../internal/desktop/local_loop_browser_test.go).
- [Routing scenario definitions](../../internal/localrouting/scenarios.json),
  [independent fixture judgments](../../internal/localrouting/fixtures.json), and
  [bench implementation](../../internal/localrouting/bench.go).
- [Remote subject persistence and access tests](../../internal/runapi/chat_subjects_test.go)
  and [integrated browser flow](../../internal/runapi/chat_subjects_browser_test.go).
- [Standing ownership tests](../../internal/runapi/chat_standing_claim_test.go)
  and [native resume tests](../../internal/standing/process_resume_test.go);
  [standing hold projection](../../internal/runapi/chat_standing_hold.go) explains
  why raw run phases and conversational pending questions are not interchangeable.

The local routing suite defines nine scenarios and 30 turns (26 scripted
judgments and four manual choices). Fixtures supply explicit provider/harness
behavior. They demonstrate orchestration and failure handling, not live-model
accuracy. Product-wide scenarios in reconnaissance are proposed acceptance
coverage; writing them does not imply a corresponding suite was run.

`make verify` is the repository publication gate. Broader Kind, provider,
authenticated browser, installation, security and release checks have separate
entry points and evidence. A source check or documentation change does not prove
that a release is installed, production configuration enables a capability, or a
real provider accepts its credentials.

## Updating this specification

**Proposed maintenance rule:** Record the inspected revision, the relevant source
or observed interface, and the validation level when changing a claim. Keep
Unknowns explicit; move them to Observed only with evidence. Update the domain
contract when behavior changes, and the implementation index when its mechanism
changes. Avoid deriving a universal guarantee from one adapter or happy path.
