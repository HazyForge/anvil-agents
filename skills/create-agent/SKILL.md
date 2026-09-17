---
name: create-agent
description: Wrapper/manager-only skill to create a named teammate (AgentRunProfile + standing chat). Peers request create-agent via requestPeer; they must not POST profiles.
---

# create-agent

Grok Bot-style CreateAgent: **name + description → new teammate**. Anvil Agents Desktop exposes this as a baked-in skill/tool on the **Wrapper** (`anvil-desktop-wrapper`) and **project manager** harnesses only.

Do not give every persona this tool.

## Who may call it

Allowed principals:

- Wrapper profile `anvil-desktop-wrapper`
- Manager profiles `desktop-manager`, `manager-hazy-trade`, `*-agent-manager`
- Label `control.anvil.hazyforge.io/role=wrapper|manager` or `control.anvil.hazyforge.io/create-agent=allow`

`control.anvil.hazyforge.io/chat-role=project-manager` is presentation only. It does not grant create.

The controller injects this skill into Wrapper/manager AgentRuns and **strips** a skill named `create-agent` from everyone else.

## Inputs

| Field | Required | Notes |
| --- | --- | --- |
| `name` | yes | DNS-1123 AgentRunProfile name |
| `description` / `title` | no | What the teammate does |
| `systemPrompt` | no | Standing prompt if the create-profile API supports it |
| `harnessProfileName` | no | Same-namespace harness override |

Desktop fulfills `POST /api/v1/namespaces/{ns}/agent-run-profiles` when `composition.writeEnabled`. GitOps-owned objects stay read-only. Standing chat gets a persona thread when chat is enabled. Return the profile name so peers can address it.

## Peer path

Peers **request** create-agent. They must not create AgentRunProfiles.

Extend existing requestPeer STATUS_JSON (do not invent a new mesh):

```
ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","request":"create-agent","name":"scout","description":"Reviews pull requests","peerProfileName":"desktop-manager"}
```

Desktop shows **Requested create: scout**. Manager/Wrapper fulfills with create-agent and returns the profile name. A peer that calls create-agent is refused with a visible receipt, not a silent no-op.

## Desktop surfaces

- **Chat**: manager skill + create form
- **Runs / Wrapper**: Wrapper skill + create form
- requestPeer live stream: requested-create chip; does not POST profiles for this request
