# Domain glossary

Status: first-pass candidate vocabulary. **Observed** unless labelled otherwise.
The [evidence index](../implementation-notes/reconnaissance-evidence.md) maps these
concepts to the present implementation; names do not require one future schema.

| Concept | Candidate definition | Identity, ownership, and lifecycle |
| --- | --- | --- |
| Principal | Person or workload whose authority is checked at a boundary | An OIDC subject, Kubernetes identity, and local operating-system user are distinct identities; there is no universal identity binding implied by their names. |
| Agent | A reusable role with instructions, composition, and execution scope | Usually represented remotely by a run profile. A role is neither a model nor a running process. Harness-only chat is also supported. |
| Run profile | Reusable defaults for a role, prompt, scope, intent, and selected composition | Mutable configuration; a particular execution records its resolved inputs. |
| Harness | Runtime adapter that carries a task to an executable agent system | Distinct from its model provider, provider account, process, and conversation. |
| Harness profile | Reusable backend and execution envelope | Selects runtime, identity, credentials, storage, placement, and limits. Changing it does not itself migrate memory or authenticate a provider. |
| Skill | Named instructions or delegated persona | Does not own credentials or create a child execution merely by being present. |
| Tool contract | Installation and verification description for an external capability | Does not own the external service or grant authority to use it. |
| Council | Named workforce inventory and optional interaction guidance | Membership does not combine permissions or automatically dispatch work. |
| Work item | **Proposed:** user objective tracked across related conversations and executions | No single universal work-item entity currently joins local tasks, remote runs, chains, and subjects. |
| Conversation / thread | Anvil-owned history and execution binding for successive messages | A wrapper over a harness. Local browser records and remote database threads currently have different persistence and access rules. |
| Subject | Named topic or ongoing concern used by manager routing | Remote owner-scoped metadata points to a chat thread; local routing works over saved Anvil conversations. These are not one replicated store. |
| Message | User input, public assistant reply, or addressed coordination content | Retains its originating conversation. Historical text is context, not new execution authority. |
| Turn | One accepted conversational request and its resulting execution/reply | Remote acceptance uses an outbox and append-only AgentRun; local direct Chat and durable Manager have separate execution paths. |
| AgentRun | Append-only cluster execution intent with controller/API-owned status | A retry with new execution intent creates a new record. Runtime selection determines whether a Job exists. |
| Local manager task | Host-owned accepted local work with frozen intent and execution binding | Contains distinct attempts, decisions, and transition evidence. It is not a Kubernetes AgentRun. |
| Attempt | One execution of a local manager task's saved intent | Retrying adds an attempt; it does not erase the previous attempt or its effects. |
| Decision | A pending choice attached to particular work | Local recovery decisions carry IDs, versions, and a bounded set of choices. Answering one does not answer another. |
| Routing judgment | Classification of a message's subject and intent | A model or manual selection supplies evidence for routing; it is not authorization or proof of delivery. |
| Receipt | Durable record of acceptance, selection, dispatch, or completion | Links to actual target and execution identities. Acceptance and completion are different receipts. |
| Workspace | Filesystem location used by execution | Local runtime/distro and canonical folder are part of the binding. A folder name alone is not a portable cross-machine identity. |
| Durable home / data volume | Explicit state retained beyond one cluster execution | Owned PVC identity and lifecycle are separate from a transient Job. It can contain sensitive provider state. |
| Schedule | Interval or manual cadence creating independent runs | Rotation and concurrency policy do not imply dependencies between templates. |
| Chain | Completion-driven ordered workflow | Current contract is linear; children are append-only runs, with status-only handoff. |
| External trigger | Authenticated inbound event source that targets work | Current implemented receiver is signed GitHub webhooks, not an arbitrary event bus. |
| Adverse signal / situation | Provider-neutral evidence and its buffered/deduplicated aggregation | Can inform a configured responder without importing consumer API types. |
| Launch control | Scope-based gate for starting work | A pause does not mean an already running generation or Job was interrupted. |
| Observation | Recorded state or event available to the person or manager | Detecting an issue does not by itself dispatch a new attempt. |
| Verification | Evidence that a stated completion criterion was checked | Process success, public reply, domain result, installation, and deployment are separate claims. |

## Distinctions to preserve

- Anvil conversation identity is separate from a native provider session ID.
- Agent role, harness adapter, model, runtime location, and execution principal
  are independently meaningful choices.
- A local manager task, remote turn, AgentRun, and Job are related concepts with
  different persistence and lifecycle rules; they are not interchangeable IDs.
- A suggested action, authorized action, accepted dispatch, and verified result
  describe different stages of work.
- **Unknown:** a common work-item identity and portable conversation format across
  local, remote, and future execution locations remain to be designed.
