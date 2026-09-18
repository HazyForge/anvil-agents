import { apiBase, apiURL } from "./client";
import { parseSSEChunk, type StreamHandlers } from "./stream";
import type { StreamEnvelope } from "./types.stream";

/**
 * Slice-1 standing chat thread stream (see docs/standing-inprocess-harness.md).
 *
 * Prefers WebSocket for first-token feel, falls back to authenticated SSE
 * when the upgrade fails (Desktop Electron constraints, proxies, or older
 * servers). Both transports deliver the same snapshot + terminal events; the
 * stream is read-only and closes after the terminal event in slice 1.
 *
 * Auth: browsers cannot set Authorization on a WebSocket, so the WS path
 * offers the versioned stream protocol plus a `bearer.<token>` subprotocol
 * the server verifies. Tokens are never placed in query strings.
 */
export const CHAT_STREAM_PROTOCOL = "anvil-agents.chat-stream.v1";

export function chatThreadStreamPath(namespace: string, threadID: string, messages = 50): string {
  const params = new URLSearchParams({ messages: String(messages) });
  return (
    `/api/v1/namespaces/${encodeURIComponent(namespace)}` +
    `/chat/threads/${encodeURIComponent(threadID)}/stream?${params}`
  );
}

function streamWebSocketURL(path: string): string | null {
  const base = apiBase();
  // Same-origin relative paths (empty base) cannot form a WS URL without a
  // host; the desktop host proxy serves those over SSE instead.
  if (!base) {
    return null;
  }
  const resolved = base + path;
  if (resolved.startsWith("https://")) {
    return `wss://${resolved.slice("https://".length)}`;
  }
  if (resolved.startsWith("http://")) {
    return `ws://${resolved.slice("http://".length)}`;
  }
  return null;
}

function openChatThreadWebSocket(
  token: string,
  namespace: string,
  threadID: string,
  handlers: StreamHandlers,
  options?: { messages?: number; signal?: AbortSignal },
): { abort: () => void } | null {
  const url = streamWebSocketURL(chatThreadStreamPath(namespace, threadID, options?.messages));
  if (!url) {
    return null;
  }
  let socket: WebSocket | null = null;
  let settled = false;
  try {
    socket = new WebSocket(url, [CHAT_STREAM_PROTOCOL, `bearer.${token}`]);
  } catch (error) {
    handlers.onTransportError?.(error instanceof Error ? error : new Error(String(error)));
    return null;
  }
  const abort = () => {
    settled = true;
    try {
      socket?.close(1000, "client abort");
    } catch {
      // closing
    }
  };
  if (options?.signal) {
    if (options.signal.aborted) {
      abort();
      return { abort };
    }
    options.signal.addEventListener("abort", abort, { once: true });
  }
  socket.onmessage = (message: MessageEvent) => {
    const text = typeof message.data === "string" ? message.data : "";
    if (!text) {
      return;
    }
    let payload: StreamEnvelope = {};
    try {
      payload = JSON.parse(text) as StreamEnvelope;
    } catch {
      payload = { type: "message", message: text };
    }
    handlers.onEvent(payload.type || "message", payload, { event: payload.type || "message", data: text });
    if (payload.type === "terminal") {
      settled = true;
      handlers.onDone?.();
    }
  };
  socket.onerror = () => {
    if (settled) {
      return;
    }
    settled = true;
    handlers.onTransportError?.(new Error("chat stream WebSocket failed; falling back to SSE"));
    const fallback = openChatThreadSSE(token, namespace, threadID, handlers, options);
    void fallback;
  };
  socket.onclose = (event: CloseEvent) => {
    if (settled) {
      return;
    }
    settled = true;
    if (event.code === 1000) {
      handlers.onDone?.();
      return;
    }
    handlers.onTransportError?.(new Error(`chat stream WebSocket closed (${event.code}); falling back to SSE`));
    void openChatThreadSSE(token, namespace, threadID, handlers, options);
  };
  return { abort };
}

/** Authenticated SSE via fetch() — EventSource cannot set Authorization. */
export function openChatThreadSSE(
  token: string,
  namespace: string,
  threadID: string,
  handlers: StreamHandlers,
  options?: { messages?: number; signal?: AbortSignal },
): { abort: () => void } {
  const controller = new AbortController();
  const signal = options?.signal
    ? cancelOnEither(options.signal, controller.signal)
    : controller.signal;

  void (async () => {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    try {
      const response = await fetch(apiURL(chatThreadStreamPath(namespace, threadID, options?.messages)), {
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: "text/event-stream",
        },
        signal,
      });
      if (!response.ok || !response.body) {
        let message = `stream HTTP ${response.status}`;
        try {
          const body = (await response.json()) as { error?: { message?: string; code?: string } };
          if (body.error?.message) {
            message = body.error.message;
          }
        } catch {
          // non-JSON
        }
        handlers.onEvent(
          "error",
          { type: "error", code: `http_${response.status}`, message },
          { event: "error", data: message },
        );
        handlers.onDone?.();
        return;
      }

      reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let buffer = "";
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
          let payload: StreamEnvelope = {};
          if (parsed.data) {
            try {
              payload = JSON.parse(parsed.data) as StreamEnvelope;
            } catch {
              payload = { type: parsed.event, message: parsed.data };
            }
          }
          handlers.onEvent(parsed.event || "message", payload, parsed);
        }
      }
      handlers.onDone?.();
    } catch (error) {
      if ((error as Error).name === "AbortError") {
        handlers.onDone?.();
        return;
      }
      handlers.onTransportError?.(error instanceof Error ? error : new Error(String(error)));
      handlers.onDone?.();
    } finally {
      if (reader) {
        try {
          await reader.cancel();
        } catch {
          // closed
        }
        try {
          reader.releaseLock();
        } catch {
          // released
        }
      }
    }
  })();

  return { abort: () => controller.abort() };
}

/**
 * Open the standing chat thread stream: WebSocket first, SSE fallback.
 * Never places tokens in query strings; apiFetch-style Authorization is used
 * for SSE and the bearer subprotocol for WS.
 */
export function openChatThreadStream(
  token: string,
  namespace: string,
  threadID: string,
  handlers: StreamHandlers,
  options?: { messages?: number; signal?: AbortSignal },
): { abort: () => void } {
  if (typeof WebSocket !== "undefined") {
    const socket = openChatThreadWebSocket(token, namespace, threadID, handlers, options);
    if (socket) {
      return socket;
    }
  }
  return openChatThreadSSE(token, namespace, threadID, handlers, options);
}

function cancelOnEither(first: AbortSignal, second: AbortSignal): AbortSignal {
  if (typeof AbortSignal.any === "function") {
    return AbortSignal.any([first, second]);
  }
  const controller = new AbortController();
  for (const signal of [first, second]) {
    if (signal.aborted) {
      controller.abort(signal.reason);
      return controller.signal;
    }
    signal.addEventListener("abort", () => controller.abort(signal.reason), { once: true });
  }
  return controller.signal;
}
