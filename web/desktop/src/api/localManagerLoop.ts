export type LoopConversation = {
  id: string; name: string; harness: string; workdir: string; target: string; wslDistro?: string;
  messages: {role: 'user' | 'assistant'; content: string}[];
};
export type LoopTask = {
  id: string; requestId: string; conversation: LoopConversation; content: string;
  state: 'queued' | 'running' | 'verifying' | 'needs_decision' | 'completed' | 'paused' | 'cancelled';
  origin?: 'manager' | 'observed'; blocking?: boolean;
  submittedWorkdir?: string;
  attempt: number; createdAt: string; updatedAt: string; reply?: string; exitCode?: number; error?: string; verification?: string;
};
export type LoopDecision = {
  id: string; taskId: string; version: number; state: 'pending' | 'resolved'; reason: string; message: string;
  choices: {id: string; label: string}[]; choice?: string;
};
export type LoopEvent = {id: number; taskId: string; stage: 'detect' | 'ask' | 'act' | 'verify'; message: string; at: string};
export type LoopSnapshot = {revision: number; tasks: LoopTask[]; decisions: LoopDecision[]; events: LoopEvent[]; storageError?: string};
export type LoopResolution = {choice?: string; confidence?: number; source: 'exact' | 'classifier' | 'clarify' | 'recorded'; reason: string};

export class LoopRequestError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {super(message);this.status = status;}
}
// A server error can follow durable acceptance (for example an fsync error).
// Conflicts may also refer to an already accepted request identity. Keep the
// original intent until the host can reconcile those outcomes.
export const managerAdmissionRejected = (error: unknown) => error instanceof LoopRequestError && [400,401,403,404,413,422].includes(error.status);
async function request(path: string, body?: unknown): Promise<Response> {
  const response = await fetch(`/local/v1/manager/loop${path}`, body === undefined ? {cache: 'no-store'} : {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
  if (!response.ok) {
    let message = `Manager loop request failed (${response.status}).`;
    try {const error = await response.json(); message = error.message || message;} catch { /* Preserve HTTP status. */ }
    throw new LoopRequestError(message, response.status);
  }
  return response;
}
export async function loadManagerLoop(): Promise<LoopSnapshot> {return (await request('')).json();}
export async function submitManagerWork(body: {requestId: string; conversation: LoopConversation; content: string}): Promise<LoopSnapshot> {return (await request('/work', body)).json();}
export async function observeManagerConversation(body: {requestId: string; conversation: LoopConversation; pending: boolean; lastError?: string}): Promise<LoopSnapshot> {return (await request('/observe', body)).json();}
export async function chooseManagerDecision(decision: LoopDecision, choice: string, requestId: string): Promise<LoopSnapshot> {return (await request(`/decisions/${encodeURIComponent(decision.id)}`, {requestId, version: decision.version, choice})).json();}
export async function answerManagerDecision(decision: Pick<LoopDecision,'id'|'version'>, content: string, requestId: string): Promise<{snapshot?: LoopSnapshot; resolution: LoopResolution}> {return (await request(`/decisions/${encodeURIComponent(decision.id)}/answer`, {requestId, version: decision.version, content})).json();}
export const taskBlocksConversation = (task: LoopTask) => task.blocking ?? !['completed', 'cancelled'].includes(task.state);
