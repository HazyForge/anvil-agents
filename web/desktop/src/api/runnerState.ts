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
