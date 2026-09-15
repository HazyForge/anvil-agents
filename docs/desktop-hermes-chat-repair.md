# Delivery Steward chat repair

The open Desktop conversation for `anvil-primaris-delivery-steward` failed on
`hi`. The controller created its runner; Hermes then exited before inference
because the saved xAI OAuth state lacked access and refresh tokens. This was
separate from the earlier independent-reviewer tool-name conflict.

## Authentication and first runtime check

The existing agent-specific AgentDataVolume and PVC were preserved. A temporary
Helm-managed helper used the same pinned Hermes image, node and non-root UID,
with no Kubernetes service-account token. The native Hermes xAI OAuth device
flow completed in the owner's signed-in browser. Only credential-presence flags
were inspected; token values were never printed, copied to another harness, or
committed. The temporary Helm release was removed after authentication.

A greeting in the same Desktop conversation then succeeded through Hermes /
`grok-4.5` on `z400`:
`chat-turn-6ed5fc953d5317e2d51042a607e3e86c5a38eb81`, from
2026-09-15T03:51:50Z to 03:52:36Z. The browser received the saved reply after
about 66 seconds. Model, reasoning setting, agent persona and execution scope
were unchanged. This confirms authentication recovery; it does not implement
persistent native chat sessions or automatic inbox wakeups.

## Output correctness

That real-browser test also exposed Hermes reasoning text mixed into the saved
assistant message. Hermes quiet mode prints reasoning and other terminal output. The API's
generic plain-output fallback incorrectly accepted those logs as the answer.
The repair requires an explicit versioned final-answer envelope from the owned
Hermes adapter and rejects unstructured Hermes logs as replies. Existing legacy
Hermes messages without verified final-answer provenance remain stored, but are
withheld from chat display and subsequent model history. No heuristic attempts
to split reasoning text from an answer are used. The operator explicitly wants
reasoning available for debugging: native reasoning and runner logs are retained
and can be expanded separately from the assistant answer, including after a turn
finishes. They are not replayed as assistant messages.

## Error presentation

The API recognizes the exact native terminal missing-access-token diagnostic and
returns fixed authentication guidance. The latest historical failed turn can be
enriched at read time only with existing run-read permission and matching run
UID, thread, turn and source metadata. Existing ownership-verified log access is
reused; no new Secret permission or raw-error rendering is introduced.

A prelaunch tool-name conflict now says the harness could not start. Earlier
failed turns stay available in collapsed history after a successful retry.

## Direct Helm rollout and runtime tool setup

The API/controller is deployed at
`sha256:37e4baa44a87ca14070d8dfa22e812d3c93846870d66958fd4c4065956e7ad33`.
The final Hermes runner, including persistent Go caches, is
`sha256:161860b11fccca46779a5d75a59f672b2cc84e83f411e40cd48b411ade33c322`.
The selected Delivery Steward harness is an existing private Primaris resource,
so a one-resource Helm release adopts it while preserving its UID and every
spec field except the image. The private `deploy/delivery-steward/` helper checks
source/live agreement, a persisted exact-Application Argo hold, and the scoped
admin image-only admission exception. Helm server-side apply transfers field
ownership from the paused Argo controller without replacing the profile.

A first new-image canary (`chat-turn-302c7921d36988136e8d00bc0490826e2ab9b51b`)
caught a second pre-inference failure: the selected tool setup compiles its
repository's `anvilctl`, but the standard Hermes runtime lacked Go. The final
runner includes the pinned public Go toolchain from its existing build stage,
with no private source or private CLI bundled into this repository. The image
checks Go as UID 10000; `docker/agent-run-hermes/image_test.sh` exercises offline
compilation as that user. No agent tool, model, scope or credential configuration
was removed to make the greeting pass.

The deployment admission exception also passed Kubernetes' actual type checker
against all nine served resource schemas after making its map comparisons
dynamic; the live policy reports no expression warnings.

## Verified browser result and setup latency

Run `chat-turn-050d266053259e320ab55817c945020302e01786` succeeded on the
Go-capable runner. The actual browser received a clean saved assistant reply
identifying Delivery Steward, Hermes and Grok 4.5, with
`replyFormat: anvil.hermes.final/v1`. The expanded completed-turn debug view
showed the separate original native output and final envelope.

Tool preparation ran from 04:21:28Z to 04:27:23Z on 2026-09-15; native Hermes
completed at 04:27:44Z. Most delay came from cold repository/tool setup, with
Go downloads and compilation stored in the disposable container filesystem.
The entrypoint now defaults Go caches beneath the selected HERMES_HOME and
preserves explicit GOCACHE/GOMODCACHE values, including GOCACHE=off. The actual
entrypoint fixture verifies export to both setup and harness, cache reuse
between turns, and explicit overrides. This reduces repeated compilation when
the selected home is persistent; it does not implement native session reuse.

The final cache image passed tests against its installed entrypoint and native
Hermes helper as the non-root runtime user with networking disabled and a
read-only filesystem. The live compiler environment was inspected using only
GOCACHE/GOMODCACHE keys and confirmed both directories under this agent's
persistent home. Helm revisions are 20 for anvil-agents-system-chart and 4 for
anvil-primaris-delivery-steward. The policy's observed generation matches its
current generation with no type-check warnings.

The cache-enabled image's first live browser turn,
`chat-turn-fe3f3cd91605c05a07cdc6276808434977369a0e`, succeeded on `z400`
with the exact final digest. Browser receipt took 454 seconds while populating
the persistent cache. Native Hermes ran 04:38:37Z–04:38:55Z; the rest was
startup and reply delivery. Both clean final reply and separate completed-turn
debug output were visible.

The immediate follow-up used a new runner and the same persistent home:
`chat-turn-d6ec2aec08b7c1169ade9ce39ea4550550147acc`. It also succeeded on
the exact final digest, receiving a clean one-sentence identity/capability
answer in 167 seconds (2m47s), compared with 454 seconds (7m34s) for the empty
cache. Logs contained no Go download events; tool setup took 91 seconds
(04:40:21Z–04:41:52Z), and Hermes took 18 seconds (through 04:42:10Z).
This is evidence of improved repeated startup, not a fast-chat or native
keep-alive result. The remaining repository/setup and scheduling cost is still
too high for the intended interactive experience.

Validation: make verify; all 19 remote-chat browser fixtures; installed-image
nonroot Go compilation, entrypoint cache contract, and five native Hermes
contract checks; live API health/readiness 200; both deployments Ready; exact
Helm image/UID checks; and the two same-conversation live browser turns above.
Native session reuse, durable automatic peer inbox wakeups, and the separate
independent-reviewer conflicting-tool configuration remain outstanding.
