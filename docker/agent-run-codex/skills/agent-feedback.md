# Skill: Anvil Hotline (Human Feedback)

Use `anvil-hotline` when one human decision is required before a safe next
action can be selected — especially when evidence is gathered and the agent
still does not know what to do. Record progress before waiting. A
`needsHuman` report is terminal controller state, so emit it only after the
full wait times out or the Hotline fails and then stop without a success
decision.

`anvil-hotline` is owned by the public repository
`github.com/hazyforge/anvil-hotline`. Runner images install that binary
(with a compatibility symlink as `anvil-agent-feedback`).

The current `discord` transport posts one concise question and waits for a
non-bot reply. Credentials and routing must come from Kubernetes Secrets; never
echo them. The controller optionally projects a same-namespace Secret named
`agent-feedback-discord`, or a run can select another Secret with
`spec.harness.execution.envSecretRefs`.

Required Discord environment:

- `ANVIL_AGENT_FEEDBACK_DISCORD_BOT_TOKEN` (or `ANVIL_HOTLINE_DISCORD_BOT_TOKEN`)
- `ANVIL_AGENT_FEEDBACK_DISCORD_CHANNEL_ID` (or `ANVIL_HOTLINE_DISCORD_CHANNEL_ID`)

Set `ANVIL_AGENT_FEEDBACK_ALLOWED_USER_IDS` (or
`ANVIL_HOTLINE_ALLOWED_USER_IDS`) to the comma-separated Discord user IDs
authorized to answer. The helper fails closed without an allowlist unless
`ANVIL_HOTLINE_ALLOW_ANY_USER=true` / `ANVIL_AGENT_FEEDBACK_ALLOW_ANY_USER=true`
is explicitly selected for a channel whose membership is itself the
authorization boundary. Other optional controls include
`ANVIL_AGENT_FEEDBACK_TIMEOUT`, `ANVIL_AGENT_FEEDBACK_POLL_INTERVAL`, and
`ANVIL_AGENT_FEEDBACK_ACCEPT_ANY_AFTER`.

Ask a single question that states the blocked decision, material consequence,
and expected answer form. Validate the reply against the run's intent and
immutable safety contract before continuing. A reply supplies information; it
does not broaden Kubernetes or repository authorization.

```sh
anvil-agent-status progress \
  --stage operator-question \
  --summary "Waiting for one scoped operator decision."

reply="$(anvil-hotline ask \
  --question "May I delete the named stale test PVC? Reply yes or no." \
  --context "AgentRun=${ANVIL_AGENT_RUN:-unknown}")"
```

After a reply, validate it against the run's existing authority and emit a
normal decision. If the command times out or fails, emit one `needsHuman`
report with a stable secret-safe question key and stop. Never emit a successful
decision after `needsHuman`.
