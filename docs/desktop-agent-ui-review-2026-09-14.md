# Desktop agent workspaces

The chat sidebar represents persistent agent identities. Selecting an agent opens
its continuing conversation; individual executions appear as activity within that
conversation. This pass applies [better-ui](https://www.skills.sh/jakubkrehel/skills/better-ui)
and [emil-design-eng](https://www.skills.sh/emilkowalski/skills/emil-design-eng).

## Identity and continuity

Local agents have stable IDs, names, icons, harness choices, working folders,
messages and drafts. Existing local histories migrate without changing their IDs
or discarding messages. Prime Agent is available before the first message. New
agents are created explicitly with a name; a prompt never renames an agent.
Native harness model, login and thinking settings remain unchanged.

Remote roster entries are AgentRunProfiles. The existing thread-create API accepts
`standing: true` with a profile name and returns that agent's canonical thread.
A PostgreSQL mapping and transaction lock keep concurrent clients on one thread.
The first request adopts an eligible direct conversation or creates one; harness
overrides and delegated child threads cannot become the standing conversation.
Historical chats remain accessible in Conversation details. Existing namespace
visibility and coordination policy are preserved; ensure also requires chat-read
permission because it can return an existing conversation.

This is durable conversation identity, not an always-running model process. Each
turn still launches its harness with conversation context. A durable multi-message
coordinator inbox, native session steering and automatic synthesis when peers
finish remain separate work; see the earlier activity review.

## Review

| Severity | Location | Before | After | Why |
| --- | --- | --- | --- | --- |
| HIGH | `internal/chat/standing.go:31`, `web/desktop/src/pages/EntityChatPage.tsx:174` | Opening a profile could pick unrelated history or create another thread | Canonical namespace/profile conversation with transactional adoption | Agent identity remains stable across clicks, clients and restarts |
| HIGH | `web/desktop/src/pages/EntityChatPage.tsx:174` | Pending navigation cleared the current workspace before success | Existing identity, messages and draft remain until opening succeeds | Failed navigation must not hide unfinished work |
| MEDIUM | `web/desktop/src/pages/LocalChatPage.tsx:123`, `web/desktop/src/pages/EntityChatPage.tsx:292` | Sidebar items were message titles or conversation records | Named agents with stable identity icons and selected state | Navigation matches the person or agent being addressed |
| MEDIUM | `web/desktop/src/App.tsx:159`, `web/desktop/src/styles.css:687` | Runs and technical configuration dominated the surface | Agent roster, persistent header, secondary settings/history, Activity navigation | Clear hierarchy keeps chat focused on the selected agent |
| MEDIUM | `web/desktop/src/components/AgentAvatar.tsx:4`, `web/desktop/src/styles.css:718` | No consistent agent identity marks | Deterministic SVG marks, consistent sizing, optical alignment and neutral outline | Icons remain recognizable at roster and header sizes |
| MEDIUM | `web/desktop/src/styles.css:712`, `web/desktop/src/App.tsx:159` | Selection and responsive sidebar behavior were inconsistent | Explicit pressed state, visible focus, scrolling roster, narrow-screen layout | Mouse, keyboard and small-window use convey the same selection |
| LOW | `web/desktop/src/styles.css:755` | Abrupt generic controls and broad visual density | Restrained 120ms pointer feedback, 0.96 press, reduced-motion override, quieter surfaces | Feedback stays quick without delaying agent switching |

## Verification

- Real PostgreSQL integration verifies canonical adoption and twelve concurrent
  ensure requests; chat/API tests cover authorization and invalid overrides.
- Local browser fixtures verify named-agent creation, independent drafts/folders,
  reload/history continuity, interruption recovery and conservative output parsing.
- Isolated visual review inspected 1440×1000 desktop and 390×844 narrow layouts,
  agent creation, empty state, selected state, focus outline and reduced-motion
  computed styles. Neither layout overflowed horizontally.
- Motion uses named transition properties, no agent-switch entrance animation,
  fine-pointer hover gating and a reduced-motion override. Animation-panel replay
  at 10% speed and physical touch hardware: **Not verified**.

Runtime proof and rollout details are recorded in
[Prime execution](desktop-prime-execution.md). A model provider timeout and a
missing remote provider credential are not evidence that agent navigation failed.

Review verdict will be finalized after the installed Desktop and deployed API
are driven together.
