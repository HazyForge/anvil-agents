# Standing Chat Storage

Standing chat is an **API feature**, not a Kubernetes custom resource. Threads
are ChatGPT-style conversations bound to an `AgentRunProfile` (persona mode)
or, later, a fleet/master mode. They persist in PostgreSQL. This repository
does not add a Conversation CRD.

The first cut stores threads and messages and exposes OIDC-gated HTTP routes on
`anvil-agents-api`. LLM and tool execution are stubbed. The Anvil Agents Console
includes a standing-chat UI gated by `ui-config` `chat.enabled`. LangGraph tools,
agent-to-agent inbox, and injecting chat into live AgentRuns remain out of scope.

## Storage

Chat reuses the AgentRun archive database installation pattern. Every archive
mode (`external`, `standalone`, `cloudnativepg`) already resolves to one
Kubernetes Secret key containing a PostgreSQL URI. When standing chat is
enabled, the API Pod mounts that same Secret as `ANVIL_AGENTS_CHAT_DATABASE_URL`
unless `api.chatDatabaseURLSecret` overrides it.

The API process never reads or writes Kubernetes Secret *values*. It only
consumes the URI from its environment, the same way the controller consumes
`ANVIL_AGENTS_ARCHIVE_DATABASE_URL`. Chat code has no Secret RBAC.

Tables live in a dedicated schema `anvil_agents_chat` so they do not collide
with `anvilhub_agent_run_archives` in `public`. The database role must be able
to `CREATE SCHEMA` (the chart's standalone superuser and CloudNativePG
application owner can). For a locked-down external role, pre-create the schema
or grant `CREATE` on the database.

| Table | Purpose |
| --- | --- |
| `anvil_agents_chat.threads` | Conversation identity, namespace, profile, mode, title, creator subject, metadata |
| `anvil_agents_chat.messages` | Ordered `system` / `user` / `assistant` / `tool` rows |
| `anvil_agents_chat.checkpoints` | Empty placeholder for a future LangGraph checkpoint store |

Persona threads require `profile_name`. Fleet threads may omit it.

The API migrates the schema on process start. It does not copy archive rows,
does not change archive retention, and does not delete GitOps-owned agents.

## Chart knobs

Chat is deny-by-default, matching `api.enabled` and the other API feature
gates.

```yaml
api:
  enabled: true
  config:
    chat:
      enabled: true
    authorization:
      bindings:
        - name: operators
          roles: [anvil_agents_operator]
          permissions:
            - anvil-agents:runs:read
            - anvil-agents:runs:stream
            - anvil-agents:chat:read
            - anvil-agents:chat:write
          namespaces: [agents]
  # Empty name reuses the archive Secret from archive.mode.
  chatDatabaseURLSecret:
    name: ""
    key: ""
archive:
  mode: external
  external:
    databaseURLSecret:
      name: agent-archive-database
      key: url
```

| Value | Default | Effect |
| --- | --- | --- |
| `api.config.chat.enabled` | `false` | Serve `/api/v1/namespaces/{namespace}/chat/*` |
| `api.chatDatabaseURLSecret.name` | empty | Override Secret; empty shares the archive URI Secret |
| `archive.mode` | disabled | Required unless an override Secret is set |
| `archive.restartToken` | empty | Also annotates the API Pod when chat is enabled so URI rotation rolls both workloads |

Grant `anvil-agents:chat:read` and `anvil-agents:chat:write` only after
`api.config.chat.enabled=true`. The URI must not appear in the API ConfigMap.

## API

OIDC is the same as the rest of `anvil-agents-api`: bearer access tokens,
exact issuer and audience, explicit bindings, namespace authorization, no
query-string tokens.

| Endpoint | Permission |
| --- | --- |
| `GET /api/v1/namespaces/{namespace}/chat/threads` | `anvil-agents:chat:read` |
| `POST /api/v1/namespaces/{namespace}/chat/threads` | `anvil-agents:chat:write` |
| `GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}` | `anvil-agents:chat:read` |
| `GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages` | `anvil-agents:chat:read` |
| `POST /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages` | `anvil-agents:chat:write` |

List accepts `profileName`, `mode`, and `limit`. Create is scoped to the path
namespace. Append always stores a `user` message from the caller; the current
assistant reply is a labeled echo stub (`metadata.stub=true`). Clients cannot
spoof `assistant`, `system`, or `tool` roles through this API.

`GET /ui-config.json` includes `chat.enabled`. When true, the console shows a
**Standing chat** nav entry and routes:

| Route | Purpose |
| --- | --- |
| `/chat` | Thread list + create for the active namespace (persona / `AgentRunProfile`) |
| `/ns/{namespace}/chat` | Same hub scoped to that namespace |
| `/ns/{namespace}/chat/{threadId}` | Thread detail, message history, and composer |

The console uses the same OIDC bearer session as the rest of the SPA. Stub
assistant replies (`metadata.stub=true`) render with a stub chip.

## Non-goals

- No Conversation (or other chat) CRD
- No Kubernetes Secret byte access from chat code
- No LangGraph microservice, gRPC worker, or tool allowlist
- No agent-to-agent inbox
- No soft-inject of chat history into live AgentRuns

Follow-ups: a LangGraph worker that writes `anvil_agents_chat.checkpoints`, and a
tool allowlist bound to the persona's `AgentToolSet`.

## Verification

Unit tests cover the in-process store and OIDC routes. The real PostgreSQL
migration runs next to the archive integration check:

```bash
make archive-postgres-integration
```
