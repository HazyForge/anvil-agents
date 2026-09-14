import { apiFetch, APIError } from "./client";
import { parseSSEChunk } from "./stream";
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
  signal?: AbortSignal,
): Promise<ChatAppendResponse> {
  const response = await apiFetch(`${threadsPath(namespace, threadID)}/messages`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
    signal,
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  return (await response.json()) as ChatAppendResponse;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function streamPayloadLooksLikeTool(payload: unknown): boolean {
  if (!isRecord(payload)) {
    return false;
  }
  const type = String(payload.type || payload.event || "").toLowerCase();
  if (type.includes("tool") || type === "function_call") {
    return true;
  }
  return Boolean(payload.tool || payload.toolCall || payload.toolCalls || payload.functionCall);
}

function deltaTextFromPayload(payload: unknown): string {
  if (typeof payload === "string") {
    return payload;
  }
  if (!isRecord(payload)) {
    return "";
  }
  for (const key of ["delta", "text", "content", "message"] as const) {
    const value = payload[key];
    if (typeof value === "string" && value) {
      return value;
    }
  }
  const assistant = payload.assistant;
  if (isRecord(assistant) && typeof assistant.content === "string") {
    return assistant.content;
  }
  return "";
}

/**
 * Append a user message and stream the standing-harness assistant text.
 * Prefers SSE; falls back to a single JSON assistant payload.
 * Never POSTs AgentRuns. Tool events are ignored (not executed).
 */
export async function appendChatMessageStream(
  token: string,
  namespace: string,
  threadID: string,
  request: AppendChatMessageRequest,
  onDelta: (text: string) => void,
  signal?: AbortSignal,
): Promise<ChatAppendResponse> {
  const response = await apiFetch(`${threadsPath(namespace, threadID)}/messages`, token, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream, application/json",
    },
    body: JSON.stringify(request),
    signal,
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  const contentType = (response.headers.get("content-type") || "").toLowerCase();
  if (contentType.includes("text/event-stream") && response.body) {
    const reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";
    let assembled = "";
    let sawTool = false;
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) {
          break;
        }
        buffer += decoder.decode(value, { stream: true });
        const parts = buffer.split(/\r?\n\r?\n/);
        buffer = parts.pop() ?? "";
        for (const part of parts) {
          const parsed = parseSSEChunk(part);
          if (!parsed) {
            continue;
          }
          let payload: unknown = parsed.data;
          if (parsed.data) {
            try {
              payload = JSON.parse(parsed.data);
            } catch {
              payload = parsed.data;
            }
          }
          if (streamPayloadLooksLikeTool(payload) || /tool/i.test(parsed.event)) {
            sawTool = true;
            continue;
          }
          const chunk = deltaTextFromPayload(payload);
          if (!chunk) {
            continue;
          }
          assembled += chunk;
          onDelta(chunk);
        }
      }
    } finally {
      try {
        await reader.cancel();
      } catch {
        /* closed */
      }
    }
    if (!assembled.trim()) {
      if (sawTool) {
        throw new APIError(
          502,
          "harness_tool_without_text",
          "manager harness used a tool instead of a text reply; Desktop did not start a run",
        );
      }
      throw new APIError(502, "empty_assistant", "manager harness returned an empty assistant reply");
    }
    return {
      thread: {
        id: threadID,
        namespace,
        mode: "persona",
        title: "",
        createdAt: "",
        updatedAt: "",
        createdBy: "",
      },
      user: {
        id: "",
        threadId: threadID,
        role: "user",
        content: request.content,
        createdAt: "",
        sequence: 0,
      },
      assistant: {
        id: "",
        threadId: threadID,
        role: "assistant",
        content: assembled,
        createdAt: "",
        sequence: 0,
      },
    };
  }
  const posted = (await response.json()) as ChatAppendResponse;
  const reply = posted.assistant?.content?.trim() || "";
  if (reply) {
    onDelta(reply);
  }
  return posted;
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
