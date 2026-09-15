# Desktop location and project navigation

Desktop separates **Location** (Local or the connected Primaris deployment)
from **Project** (Anvil or Hazy Trade within that deployment). Projects scope
the agent roster; agents retain their individual standing conversations.

The initial remote project adapter maps existing configured namespaces to
stable project IDs and friendly names: `anvilhub` becomes Anvil and
`hazy-trade` becomes Hazy Trade. The selector component consumes project
IDs/names rather than Kubernetes resources. Namespace remains visible in
Conversation details for debugging. API paths and authorization are unchanged;
configured namespaces are discovery hints, not access grants.

Keep one project's agents visible at a time. Switching clears the previous
roster immediately and uses the existing per-namespace conversation selection,
new-chat configuration, pending-send receipts, and drafts. A switch does not
cancel or send agent work. Additional simultaneous remote connections and
cross-project aggregate rosters are not implemented by this UI pass.

## UI review

| Severity | Location | Before | After | Why |
| --- | --- | --- | --- | --- |
| High | remoteChat.ts empty history | New standing chats could receive null messages and render a blank page | Normalize empty messages/turns to arrays | Makes first project conversations usable |
| Medium | EntityChatPage.tsx sidebar | Namespace buried in collapsed Workspace | Visible Project selector above agents | Makes Hazy Trade discoverable |
| Low | App.tsx location switch | Workspace ambiguously described Local/Primaris | Location labels execution placement | Keeps project grouping distinct |
| Low | EntityChatPage.tsx project transition | Previous roster retained until effect | Clear roster and conversation immediately | Avoids showing the wrong project's agents |

The native select has a 44px hit target, explicit accessible name, keyboard
semantics and visible focus outline. It adds no navigation animation. Live
Chromium inspection found no folder-icon/label overlap. Independent Grok review
raised conditional draft-persistence and native-padding concerns; the actual
implementation persists drafts on input (not in a namespace/draft effect), and
browser tests plus live round trips verified draft restoration and spacing.

Validation: `make verify`, all 21 remote-chat browser fixtures, production UI
build, nullable empty-history fixtures, live authorized Hazy Trade roster, Anvil-to-Hazy-Trade round trips and
preserved existing Anvil draft. A final reload verified the Hazy Trade manager
empty chat is ready to receive a message. The local installed UI was updated directly;
the existing API already exposes both projects so no cluster rollout was needed.

Approve. Physical touch-device interaction and a simultaneous second remote
connection were not verified; no new animated motion was introduced.

## Project manager conversations

The designated manager appears in a separate Project manager section above
Agents. Its existing avatar and standing thread remain its identity. The chat
header adds a quiet role badge and a sentence about coordinating work,
schedules, and agent instructions. The empty conversation suggests those
requests; Conversation details explains that configured tools and project
permissions govern changes and some changes require review.

A profile can designate its presentation role with the source-owned label
`control.anvil.hazyforge.io/chat-role: project-manager`. Explicit designations
supersede the initial adapter's exact legacy identities:
`anvilhub/anvil-primaris-agent-manager` and
`hazy-trade/hazy-trade-agent-manager`. Any other nonempty chat-role on a legacy
profile opts it out. Names merely containing manager do not qualify; a
specialized book manager stays in Agents. Multiple explicit designations are
shown under Project managers. Projects with no designation show only Agents.

The first visit to a project opens its manager, then falls back to Desktop
assistant or the available roster. Saved conversation selection or unfinished
new-chat configuration always takes precedence. Selecting a manager only
opens its standing conversation. It does not send a message, change harness,
switch conversation mode, enable peer delegation, or grant any permissions.
The designation is presentation metadata, never an authorization input.

| Before | After | Why |
| --- | --- | --- |
| Manager mixed into alphabetic roster | Separate manager group above Agents | Makes the project's coordination entry obvious |
| Same generic header for every agent | Small Project manager badge and role description | Explains who this conversation is with |
| First visit chooses assistant or arbitrary profile | Prefer designated manager while retaining saved selection | Starts project conversations with the coordinator without losing drafts |

Validation: production UI build, `make verify`, all 22 remote-chat browser
scenarios including designation overrides, specialized-manager exclusion,
first-visit selection, saved drafts, and unchanged conversation/delegation
settings. Live Desktop inspection verified the Hazy Trade manager group and
header; the previously selected agent was restored and no message was sent.
This UI update needs no Helm rollout or change to fleet permissions.
