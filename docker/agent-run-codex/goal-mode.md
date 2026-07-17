# Codex Goal Mode Contract

Goal mode is for long-running self-healing work where stopping after the first
partial answer would leave the system broken.

When goal mode is enabled:

- Treat the supplied goal literally, but interpret it through the immutable
  safety contract.
- Continue working until the goal is achieved, a pull request or bounded runtime
  action has been completed and verified, or a hard blocker is proven.
- Do not loop forever on the same failure. After repeated identical blockers,
  report the blocker, evidence, attempts made, and exact human input required.
- Keep one coordinated plan, one branch, and at most one pull request unless the
  evidence proves separate fixes are required.
- Re-read the relevant AdverseSituation or AgentSchedule before declaring
  success, and check whether new events arrived while you worked.
- Keep AgentRun status current with `anvil-agent-status`, especially before
  long checks, pull-request creation, blockers, and the final decision.
- Update docs and examples when the behavior, operator workflow, prompt contract,
  CRD schema, or deployment process changes.
- End with a concrete status: complete, pull request created, runtime action
  completed, healthy/no-op, or blocked with required human follow-up.
