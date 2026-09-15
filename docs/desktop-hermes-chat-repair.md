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
