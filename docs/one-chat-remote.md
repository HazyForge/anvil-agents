# One chat from Desktop and iPhone

[One chat](one-chat.md) puts one manager in front of every agent session: local
Claude Code and Codex runs on this machine and cloud chat threads on an Anvil
Agents API. This document is how a person reaches that manager from Anvil
Agents Desktop and from the Anvil iPhone app, wherever the manager runs.

```
 iPhone (Anvil app, Manager tab)      Anvil Agents Desktop
   │ ZITADEL native PKCE                │ loopback, through the Desktop host
   ▼                                    ▼
 agents.anvil.hazyforge.io: relay (anvil-agents API, opt-in)        phase 2
   ▲  validates ZITADEL tokens; connects a person only to hosts they own
   │  outbound WebSocket, signed in as the owner
 one chat host: `onechat serve` (WSL user service, owns one chat's state)
   ├─ local sessions: claude / codex in the owner's folders
   └─ cloud sessions: Anvil Agents API chat threads
```

## Decisions (2026-10-26)

- **The host dials out to a relay in the cluster.** Nothing listens on the
  owner's network and the phone needs no VPN. This is the shape of Claude Code
  Remote Control and of the `anvild` direction (hosts dial out to a public
  control service; NetBird is an optional direct path, never required).
- **The iPhone client is a Manager tab in the Anvil SwiftUI app**
  (`HazyForge/anvil-primaris-mobile`). It already signs in with a ZITADEL native
  PKCE client for the project audience this API accepts, so no new client, App
  Store Connect app or release pipeline is needed.
- **Device pairing and end-to-end encryption are in the first remote version.**
  A phone message can make Claude Code edit files on the owner's machine, and the
  token audience is shared with Console, Desktop and the Anvil app. A leaked
  token or a compromised API pod must not be enough to drive the host.

## Phases

| Phase | What ships | Where it runs |
| --- | --- | --- |
| 1 | `onechat serve`, protocol v1 on loopback, Desktop Manager view, Desktop local request guard | This machine only |
| 2 | Relay routes in the API, host connector, pairing and end-to-end encryption | Simulator first, production with the owner's approval |
| 3 | Manager tab in the Anvil iPhone app | Built and tested on the owner's Mac, shipped through `IOSBuild` |

Push notifications for finished turns come after phase 3.

## Host: `onechat serve`

```sh
onechat --config ~/.config/onechat/onechat.yaml serve --listen 127.0.0.1:1740
```

- Runs where the local sessions are (WSL by default) and is the only process
  that owns one chat's state, so Desktop and the phone never race each other.
  The CLI's `say` and `sessions` take the host's state lock, so they refuse to
  run while a host serves the same state directory.
- Binds loopback only and refuses any other address.
- Writes `<stateDir>/host.json` (listen address, pid, host id, version) and
  `<stateDir>/host-token` (random bearer secret), both mode 0600. Local clients
  present `Authorization: Bearer <host-token>`.
- Rejects any request that carries an `Origin` header, and any `Host` that is
  not a loopback name for its own port, so no browser page can reach it
  directly. Desktop reaches it through its own host process.
- `GET /healthz` answers `{"ok": true, "hostId": "…"}`. Desktop sends the token
  only to a listener that answers with the host id from `host.json`, and treats
  a `host.json` marked stopped as stopped, so a stale port never receives it.
- Runs one turn at a time. Every connected client sees every turn's events.
- Keeps running when Desktop is closed. As a systemd user unit:

  ```ini
  [Service]
  ExecStart=%h/.local/bin/onechat --config %h/.config/onechat/onechat.yaml serve
  Restart=on-failure
  ```

  WSL itself must be running for the phone to reach the host, the same
  "machine must be on" limit Claude Code Remote Control has.

## Protocol v1

WebSocket, subprotocol `anvil.onechat.v1`, one JSON object per text frame,
at most 1 MiB per frame. The same frames travel over loopback in phase 1 and
inside the encrypted relay channel in phase 2, so Desktop and the phone share
one client model.

Requests carry an `id` that the response repeats:

```json
{"type": "request", "id": "6f0c…", "op": "turn.start", "body": {"text": "…", "requestId": "a1b2…"}}
{"type": "response", "id": "6f0c…", "ok": true, "body": {"turnId": "…"}}
{"type": "response", "id": "6f0c…", "ok": false, "error": {"code": "busy", "message": "…"}}
```

| Op | Body | Result |
| --- | --- | --- |
| `hello` | `client: {name, kind}` | host identity, protocol version, current status including the running turn's events so far |
| `status` | | `state` (`idle`/`busy`), running turn |
| `sessions.list` | `refresh` | sessions without transcripts; while a turn runs, the last snapshot with `stale: true` |
| `session.get` | `sessionId` | one session with its transcript |
| `turn.start` | `text`, `requestId` | `turnId`; `busy` while another turn runs; a turn started during a session refresh waits for it; a repeated `requestId` returns the original `turnId` |
| `turn.cancel` | `turnId` | cancels the running turn and its harness processes |
| `history.list` | `limit` | one chat messages and recent turn summaries |
| `trace.get` | `turnId` | the turn's trace (`onechat.turn/v1`), trimmed to fit a frame |

Events are broadcast to every client, numbered per turn:

```json
{"type": "event", "event": "action.finished", "turnId": "…", "seq": 5, "at": "2026-10-26T23:10:04Z", "body": {…}}
```

`turn.started` → `turn.inventory` → `turn.decided` → (`action.started` →
`action.finished`)… → `turn.answered` → `turn.finished`, plus
`sessions.changed` (sessions without transcripts) and `status`. A client that
connects mid-turn receives the events so far in its `hello` result. A cancelled
turn finishes with `error: "context canceled"`. A frame that would exceed
1 MiB has its longest strings and lists shortened and `truncated: true` set on
its body; saved traces stay complete. Error codes: `unauthorized`, `invalid`,
`busy`, `not_found`, `too_large`, `internal`.

## Desktop

- A **Manager** view: the one chat conversation, a live rail for the running
  turn (decided → each session's action → answer), the session inventory, and
  later the phone pairing panel. Each past exchange opens into what the
  manager did, loaded from its trace. The running turn can be cancelled, and
  after a dropped connection it is rebuilt from the host's status.
- The Desktop host serves `GET /local/v1/onechat/status` and proxies
  `GET /local/v1/onechat/connect` to the one chat host, adding the host token
  and dropping the browser `Origin`. The page never sees the token. The state
  directory defaults to `~/.config/onechat` (`--onechat-state-dir`).
- **Local request guard.** Every `/local/` route rejects a `Host` that is not a
  loopback name for the Desktop port (DNS rebinding), an `Origin` other than the
  Desktop's own, and `Sec-Fetch-Site: cross-site`. Before this guard, a web page
  could POST to `/local/v1/delegate` and start a harness CLI.

## Try phase 1

```sh
onechat --config ~/.config/onechat/onechat.yaml serve
hack/run-anvil-desktop.sh            # open Manager in the sidebar
```

The Manager view says how to start the host when it is not running and
connects as soon as it is. Its UI contract test drives a scripted host:
`node hack/desktop-manager.browser-test.mjs` (build `web/desktop` first).

## Relay (phase 2)

Opt-in routes on the anvil-agents API, behind `api.config.managerRelay.enabled`:

- `GET /api/v1/manager/hosts/connect`: the host's outbound WebSocket. The token
  must carry the new owner-scoped permission `anvil-agents:manager:host`. The
  relay registers the host under the token's subject.
- `GET /api/v1/manager/hosts`: the caller's online hosts (id, name, key
  fingerprint, last seen).
- `GET /api/v1/manager/hosts/{id}/connect`: a client pipe. Requires
  `anvil-agents:manager:use` and the same subject as the host's owner. The
  relay multiplexes client pipes over the host connection and forwards opaque
  encrypted frames.
- Presence is in memory (the chart runs one API replica). The relay stores no
  content and logs connects and disconnects only. Frame, pipe, connection and
  duration limits follow `stream.*`.
- Production needs a role binding for the two permissions; that change waits
  for the owner's approval.

## Pairing and end-to-end encryption (phase 2)

Keys:

- Host: an ECDSA P-256 key in `<stateDir>/host-key.pem` (0600). Its
  fingerprint is SHA-256 of the SPKI DER.
- Phone: an ECDSA P-256 key in the Secure Enclave, never exported.

Pairing, started from Desktop:

1. The host creates a single-use pairing code (16 random bytes, 5 minutes) and
   Desktop shows a QR code:
   `anvil-onechat://pair?v=1&host=<id>&name=<name>&key=<host public key>&code=<code>&relay=<origin>`.
2. The phone scans it, opens a relay pipe, and runs the handshake below with
   only the host authenticated (the host key is pinned from the QR code).
3. Inside that channel it sends its public key, a name, an HMAC-SHA256 of the
   transcript keyed with the code, and a signature with its new key. The host
   checks all three, burns the code and records the device; Desktop shows it.
   Desktop lists paired phones and can revoke them, which also closes their
   pipes.

Every later connection runs a SIGMA-style handshake:

1. phone → host: ephemeral P-256 public key, 32-byte nonce, device id;
2. host → phone: ephemeral key, nonce, and an ECDSA signature over the
   length-prefixed transcript (protocol label, host id, device id, both keys,
   both nonces). The phone verifies it against the pinned host key;
3. phone → host: its signature over the transcript and the host signature.
   The host accepts only paired devices.

Both sides derive per-direction AES-256-GCM keys with HKDF-SHA256 from the
ephemeral ECDH secret (salt: both nonces; info: label and transcript). Nonces
are a direction tag plus a counter, and counters must increase. Protocol v1
frames travel inside. Go and Swift implement this with standard library and
CryptoKit primitives only and share test vectors; the phase 2 brief fixes the
exact encodings.

## iPhone (phase 3)

- A Manager tab in the Anvil app: online hosts, the conversation, the live turn
  rail, sessions, and answering the manager's questions.
- Signs in with the app's existing `AnvilCore` ZITADEL session and calls the
  relay at `agents.anvil.hazyforge.io` with `Authorization: Bearer`.
- Pairs by scanning Desktop's QR code; keys live in the Secure Enclave.
- Built and tested on the owner's Mac; this repository's WSL machine cannot
  compile iOS.

## Threats and answers

| Threat | Answer |
| --- | --- |
| A web page on the owner's machine drives the host or Desktop | Loopback only, host token never in the page, `Origin`/`Host`/`Sec-Fetch-Site` checks |
| A leaked token for the shared audience | Host obeys paired devices only; relay requires the manager permission and the same owner |
| A compromised relay or API pod reads or injects work | End-to-end encryption with the host key pinned at pairing; device signatures |
| Replay of captured frames | Fresh ephemeral keys and nonces per connection; increasing counters |
| A lost phone | Revoke it in Desktop; ZITADEL sessions can be ended centrally |
| Open ports on the owner's network | None; the host only dials out |
