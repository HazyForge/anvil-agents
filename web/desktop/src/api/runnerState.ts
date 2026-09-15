export type RunnerState = {code: string; message?: string};

/** Only predefined public labels; Kubernetes messages must never become UI copy. */
export function runnerStateLabel(state?: RunnerState): string | undefined {
  switch (state?.code) {
    case 'configuration_unavailable': return 'Runner configuration is unavailable. Waiting for it to be corrected.';
    case 'image_unavailable': return 'The runner image is unavailable. Waiting for it to become available.';
    case 'capacity_wait': return 'Waiting for a worker that can schedule this runner.';
    default: return undefined;
  }
}

/** Configuration rejection is recorded before a runner Job is launched. */
export function runnerFailureLabel(run?: {job?: unknown; runnerPod?: unknown; conditions?: {type?: string; status?: string; reason?: string}[]}): string {
  if (!run?.job && !run?.runnerPod && run?.conditions?.some(c => c.type === 'Ready' && c.status === 'False' && c.reason === 'ConflictingToolName')) {
    return 'Harness could not start: conflicting tool configuration';
  }
  return 'Agent could not finish this turn';
}

/** Recognize only API-canned diagnostics; never display native error text. */
export function chatFailureLabel(error?: string): string | undefined {
  if (error === 'Hermes provider authentication or configuration is unavailable. Check the selected remote harness provider setup, then start a new turn.') return 'Hermes provider setup required';
  if (error === 'Hermes cannot authenticate with xAI. Renew authentication for the selected remote Hermes harness with `hermes model`, then start a new turn.') return 'Hermes authentication required for xAI';
  if (error === "The harness could not start because its selected skill sets or tool sets define conflicting tools. Correct the agent's tool configuration, then start a new turn.") return 'Harness could not start: conflicting tool configuration';
  return undefined;
}
