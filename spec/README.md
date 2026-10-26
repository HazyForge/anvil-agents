# Anvil Agents Product and Architecture Specification

This directory defines the emerging specification of **Anvil Agents as a whole**:
agent execution, composition, conversations, coordination, storage, authority,
and the interfaces through which people operate them. The manager loop is one
part of that product.

The target is an implementation-independent contract from which the product can
eventually be rebuilt and tested. Current source explains the observations;
it does not automatically make every implementation choice a requirement.

## Product intent

**Inferred:** Anvil Agents lets a person give work to agents, retain its context
and execution history, and follow what happens across local harnesses and
cluster runtimes. Roles, instructions, tools, runtime configuration, and durable
state have separate lifecycles. People can work directly with an agent or use a
manager to route and coordinate work.

**Proposed product direction:** A central conversation can move between the
person's ongoing work. The product identifies the relevant Anvil conversation,
chooses an authorized delivery action, shows its decisions and execution events
live, and follows observations through action and verification. It asks when
information or authority is missing or uncertainty materially changes the next
action. It continues when those are already clear. Existing implementation of
this direction is described separately from remaining work.

## Current status and evidence discipline

First product-wide draft, **2026-10-26 — Phase 1: architectural reconnaissance**.
This follows Hazy Trade's `spec/` organization: concepts, reconnaissance,
architecture, candidate contracts, and an implementation evidence index.
It does not introduce a new publication or approval workflow.

| Label | Meaning |
| --- | --- |
| **Observed** | Demonstrated by the inspected source, tests, checked-in configuration, or interface. It does not imply deployment or successful live-provider execution. |
| **Inferred** | Supported by observations, but not established as an intentional, complete product contract. |
| **Proposed** | Candidate future behavior or invariant; not a claim that it works today. |
| **Unknown** | A material question still requiring evidence or a design decision. |

These documents are candidate contracts, not newly accepted normative policy.
RFC-style **MUST** and **MUST NOT** are reserved for later accepted contracts.
Existing repository ownership and security rules in [AGENTS.md](../AGENTS.md)
remain in force; this draft does not reopen them. Unknowns are recorded for the
relevant implementation slice rather than turned into an immediate interview.

## Read the specification

| Document | Purpose |
| --- | --- |
| [Product reconnaissance](reconnaissance/phase-1.md) | Whole-product boundaries, user journeys, scenarios, and priorities |
| [Domain glossary](concepts/glossary.md) | Shared vocabulary and identity distinctions |
| [Architecture overview](architecture/overview.md) | Current execution and data boundaries |
| [Execution and durable state](contracts/execution.md) | Acceptance, runtimes, lifecycle, recovery, storage, and evidence |
| [Composition and authority](contracts/authority-and-composition.md) | Roles, capabilities, identities, API gates, and external policy |
| [Conversations and coordination](contracts/conversations-and-coordination.md) | Local and remote chat, manager routing, decisions, and peer delivery |
| [Implementation evidence](implementation-notes/reconnaissance-evidence.md) | Source baseline, verification entry points, discrepancies, and limits |

`spec/` owns the product model and candidate behavior. `docs/` continues to own
operator instructions, configuration recipes, implementation detail, and feature
design notes. In particular, [Manager live feedback](../docs/ManagerLiveFeedback.md)
supports the conversation contract; it is not the whole-product specification.

## Scope and baseline

**Observed scope:** the standalone execution operator and resource API; optional
OIDC API and Anvil Agents Console; Kubernetes CLI `anvil-agentctl`; Anvil Agents
Desktop and local harness adapters; composition, schedules, chains, event intake,
chat, manager routing, storage maintenance, and execution evidence.

External model services, native harness internals, Kubernetes, identity providers,
external tools, and consumer policy systems participate through boundaries.
Anvil Primaris and Anvil Hub are consumers or integrations, not prerequisites for
the standalone product.

Initial source baseline:
`648aab12c87e617fb59fe50d4cd8db2b50d8a1c8`, branch
`codex/subject-vault-routing`, [PR #317](https://github.com/HazyForge/anvil-agents/pull/317).
This includes manager work under review; it is not a release or deployment claim.
Later investigations should record their own revision and correct earlier
conclusions when stronger evidence appears.
