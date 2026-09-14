# Desktop remote chat verification — 2026-09-14

The user authorized direct master integration and Helm deployment, bypassing
Argo synchronization and GitHub Actions. PR #197 is merged. Existing unrelated
Desktop edits in the original working directory were preserved; integration
used branch `codex/desktop-harness-delivery`.

## Deployed release

- Helm release: `anvil-agents-system-chart`, namespace `anvil-agents-system`,
  revision 12, status `deployed`.
- Controller and API: ready at
  `ghcr.io/hazyforge/anvil-agents@sha256:3c9cbfc0af0aa397ce829977f288dcc4f29a68692b58aac411f5c6c874fb69b5`.
- AGY runner:
  `ghcr.io/hazyforge/anvil-agents/anvil-agent-run-agy@sha256:ba4bcf954552a0f898035af898243b59a7ed9a7d5693cebd578761b91448c86f`.
- Public API reports `chat.enabled=true`, the existing Native Desktop OIDC
  client, and external triggers still enabled.
- The release Argo Application has automated sync unset. No Argo sync or
  GitHub Actions build/deploy was invoked.
- Running local Desktop was replaced with the new binary and UI while retaining
  its existing preferences and signed-in browser session. The local
  `~/bin/anvil-desktop` launcher reopens this build with the same configuration.

## Native remote AGY proof

Harness-only thread `37fda342-b26d-40bf-b3c1-7ed117143e54` selected `desktop-agy`
in namespace `anvilhub`, without creating a profile.

| Turn | AgentRun | Persisted native reply |
| --- | --- | --- |
| Initial connectivity check | `chat-turn-88a49359018612d5195c813d99e76fafa2c2f84e` | `AGY_REMOTE_READY` |
| Conversation memory | `chat-turn-4891f7a4bb33e635315b4919d2ef713e3ad516e6` | `copper` |

Both runs succeeded. The AGY home became Bound/Ready on its first consumer,
and the second run also has a successful PostgreSQL archive receipt. Repeating
the initial request ID returned the original turn
`f1819a01-c85c-4c72-ac18-25d8388a86aa`, without a duplicate run or message. The saved AGY conversation was also
opened from the actual Desktop sidebar; both persisted replies were visible.

## Actual Desktop OpenCode proof

The existing signed-in browser was exercised through Chrome DevTools MCP.
Thread `0c3729af-41c8-4b8b-bc5f-37964f8197ee` was created and sent from the UI
using `desktop-assistant` and its configured OpenCode harness.

- Run `chat-turn-a77467d1e9213088f647671bddc0aa1ae7f21b94` succeeded and
  returned `READY ORCHID-4817`.
- Reloading the browser and reopening the sidebar conversation retained that
  reply. A second message sent through the UI produced `ORCHID-4817` in
  `chat-turn-2b480e54ce76db54a39b353542f3627a570cf7db`.
- These replies were checked against the persisted messages and native runner
  output. MCP textarea `fill` did not update React state in the live browser;
  native keyboard input did. No application workaround was introduced.

## Manager and independent peer proof

The Desktop created manager thread `337a5c3e-29eb-4327-9d93-e6a569b38c08`
with `desktop-reviewer` explicitly allowed. Parent run
`chat-turn-9cc8ce50e171519d39773840cce455cbb6abe4e3` succeeded, and the API
persisted a delivery receipt to thread `ae16d2f3-cfbd-5b0a-9e48-51fc015387c1`.

The peer independently executed
`chat-turn-2e3d4291036812b213ac34579984a0bc16eca148` and replied
`Yes, 2+2=4.` Its incoming message is attributed to `desktop-assistant`,
with the source thread and turn IDs; the answer is attributed to
`desktop-reviewer`. The peer run succeeded and was archived. The Desktop's
Open peer conversation control navigated to this real recipient conversation.

## Deployment findings

The production database needed its CA mounted separately into the API and one
exact TLS HBA entry for the existing role/database from the selected worker.
Both API and controller are now pinned to that admitted worker. The dedicated
chat schema was provisioned under the existing role; database-wide CREATE stays
denied. The migration recognizes a pre-provisioned schema. No credentials were
rotated, no broad database permissions added, and no database restart performed.

Helm 4 needed explicit field-conflict adoption for existing Argo/kubectl fields,
a schema-first pass before AGY resources, and `--wait=legacy` to avoid waiting
for a first-consumer volume before a chat run could use it. Directly managed
profiles use `ownership=helm`, while remaining defined in the Git overlay.

## Checks and limits

`make verify`, real PostgreSQL integration tests, runner contracts, Desktop and
Console checks, isolated browser regressions, and a Grok review were completed.
The production permission issue has an additional regression using a schema
owner without database CREATE permission.

Conversations replay history into an append-only run per turn. Agent and Manager
can select a harness; coordination is opt-in and bounded to allowed peers in the
same namespace/application. Shared writable homes serialize execution and
stable request/delivery IDs prevent duplicate dispatch. This does not implement
a continuously running native session, general semantic duplicate detection,
or a connected UPP adapter daemon. See [standing-chat.md](standing-chat.md).
