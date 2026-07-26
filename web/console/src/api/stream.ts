import { eventsURL } from "./client";
import type { ParsedSSEEvent, StreamEnvelope } from "./types";

export type StreamHandlers = {
  onEvent: (event: string, payload: StreamEnvelope, raw: ParsedSSEEvent) => void;
  onTransportError?: (error: Error) => void;
  onDone?: () => void;
};

/**
 * Authenticated SSE via fetch() — native EventSource cannot set Authorization.
 * Tokens are never placed in the URL.
 */
export function openAgentRunStream(
  token: string,
  namespace: string,
  name: string,
  handlers: StreamHandlers,
  options?: { tailLines?: number; lastEventID?: string; signal?: AbortSignal },
): { abort: () => void } {
  const controller = new AbortController();
  const signal = options?.signal
    ? anySignal([options.signal, controller.signal])
    : controller.signal;

  void (async () => {
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    try {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${token}`,
        Accept: "text/event-stream",
      };
      if (options?.lastEventID) {
        headers["Last-Event-ID"] = options.lastEventID;
      }
      const response = await fetch(eventsURL(namespace, name, options?.tailLines ?? 200), {
        headers,
        signal,
      });
      if (!response.ok || !response.body) {
        let message = `stream HTTP ${response.status}`;
        try {
          const body = (await response.json()) as { error?: { message?: string; code?: string } };
          if (body.error?.message) {
            message = body.error.message;
          }
          handlers.onEvent(
            "error",
            {
              type: "error",
              code: body.error?.code ?? `http_${response.status}`,
              message,
            },
            { event: "error", data: message },
          );
        } catch {
          handlers.onEvent(
            "error",
            { type: "error", code: `http_${response.status}`, message },
            { event: "error", data: message },
          );
        }
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
          // already closed or aborted
        }
        try {
          reader.releaseLock();
        } catch {
          // lock already released
        }
      }
    }
  })();

  return {
    abort: () => controller.abort(),
  };
}

export function parseSSEChunk(chunk: string): ParsedSSEEvent | null {
  const lines = chunk.split(/\r?\n/);
  let id: string | undefined;
  let event = "message";
  const dataLines: string[] = [];
  for (const line of lines) {
    if (!line || line.startsWith(":")) {
      continue;
    }
    const colon = line.indexOf(":");
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) {
      value = value.slice(1);
    }
    switch (field) {
      case "id":
        id = value;
        break;
      case "event":
        event = value;
        break;
      case "data":
        dataLines.push(value);
        break;
      default:
        break;
    }
  }
  if (dataLines.length === 0 && !id) {
    return null;
  }
  return { id, event, data: dataLines.join("\n") };
}

function anySignal(signals: AbortSignal[]): AbortSignal {
  if (typeof AbortSignal.any === "function") {
    return AbortSignal.any(signals);
  }
  const controller = new AbortController();
  for (const signal of signals) {
    if (signal.aborted) {
      controller.abort(signal.reason);
      return controller.signal;
    }
    signal.addEventListener("abort", () => controller.abort(signal.reason), { once: true });
  }
  return controller.signal;
}
