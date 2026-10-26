import { apiFetch, APIError } from './client';
import { parseSSEChunk } from './stream';
import type { AcceptedTurn } from './remoteChat';

export type Subject = {
  id: string;
  kind: string;
  label: string;
  aliases?: string[];
  summary: string;
  threadId: string;
  profileName: string;
  status: 'open' | 'parked' | 'done' | 'merged';
  openedAt?: string;
  touchedAt: string;
};
export type SubjectCandidate = { id: string; label: string; summary?: string; probability?: number };
export type SubjectDecision = {
  subjectChoice: string;
  subjectConfidence: number;
  subjectProbabilities?: Record<string, number>;
  intent: string;
  intentConfidence: number;
  intentProbabilities?: Record<string, number>;
  model?: string;
  outcome?: string;
  reason?: string;
};
export type RoutingEvent = {
  step: string;
  status: 'running' | 'completed' | 'waiting' | 'failed';
  at: string;
  label: string;
  detail?: string;
  candidates?: SubjectCandidate[];
  decision?: SubjectDecision;
  subjectId?: string;
  threadId?: string;
};
export type ManagerResult = Partial<AcceptedTurn> & {
  requestId: string;
  status: 'accepted' | 'clarify' | 'failed';
  subject?: Subject;
  events: RoutingEvent[];
  clarification?: string;
  error?: string;
};
export type RoutingRequest = {
  id: string;
  content: string;
  profileName: string;
  status: 'routing' | 'clarify' | 'accepted' | 'failed';
  subjectId?: string;
  threadId?: string;
  turnId?: string;
  events: RoutingEvent[];
  result?: ManagerResult;
  createdAt: string;
  updatedAt?: string;
  /** A definite HTTP rejection before the event stream opened; local display only. */
  admissionRejected?: boolean;
};
export type ManagerMessage = { requestId: string; content: string; profileName: string; subjectId?: string };
const base = (namespace: string) => `/api/v1/namespaces/${encodeURIComponent(namespace)}/chat`;

export async function listSubjects(token: string, namespace: string, signal?: AbortSignal) {
  const response = await apiFetch(`${base(namespace)}/subjects`, token, { signal });
  if (!response.ok) throw await APIError.fromResponse(response);
  const body = await response.json() as { items?: Subject[]; enabled?: boolean };
  return { items: body.items ?? [], enabled: body.enabled !== false };
}

export async function listRoutingRequests(token: string, namespace: string, signal?: AbortSignal): Promise<RoutingRequest[]> {
  const response = await apiFetch(`${base(namespace)}/routing`, token, { signal });
  if (!response.ok) throw await APIError.fromResponse(response);
  const body = await response.json() as { items?: RoutingRequest[] };
  return (body.items ?? []).map(request => ({ ...request, events: request.events ?? [] }));
}

export async function updateSubject(token: string, namespace: string, id: string, patch: { label?: string; summary?: string; kind?: string; aliases?: string[]; status?: Subject['status'] }): Promise<Subject> {
  const response = await apiFetch(`${base(namespace)}/subjects/${encodeURIComponent(id)}`, token, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(patch),
  });
  if (!response.ok) throw await APIError.fromResponse(response);
  return response.json() as Promise<Subject>;
}

export async function mergeSubjects(token: string, namespace: string, sourceId: string, targetId: string): Promise<Subject> {
  const response = await apiFetch(`${base(namespace)}/subjects/${encodeURIComponent(sourceId)}/merge`, token, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ targetId }),
  });
  if (!response.ok) throw await APIError.fromResponse(response);
  return response.json() as Promise<Subject>;
}

/** The POST streams observed routing steps before handing the accepted turn to chat. */
export async function sendManagerMessage(
  token: string, namespace: string, message: ManagerMessage,
  onEvent: (event: RoutingEvent) => void, signal?: AbortSignal,
): Promise<ManagerResult> {
  const response = await apiFetch(`${base(namespace)}/manager/messages`, token, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify(message), signal,
  });
  if (!response.ok) throw await APIError.fromResponse(response);
  if (!response.headers.get('content-type')?.includes('text/event-stream')) {
    const result = await response.json() as ManagerResult;
    for (const event of result.events ?? []) onEvent(event);
    return result;
  }
  if (!response.body) throw new Error('The routing connection opened without a response stream.');
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let result: ManagerResult | undefined;
  const consume = (chunk: string) => {
    const frame = parseSSEChunk(chunk);
    if (!frame?.data) return;
    if (frame.event === 'routing') onEvent(JSON.parse(frame.data) as RoutingEvent);
    if (frame.event === 'result') result = JSON.parse(frame.data) as ManagerResult;
    if (frame.event === 'error') {
      const error = JSON.parse(frame.data) as { message?: string; error?: string };
      throw new Error(error.message || error.error || 'Routing could not finish.');
    }
  };
  try {
    while (true) {
      const chunk = await reader.read();
      buffer += decoder.decode(chunk.value, { stream: !chunk.done });
      const frames = buffer.split(/\r?\n\r?\n/);
      buffer = frames.pop() ?? '';
      for (const frame of frames) consume(frame);
      if (chunk.done) break;
    }
    if (buffer.trim()) consume(buffer);
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  if (!result) throw new Error('The connection ended before routing was confirmed. Retry this message to recover its saved result.');
  return result;
}
