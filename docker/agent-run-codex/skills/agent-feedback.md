# Skill: Human Feedback

Use `anvil-agent-feedback` when one operator decision is required before a safe
next action can be selected. Record a needs-human status before waiting.

The current `discord` transport posts one concise question and waits for a
non-bot reply. Credentials and routing must come from Kubernetes Secrets; never
echo them. The controller optionally projects a same-namespace Secret named
`agent-feedback-discord`, or a run can select another Secret with
`spec.harness.execution.envSecretRefs`.

Required Discord environment:

- `ANVIL_AGENT_FEEDBACK_DISCORD_BOT_TOKEN`
- `ANVIL_AGENT_FEEDBACK_DISCORD_CHANNEL_ID`

Optional controls include `ANVIL_AGENT_FEEDBACK_ALLOWED_USER_IDS`,
`ANVIL_AGENT_FEEDBACK_TIMEOUT`, `ANVIL_AGENT_FEEDBACK_POLL_INTERVAL`, and
`ANVIL_AGENT_FEEDBACK_ACCEPT_ANY_AFTER`.

Ask a single question that states the blocked decision, material consequence,
and expected answer form. Validate the reply against the run's intent and
immutable safety contract before continuing. A reply supplies information; it
does not broaden Kubernetes or repository authorization.

```sh
anvil-agent-status needsHuman \
  --summary "Operator decision required" \
  --human-follow-up "Choose whether the scoped cleanup may proceed."

reply="$(anvil-agent-feedback ask \
  --question "May I delete the named stale test PVC? Reply yes or no." \
  --context "AgentRun=${ANVIL_AGENT_RUN:-unknown}")"
```
