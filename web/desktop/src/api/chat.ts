import { apiFetch, APIError } from "./client";
import type {
  AppendChatMessageRequest,
  ChatAppendResponse,
  ChatMessage,
  ChatMessageListResponse,
  ChatThread,
  ChatThreadDetailResponse,
  ChatThreadListResponse,
  CreateChatThreadRequest,
  ListChatThreadsParams,
} from "./types.chat";

function threadsPath(namespace: string, threadID?: string): string {
  const root = `/api/v1/namespaces/${encodeURIComponent(namespace)}/chat/threads`;
  return threadID ? `${root}/${encodeURIComponent(threadID)}` : root;
}

export async function listChatThreads(
  token: string,
  namespace: string,
  params: ListChatThreadsParams = {},
  signal?: AbortSignal,
): Promise<ChatThread[]> {
  const query = new URLSearchParams();
  if (params.limit != null) {
    query.set("limit", String(params.limit));
  }
  if (params.profileName?.trim()) {
    query.set("profileName", params.profileName.trim());
  }
  if (params.mode?.trim()) {
    query.set("mode", params.mode.trim());
  }
  const suffix = query.toString() ? `?${query}` : "";
  const response = await apiFetch(`${threadsPath(namespace)}${suffix}`, token, { signal });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  const body = (await response.json()) as ChatThreadListResponse;
  return body.items ?? [];
}

export async function createChatThread(
  token: string,
  namespace: string,
  request: CreateChatThreadRequest,
): Promise<ChatThread> {
  const response = await apiFetch(threadsPath(namespace), token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as ChatThread;
}

export async function getChatThread(
  token: string,
  namespace: string,
  threadID: string,
  signal?: AbortSignal,
): Promise<ChatThreadDetailResponse> {
  const response = await apiFetch(threadsPath(namespace, threadID), token, { signal });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as ChatThreadDetailResponse;
}

export async function appendChatMessage(
  token: string,
  namespace: string,
  threadID: string,
  request: AppendChatMessageRequest,
): Promise<ChatAppendResponse> {
  const response = await apiFetch(`${threadsPath(namespace, threadID)}/messages`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as ChatAppendResponse;
}

export async function listChatMessages(
  token: string,
  namespace: string,
  threadID: string,
  signal?: AbortSignal,
): Promise<ChatMessage[]> {
  const response = await apiFetch(`${threadsPath(namespace, threadID)}/messages`, token, { signal });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  const body = (await response.json()) as ChatMessageListResponse;
  return body.items ?? [];
}
