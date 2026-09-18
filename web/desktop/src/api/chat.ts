import { apiFetch, APIError } from "./client";
import {
  assistantAfterUser,
  dedupeChips,
  extractChipsAndCleanText,
  extractChipsFromMessage,
  extractChipsFromPayload,
  isRecord,
  normalizeChatMessages,
} from "./chatParse";
import { parseSSEChunk } from "./stream";
import type {
  AppendChatMessageRequest,
  ChatAppendResponse,
  ChatChip,
  ChatMessage,
  ChatMessageListResponse,
  ChatThread,
  ChatThreadDetailResponse,
  ChatThreadListResponse,
  CreateChatThreadRequest,
  ListChatThreadsParams,
} from "./types.chat";

export {
  assistantAfterUser,
  dedupeChips,
  extractChipsAndCleanText,
  extractChipsFromMessage,
  extractChipsFromPayload,
  extractToolCalls,
  isRecord,
  normalizeChatMessages,
  peerTargetFromArgs,
  turnStatusFromMessage,
  turnStatusFromRaw,
} from "./chatParse";

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
  return Array.isArray(body.items) ? body.items : [];
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
  const body = (await response.json()) as ChatThreadDetailResponse;
  return { ...body, messages: normalizeChatMessages(body.messages) };
}

export async function appendChatMessage(
  token: string,
  namespace: string,
  threadID: string,
  request: AppendChatMessageRequest,
  signal?: AbortSignal,
): Promise<ChatAppendResponse & { chips?: ChatChip[] }> {
  const response = await apiFetch(`${threadsPath(namespace, threadID)}/messages`, token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
    signal,
  });
  if (!response.ok) {
    throw await APIError.fromResponse(response);
  }
  const posted = (await response.json()) as ChatAppendResponse;
  const chips = extractChipsFromMessage(posted.assistant);
  return { ...posted, chips: chips.length > 0 ? chips : undefined };
}

export async function recoverAssistantFromThread(
  token: string,
  namespace: string,
  threadID: string,
  userContent: string,
  signal?: AbortSignal,
): Promise<ChatMessage | undefined> {
  const detail = await getChatThread(token, namespace, threadID, signal);
  return assistantAfterUser(detail.messages, userContent);
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

export type ChatStreamCallbacks = {
  onDelta?: (text: string) => void;
  onChips?: (chips: ChatChip[]) => void;
};

/**
 * Append a user message and stream the standing-harness assistant text and transitions.
 * Prefers SSE; falls back to a single JSON assistant payload.
 * Surfaces delegation routing, target agent, and transition states as chips.
 * Re-GETs the thread after stream/JSON so a recovered assistant reply actually renders.
 * Accepts either `{onDelta, onChips}` or the older `(onDelta, signal, onChips)` form.
 */
export async function appendChatMessageStream(
  token: string,
  namespace: string,
  threadID: string,
  request: AppendChatMessageRequest,
  onDeltaOrCallbacks: ((text: string) => void) | ChatStreamCallbacks,
  signal?: AbortSignal,
  onChipsLegacy?: (chips: ChatChip[]) => void,
): Promise<ChatAppendResponse & { chips?: ChatChip[] }> {
  const onDelta =
    typeof onDeltaOrCallbacks === "function" ? onDeltaOrCallbacks : onDeltaOrCallbacks.onDelta;
  const onChips =
    typeof onDeltaOrCallbacks === "function" ? onChipsLegacy : onDeltaOrCallbacks.onChips ?? onChipsLegacy;

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
    let collectedChips: ChatChip[] = [];
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

          const newChips = extractChipsFromPayload(payload, parsed.event);
          if (newChips.length > 0) {
            collectedChips = dedupeChips([...collectedChips, ...newChips]);
            onChips?.(collectedChips);
          }

          const chunk = deltaTextFromPayload(payload);
          if (chunk) {
            assembled += chunk;
            onDelta?.(chunk);
          }
        }
      }
    } finally {
      try {
        await reader.cancel();
      } catch {
        /* closed */
      }
    }

    const cleaned = extractChipsAndCleanText(assembled);
    if (cleaned.chips.length > 0) {
      collectedChips = dedupeChips([...collectedChips, ...cleaned.chips]);
      onChips?.(collectedChips);
    }
    assembled = cleaned.text;

    const recovered = await recoverAssistantFromThread(token, namespace, threadID, request.content, signal).catch(
      () => undefined,
    );
    if (recovered) {
      const recoveredClean = extractChipsAndCleanText(recovered.content || "");
      const recoveredChips = dedupeChips([
        ...collectedChips,
        ...extractChipsFromMessage(recovered),
        ...recoveredClean.chips,
      ]);
      const recoveredText = recoveredClean.text.trim() || assembled;
      if (!assembled.trim() && recoveredText) {
        onDelta?.(recoveredText);
      } else if (recoveredText.startsWith(assembled) && recoveredText.length > assembled.length) {
        onDelta?.(recoveredText.slice(assembled.length));
      }
      if (recoveredChips.length > 0) {
        onChips?.(recoveredChips);
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
          ...recovered,
          content: recoveredText,
          metadata: isRecord(recovered.metadata)
            ? { ...recovered.metadata, chips: recoveredChips }
            : { chips: recoveredChips },
        },
        chips: recoveredChips.length > 0 ? recoveredChips : undefined,
      };
    }

    if (!assembled.trim() && collectedChips.length === 0) {
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
        metadata: {
          chips: collectedChips,
        },
      },
      chips: collectedChips.length > 0 ? collectedChips : undefined,
    };
  }

  const posted = (await response.json()) as ChatAppendResponse;
  const recovered = await recoverAssistantFromThread(token, namespace, threadID, request.content, signal).catch(
    () => posted.assistant,
  );
  const assistant = recovered || posted.assistant;
  const metaChips = extractChipsFromMessage(assistant);
  const cleaned = extractChipsAndCleanText(assistant?.content || "");
  const combinedChips = dedupeChips([...metaChips, ...cleaned.chips]);

  const reply = cleaned.text.trim();
  if (reply) {
    onDelta?.(reply);
  }
  if (combinedChips.length > 0) {
    onChips?.(combinedChips);
  }

  return {
    ...posted,
    assistant: {
      ...assistant,
      content: reply,
      metadata: isRecord(assistant?.metadata)
        ? { ...assistant.metadata, chips: combinedChips }
        : { chips: combinedChips },
    },
    chips: combinedChips.length > 0 ? combinedChips : undefined,
  };
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
  return normalizeChatMessages(body.items);
}
