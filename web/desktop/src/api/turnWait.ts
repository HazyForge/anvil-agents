export type TurnWaitFeedback = {tone: 'waiting' | 'delayed' | 'working'; label?: string; note?: string};

/** Elapsed time is evidence of delay, never proof of process failure. */
export function turnWaitFeedback(status: string | undefined, elapsedSeconds: number, quietSeconds: number, workObserved: boolean): TurnWaitFeedback | undefined {
  if (!status || status === 'succeeded' || status === 'failed') return undefined;
  if (status === 'waiting') return elapsedSeconds >= 120
    ? {tone: 'delayed', label: 'Delivery is delayed; your message is still waiting', note: 'Your message is saved. Waiting for the server to make this agent available; no additional copy has been sent.'}
    : {tone: 'waiting'};
  if (!workObserved) return elapsedSeconds >= 120
    ? {tone: 'delayed', label: 'Agent startup is delayed; no harness work has been reported', note: 'Your message is saved. The server has not confirmed that this agent started work. You can keep drafting while waiting for its status to update.'}
    : {tone: 'waiting'};
  if (quietSeconds >= 120) return {tone: 'delayed', label: 'No recent activity reported; this turn may still be running', note: 'No new harness activity has been reported for at least two minutes. Your message remains saved; this does not confirm the work has stopped.'};
  return {tone: 'working'};
}

export const chatStartupDeadlineError = 'This chat turn did not start within 5 minutes. No runner Job was found. Send a new message to try again.';
