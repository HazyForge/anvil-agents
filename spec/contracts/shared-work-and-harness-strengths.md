# Shared work and harness strengths: candidate contract

Product direction supplied by the operator on **2026-10-26**. Behavior in this
contract is **Proposed**, except the explicitly Observed baseline at the end.
The requirement is a coherent Anvil layer over the chats and work it coordinates,
with user-defined strengths guiding harness and model selection. This document
does not claim that the complete layer is implemented.

## 1. Anvil's superset of conversations and work

Anvil maintains the overarching work context and links to the conversations,
messages, tasks, runs, decisions, and artifacts produced through different
harnesses. The person can continue discussing a project in Anvil while different
parts of the work go to Codex, Claude Code, or another configured execution setup.
Results return to that work context with their source and status intact.

The shared layer includes the user's objective and preferences, work summary,
relevant decisions, pending questions, and references to participating harness
conversations. Each conversation retains its own identity, transcript, execution
binding and access rules. A project may have several conversations on one harness
and others on different harnesses; a harness name is not a conversation ID.

“Superset” describes Anvil's coordination view, not a requirement to merge every
native transcript into one file or replay all history into every model. The
manager retrieves the relevant authorized context for each delivery, records its
provenance, and links the resulting execution and artifacts back to the work.
The person can open a constituent conversation without losing that wider context.

Scope starts with **Anvil conversations first**. Independently created native
chats would need a separately defined import/attachment path with source identity,
access and freshness semantics. This direction does not silently authorize a
scan of every local harness history directory.

## 2. User-defined harness strengths

A selectable setup combines a harness with an execution location, model
configuration, tool access and scope. The user can name it, give it editable tags,
and describe when to prefer or avoid it. Multiple setups may use the same harness
with different models or tools. These definitions are routing preferences; they
do not assert that one harness brand has an inherent or universally superior
capability.

| Candidate field | Meaning |
| --- | --- |
| Identity and revision | Stable setup reference and the definition version used for a decision |
| Name and description | User-facing identity, such as “Implementation,” “Review,” or “Video production” |
| Harness and location | Adapter plus local/native/WSL or remote execution binding |
| Agent model | Configured provider/model used by the agent, or an explicit native-default/unknown value |
| Strength tags | User-editable task labels such as `code-review`, `image-generation`, `video-generation`, `research`, or project-specific names |
| Preference notes | Conditions under which the user prefers this setup, including weaker areas and avoid rules |
| Required tools and generation models | Capabilities and model bindings used through tools, distinct from the agent's own model |
| Scope and constraints | Accessible workspaces and artifacts, allowed actions, required capabilities, and explicit user pins |
| Availability evidence | Installed/configured/usable/busy/unknown state, its source and observation time |

An agent model and an image/video model are separate choices when generation
happens through a tool. A setup tagged `video-generation` might use an agent
model to orchestrate a specific video service/model. Its tag does not prove the
tool is installed, the model is supported, or the necessary access is available.
Those facts come from the adapter and current execution configuration.

For example, the user may define these **illustrative preferences**, not measured
capability claims or ready-to-use configuration:

| Named setup | User-assigned strengths | Configured binding |
| --- | --- | --- |
| Implementation | `code-changes`, `tests` | Codex with the user's selected agent model and repository workspace |
| Review | `code-review`, `architecture` | Claude Code with the user's selected model and review access |
| Image production | `image-generation`, `image-editing` | A supported harness plus an image tool and selected image model |
| Video production | `video-generation` | A supported harness plus a video tool and selected video model |

The person can revise these assignments. Suggestions from past results, if later
added, remain separate from user-authored preferences and do not silently rewrite
them. The manager's classification model is also distinct from all execution and
media-generation models.

## 3. Choosing the conversation and the executor

Routing resolves two related questions: **what work does this message belong to?**
and **which eligible setup should perform the next action?** A continuation may
stay in its current conversation, while a new specialist task can be delivered
to a linked conversation without losing the parent work context.

1. Resolve the intended work, message intent, explicit target/model choices, and
   whether any execution is needed. Reading saved status requires no new harness.
2. Retrieve relevant conversations and user-defined setup strengths within the
   person's access scope. Keep candidate work identity separate from executor ID.
3. Check hard requirements: adapter capability, required tools/models, current
   availability, workspace/artifact access, explicit pins, and authority. A tag
   match alone cannot make a setup eligible.
4. Rank eligible choices using task fit, user preferences, existing context,
   continuity, and known constraints. Continuing an active conversation can be
   preferable to handing off merely because another setup has a matching tag.
5. Record the selected conversation/action and setup, relevant matches, exclusions,
   definition revision, and a concise reason. Admit execution through the existing
   runtime boundary; retain its receipt and actual model/tool identities when known.
6. Return the resulting status, public reply and artifact references to the shared
   work view. Keep pending decisions and verification criteria attached to that work.

An explicit user choice remains a constraint until changed. An unavailable pinned
model is not silently replaced. When no pin exists, a known suitable authorized
alternative can proceed under the user's preferences, with its selection visible.
Ask only when missing information or a material unresolved choice prevents action.
If no eligible setup exists, explain the concrete missing capability or binding.

Editing strengths affects future decisions. Accepted work retains its original
binding and selection receipt; changing tags does not retarget a running attempt.
Replanning or retry creates an explicit new decision/attempt as appropriate.

## 4. Handoff and live UI

A handoff carries the source work/conversation, recipient, bounded task and
relevant context, artifact references, expected result, execution constraints, and
a stable delivery identity. It distinguishes the user's original words from
manager instructions and another agent's messages. Model selection or context
delivery does not transfer additional credentials or authority.

The UI exposes the shared work and its linked conversations, including which
harness/model is handling each part. Setup cards let the user edit strength tags,
notes and model/tool bindings, and distinguish declared strengths from observed
availability. The routing view shows the matched tags, selected setup and model,
relevant alternatives or exclusions, and delivery/execution/verification states.
Unknown actual model identity remains visibly unknown rather than copied from a
preferred label.

For a mixed workflow, the person can request a code change, a review, an image,
then a video in one Anvil conversation. The manager preserves the shared brief,
routes the parts to eligible setups, passes artifact references between them,
and shows each result and unresolved question in that same work view. Artifact
dependencies wait for real results; a queued image task is not an available image.

## 5. Scenarios for acceptance

These are proposed acceptance scenarios, not tests reported as passing.

| Scenario | Expected result |
| --- | --- |
| Continue code work, then request an independent review | Keep the original conversation; deliver the review to a linked eligible setup; show origin, recipient and real reply in shared work. |
| Two setups use one harness with different models/strengths | Select the matching configured setup, not just the harness brand; preserve any explicit model pin. |
| Request an image, then a video using that image | Choose the configured image and video setups; distinguish their agent and generation models; pass the actual image artifact after completion. |
| A tag says video, but its required tool is unavailable | Exclude or visibly hold that setup; use an eligible permitted alternative or explain the missing binding, without pretending generation started. |
| Change tags while work is running | Preserve accepted binding and receipt; apply the revised definitions to future choices. |
| Ask “how is that going?” after several harness handoffs | Read the related Anvil work, pending questions and receipts without launching new generation or asking the user to find the native chat. |
| Reconnect after an accepted handoff | Restore linked work and delivery identity; reconcile rather than duplicating the task. |
| A useful-looking conversation belongs to another owner | Exclude inaccessible content and artifacts before context selection or dispatch. |

## 6. Observed baseline and remaining design

**Observed at `d97dc1b`:** Anvil has wrapper conversations, local subject/intent
routing, remote subject storage and bounded remote coordination. Local new work
uses the chosen default harness; existing work retains its harness binding.
The local classifier is not currently a strength-based harness/model selector.
Backend-specific model configuration exists remotely; local recipes preserve
native model/provider configuration.

**Observed:** The Desktop catalog can discover Claude Code, but its current entry
has no delegatable invoke recipe. Codex has one. The proposed cross-harness
workflow includes an actual Claude adapter as implementation work, rather than
claiming discovery proves delegation. There is no implemented user-authored
strength profile or combined media-aware selection contract in current local
routing. See E10 in the [evidence index](../implementation-notes/reconnaissance-evidence.md).

**Unknown:** Exact persisted schema, local/remote synchronization and ownership of
setup definitions, model/capability discovery protocols, and ranking calibration.
The direction and user-editable nature are specified; these remaining mechanism
choices can be resolved during implementation without repeated confirmation of
the product intent.
