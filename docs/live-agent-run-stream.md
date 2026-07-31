# Live AgentRun API

The optional `anvil-agents-api` process exposes sanitized AgentRun state and a
live Server-Sent Events (SSE) stream without granting clients Kubernetes
credentials. It is a read-only resource server: it validates signed JWT OAuth
access tokens using generic OpenID Connect discovery, but it does not log users
in, issue tokens, refresh tokens, or create and mutate AgentRuns. Opaque access
tokens, JWE tokens, and RFC 7662 introspection are not supported.

This gives operators and users one authenticated observation endpoint even
when heavy runs are spread across many worker nodes. Status and per-run logs
remain reachable without node access, SSH, or `kubectl`; the endpoint does not
merge independent runs into a distributed trace or shared conversation.

The API runs separately from the controller with its own ServiceAccount. Its
ClusterRole has cluster-wide read access to AgentRuns, Pods, Jobs, and Pod logs;
application-layer namespace authorization and controller-owner checks narrow
what each request can expose. It cannot read Secrets or mutate Kubernetes
objects. A compromised API process could still read cluster-wide workload logs,
so treat the workload and its ingress as sensitive. Keep create, approval,
repository mutation, and delivery policy in the existing trusted policy plane.

## Endpoints

| Endpoint | Permission | Purpose |
| --- | --- | --- |
| `GET /api/v1/namespaces/{namespace}/agent-runs` | `anvil-agents:runs:read` | List authorized run summaries |
| `GET /api/v1/namespaces/{namespace}/agent-runs/{name}` | `anvil-agents:runs:read` | Read one sanitized run summary |
| `GET /api/v1/namespaces/{namespace}/agent-runs/{name}/events` | `anvil-agents:runs:read` and `anvil-agents:runs:stream` | Stream snapshot, status, log, and terminal SSE events |
| `GET /healthz` and `GET /readyz` | none | Internal probes |

The API accepts access tokens only through `Authorization: Bearer`. Tokens in
URLs are rejected. Browser clients must use authenticated `fetch()` and parse
the response stream because the browser `EventSource` API cannot set an
Authorization header.

List, detail, snapshot, and status events include sanitized
`resolvedComposition` evidence when available: selected object identities,
opaque inherited application/target names in the sanitized view, the
effective-spec digest, and the mounted-payload digest. They never include
Secret values or complete skill contents.

The stream resolves the Pod from the AgentRun status and verifies its Job and
AgentRun ownership before reading the fixed `agent` container. A client cannot
select an arbitrary Pod, Job, or container through this API.
Kubernetes opens the log subresource by Pod name and offers no UID precondition
on that request. Deployments where namespace users may replace controller-owned
Pods must treat that existing Pod authority as log-injection authority despite
the immediately preceding UID, label, and controller-owner validation.

## OIDC configuration

Create a dedicated provider-side API/resource audience for `anvil-agents` that
is distinct from every interactive OIDC client ID, then arrange for the existing
login client to request a signed JWT access token containing that audience. The
dedicated audience is the token-substitution boundary because the verifier does
not accept opaque tokens or introspect provider state. ZITADEL, Keycloak, Auth0,
Entra ID, Dex, and other discovery-capable issuers can be used when configured
to issue compatible signed JWT access tokens; no provider-specific login flow
lives in this operator.

The chart is deny-by-default. Enabling the API requires an issuer, at least one
audience, and at least one explicit authorization binding:

```yaml
api:
  enabled: true
  config:
    oidc:
      issuer: https://identity.example.com
      audiences:
        - anvil-agents
      allowedSigningAlgorithms: [RS256, ES256]
      additionalJWKSHosts: []
      caFile: ""
      allowInsecureIssuer: false
      discoveryRetryInterval: 5s
      discoveryRefreshInterval: 1h
      discoveryRequestTimeout: 10s
    authorization:
      scopeClaims: [scope, scp]
      roleClaims:
        - roles
      roleObjectClaims: []
      groupClaims: [groups]
      namespaceClaim: anvil_agents_namespaces
      bindings:
        - name: agent-viewers
          roles: [anvil_agents_viewer]
          permissions:
            - anvil-agents:runs:read
            - anvil-agents:runs:stream
          namespaces: [agents]
    cors:
      allowedOrigins:
        - https://hub.example.com
    stream:
      heartbeatInterval: 15s
      statusPollInterval: 2s
      maxDuration: 15m
      defaultTailLines: 200
      maxTailLines: 10000
      maxLogBytes: 4194304
      maxLineBytes: 1048576
      maxConnections: 50
      maxConnectionsPerSubject: 5
```

Bindings may select identities with `roles`, `groups`, `subjects`, `emails`,
or `scopes`; every populated selector on a binding must match. Bindings grant
only their listed `permissions` and `namespaces`.
Email bindings match only when the token also carries `email_verified: true`.
`roleClaims` accept string or array claims and reject objects. Claims named in
`roleObjectClaims` deliberately treat every object key as an assigned role and
ignore its value, matching ZITADEL's project-role map shape. Configure only
provider claims whose keys exclusively represent assigned roles.
`namespacesFromClaim` grants the namespace values carried in the configured
`namespaceClaim` in addition to any static namespace list; leave `namespaces`
empty to rely only on the token claim. A wildcard namespace is honored only
when an explicit binding or authorized namespace claim contains `"*"`; no
implicit administrator bypass exists.

For ZITADEL migration, retain the existing issuer and map the existing
`anvil_agent_read` and `anvil_primaris_admin` roles only in separate, explicit
bindings. Hazy Forge assigns the narrower `anvil_agents_viewer` role to
dashboard observers while retaining a distinct administrative compatibility
binding for operator permissions. Prefer a distinct `anvil-agents` API audience
before removing the compatibility binding after clients have migrated. Claim
names are chart configuration rather than ZITADEL constants.
See `examples/live-api/zitadel-values.yaml` for the explicit ZITADEL object
claim overlay. Keycloak, Auth0, Entra ID, and Dex deployments should map their
tokens to configured top-level string/array role, group, scope, and namespace
claims; nested claim paths are not interpreted.

In ZITADEL, set **Access Token Type** to **JWT** on the OIDC application/client
that issues the access token. Tokens minted before that change remain
unchanged, so sign in again or refresh the login session before testing. A
compatible token has the compact three-segment JWT form and carries the
dedicated API audience; an ID token or a token whose only audience is an
interactive client ID is not an API access token.

Issuers and discovered JWKS endpoints must use HTTPS by default. If discovery
returns a JWKS host different from the issuer host, add that hostname to
`additionalJWKSHosts`. For a private CA, put `ca.crt` in a dedicated ConfigMap,
set `api.oidcCA.configMapName`, and set `caFile` to
`/etc/anvil-agents-api/oidc-ca/ca.crt`. The chart intentionally has no generic
Secret/env/volume injection hooks for the public API workload.
The trust pool is loaded at process startup. After rotating the externally
managed CA ConfigMap, change `api.oidcCA.restartToken` to force a Deployment
rollout; changing only the ConfigMap contents does not reload the in-memory
roots.
`allowInsecureIssuer` exists only for isolated development and must not be
enabled on an exposed installation.

## Exposure through Gateway API

The chart creates only a ClusterIP Service unless `api.httpRoute.enabled` is
set. The optional HTTPRoute expects TLS termination on an existing Gateway
HTTPS listener:

```yaml
api:
  httpRoute:
    enabled: true
    parentRefs:
      - name: public
        namespace: gateway-system
        sectionName: https
    hostnames:
      - agents.example.com
```

Configure the Gateway listener certificate, allowed route namespaces, idle
timeout, and any external rate limiting outside this chart. The listener idle
timeout must exceed `stream.heartbeatInterval`. CORS origins are exact browser
origins; wildcard, pattern, and same-host fallbacks are rejected. List every
browser Origin that must call the API (including the console host when the SPA
is served from this process, for example `https://agents.example.com`). CORS is
not an authorization mechanism.

Tail, duration, byte, and line limits apply to each stream. Connection totals
are enforced by each API replica; use Gateway-level rate and connection limits
when a deployment needs one aggregate limit across multiple replicas.
Gateway and network policy remain important because the Kubernetes ClusterRole
is read-only but cluster-wide.

## Streaming a run

Acquire an access token through the existing OIDC client, then use the helper:

```bash
export ANVIL_AGENTS_API_URL=https://agents.example.com
export ANVIL_AGENTS_ACCESS_TOKEN="$(existing-login-command)"
./hack/stream-agent-run.sh \
  --namespace agents \
  --run agent-run-example \
  --tail-lines 200
```

A protected token file can be used instead of an environment variable:

```bash
./hack/stream-agent-run.sh \
  --endpoint https://agents.example.com \
  --namespace agents \
  --run agent-run-example \
  --token-file /run/user/1000/anvil-agents-token
```

The helper never places the token in the URL or command argument list. Access
tokens expire normally, so reconnect with a refreshed token. The current
`Last-Event-ID` behavior emits a `reset`, a fresh status snapshot, and another
bounded log tail rather than promising exact replay. Run logs remain Kubernetes
Pod logs, so durable replay requires a separate log store such as Loki. The
terminal AgentRun status/archive is not a complete log archive.

The API binary is included in the existing `anvil-agents` controller image;
it does not have a separate image. The complete release has seven images: the
controller/API image plus six runner images. `make docker-build` or
`hack/build-images.sh --component controller` rebuilds both binaries together.

## Anvil Agents Console

The same `anvil-agents-api` process serves the read-only **Anvil Agents Console**
SPA at `/` (API under `/api/v1/...`, probes at `/healthz` and `/readyz`). Phase 1
auth uses OIDC Authorization Code + PKCE. Public client settings come from
`GET /ui-config.json` (`api.config.ui.oidc.clientId` plus the API issuer and
audiences). Access tokens stay in browser `sessionStorage`. The console uses
authenticated `fetch()` for SSE; it never places tokens in query strings and
never lets the client choose arbitrary Pods or containers.

Browser calls send an `Origin` header. Configure
`api.config.cors.allowedOrigins` with the exact console origin (for example
`https://agents.example.com`) so the SPA is not denied by the deny-by-default
CORS allowlist. Host-only matching is not used.

Build the SPA with `make console-build` (or the Docker Node stage).
`make verify` runs `console-typecheck`. Production images embed Vite output under
`internal/runapi/consolefs/dist`. See `web/console/README.md` and
`docs/agent-frontend-plan.md`.

## Operational boundary

The API can be deployed against existing AgentRuns before the controller
handoff because it is read-only and has a separate ServiceAccount. Do not use
that fact to run both old and new reconcilers. Once clients use this endpoint,
the old Hub AgentRun projection can be retired independently while unrelated
Hub build and release streams remain in place.
