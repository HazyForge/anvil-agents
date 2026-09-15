import { apiFetch, APIError, type CompositionDocument } from './client';
import type { ChatMessage, ChatThread } from './types.chat';

export type RemoteTurn = {
  createdAt?: string;
  userMessageId?: string;
  id: string;
  runName: string;
  requestId?: string;
  status: 'waiting' | 'queued' | 'running' | 'succeeded' | 'failed';
  error?: string;
  retryCount?: number;
  retryAt?: string;
  recoveryReason?: string;
  attempts?: {runName: string; runUid?: string; error: string; completedAt: string}[];
  delegates?: {profileName: string; threadId: string; turnId: string; runName: string; status: string}[];
};
export type RemoteThread = ChatThread & { harnessProfileName?: string };
export type RemoteThreadDetail = RemoteThread & {
  messages: ChatMessage[];
  recoveryPending?: boolean;
  turns?: RemoteTurn[];
  activeTurn?: RemoteTurn;
};
export type AcceptedTurn = { thread: RemoteThread; user: ChatMessage; turn: RemoteTurn };
const base = (ns: string) => `/api/v1/namespaces/${encodeURIComponent(ns)}/chat/threads`;
async function json<T>(path: string, token: string, init?: RequestInit): Promise<T> {
  const response = await apiFetch(path, token, init);
  if (!response.ok) throw await APIError.fromResponse(response);
  return response.json() as Promise<T>;
}
export async function listRemoteThreads(token: string, ns: string, signal?: AbortSignal, profileName?: string) {
  const filter = profileName ? `&profileName=${encodeURIComponent(profileName)}` : '';
  return (await json<{items: RemoteThread[]}>(`${base(ns)}?limit=200${filter}`, token, {signal})).items ?? [];
}
export async function getRemoteThread(token: string, ns: string, id: string, signal?: AbortSignal) {
  const detail = await json<RemoteThreadDetail>(`${base(ns)}/${encodeURIComponent(id)}`, token, {signal});
  // Empty persisted Go slices may be encoded as null. Consumers need an empty
  // transcript for a new standing conversation, not a render-time exception.
  return {...detail, messages: detail.messages ?? [], turns: detail.turns ?? []};
}
export function createRemoteThread(token: string, ns: string, body: {
  profileName?: string; harnessProfileName?: string; metadata?: {coordination: {enabled: boolean; allowedProfiles: string[]}}; mode: 'persona' | 'fleet'; title: string;
}) {
  return json<RemoteThread>(base(ns), token, {
    method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body),
  });
}
export function ensureRemoteStandingThread(token: string, ns: string, profileName: string, signal?: AbortSignal) {
  return json<RemoteThread>(base(ns), token, {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({standing: true, profileName}), signal,
  });
}
export function sendRemoteMessage(token: string, ns: string, id: string, content: string, requestId: string) {
  return json<AcceptedTurn>(`${base(ns)}/${encodeURIComponent(id)}/messages`, token, {
    method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({content, requestId}),
  });
}
export async function listRemoteHarnesses(token: string, ns: string, signal?: AbortSignal) {
  return (await json<{items: CompositionDocument[]}>(
    `/api/v1/namespaces/${encodeURIComponent(ns)}/agent-harness-profiles?limit=200`, token, {signal},
  )).items ?? [];
}
export function threadHarness(thread: RemoteThread): string {
  if (thread.harnessProfileName) return thread.harnessProfileName;
  const meta = thread.metadata as {harnessProfileName?: string} | undefined;
  return meta?.harnessProfileName ?? '';
}
