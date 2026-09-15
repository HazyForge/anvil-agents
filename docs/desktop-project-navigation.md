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
build, live authorized Hazy Trade roster, Anvil-to-Hazy-Trade round trips and
preserved existing Anvil draft. The local installed UI was updated directly;
the existing API already exposes both projects so no cluster rollout was needed.

Approve. Physical touch-device interaction and a simultaneous second remote
connection were not verified; no new animated motion was introduced.
