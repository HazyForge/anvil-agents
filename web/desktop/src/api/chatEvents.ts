import { apiURL } from "./client";
import { parseSSEChunk } from "./stream";

/**
 * Namespace chat change notices (GET .../chat/events, Server-Sent Events).
 * Each `thread` event names a thread that changed — a new message, a turn
 * that started or finished, a new conversation — so the open chat re-reads
 * it right away instead of waiting for its next poll. Events never carry
 * message content; the thread endpoints stay the source of truth.
 *
 * Authenticated with the Authorization header (fetch, not EventSource), so
 * the token never appears in a URL.
 */
export type ChatThreadEvent = {
  type: "thread";
  threadId: string;
  reason: "created" | "message" | "turn" | string;
  turnId?: string;
  at?: string;
};

export type ChatEventsHandlers = {
  onReady: () => void;
  onThread: (event: ChatThreadEvent) => void;
  /** Some notices were dropped; re-read everything on screen. */
  onResync: () => void;
};

/** Why one events connection ended, so the caller can pick its next step. */
export type ChatEventsEnd =
  | { kind: "closed" }
  | { kind: "expired" }
  | { kind: "unsupported" }
  | { kind: "denied"; status: number }
  | { kind: "busy" }
  | { kind: "failed"; message: string };

export function chatEventsPath(namespace: string): string {
  return `/api/v1/namespaces/${encodeURIComponent(namespace)}/chat/events`;
}

/** Read one events connection until it ends and report why it ended. */
export async function readChatEvents(
  token: string,
  namespace: string,
  handlers: ChatEventsHandlers,
  signal: AbortSignal,
): Promise<ChatEventsEnd> {
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  try {
    const response = await fetch(apiURL(chatEventsPath(namespace)), {
      headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
      cache: "no-store",
      signal,
    });
    if (response.status === 401) return { kind: "denied", status: 401 };
    // An older API has no events route; a namespace without chat read access
    // answers 403/404 as well. Either way the chat keeps polling.
    if (response.status === 403 || response.status === 404 || response.status === 405) return { kind: "unsupported" };
    if (response.status === 429) return { kind: "busy" };
    if (!response.ok || !response.body || !(response.headers.get("Content-Type") || "").includes("text/event-stream")) {
      return { kind: "failed", message: `chat events HTTP ${response.status}` };
    }
    reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return { kind: "closed" };
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split(/\r?\n\r?\n/);
      buffer = parts.pop() ?? "";
      for (const part of parts) {
        const parsed = parseSSEChunk(part);
        if (!parsed) continue;
        let payload: Record<string, unknown> = {};
        try {
          payload = parsed.data ? (JSON.parse(parsed.data) as Record<string, unknown>) : {};
        } catch {
          continue;
        }
        if (parsed.event === "ready") handlers.onReady();
        else if (parsed.event === "resync") handlers.onResync();
        else if (parsed.event === "thread" && typeof payload.threadId === "string" && payload.threadId) {
          handlers.onThread(payload as ChatThreadEvent);
        } else if (parsed.event === "error" && payload.code === "token_expired") {
          return { kind: "expired" };
        } else if (parsed.event === "complete") {
          return { kind: "closed" };
        }
      }
    }
  } catch (error) {
    if ((error as Error).name === "AbortError") return { kind: "closed" };
    return { kind: "failed", message: error instanceof Error ? error.message : String(error) };
  } finally {
    if (reader) {
      try {
        await reader.cancel();
      } catch {
        // already closed
      }
    }
  }
}
