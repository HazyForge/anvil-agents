/**
 * Desktop standing live-turn over the slice-3 WebSocket token path.
 *
 * `openChatThreadStream` (see `../api/chatThreadStream.ts`) opens the
 * read-only thread stream WebSocket-first: a standing (InProcess) thread
 * stays open past the snapshot and multiplexes live `token` frames
 * (`{type:"token", threadId, turnId, runName, seq, token, done}`) for the
 * thread's turns before the same `terminal` frame; Job-plane threads keep
 * snapshot + terminal and close. The durable turn record stays the source
 * of truth, so a dropped live frame never loses the reply.
 *
 * This module orchestrates one live turn with injectable transport so unit
 * tests fake the WS stream with no live cluster and no OIDC:
 *
 *   1. Open the stream FIRST (subscribe before the POST so tokens published
 *      while the handshake completes still reach this turn).
 *   2. Read the snapshot: `snapshot.standing` present means the standing
 *      path (`path: "standing"`); absent means the Job plane (`path: "job"`)
 *      and the caller should keep its existing POST + poll path.
 *   3. POST the message once (the send is never retried here — the frozen
 *      turn outbox makes the POST idempotent by requestId, and a second
 *      POST would create a second turn).
 *   4. Forward live `token` frames as deltas (first frame marks first-token
 *      timing at the call site) until the done frame or `terminal`.
 *   5. Settle from the durable thread read (frozen outbox, Succeeded
 *      completion, peer fanout all unchanged); assembled live tokens are
 *      only the progressive display text.
 *
 * Any transport failure (no WS available, OIDC denied, snapshot timeout,
 * send failure, terminal timeout) returns a fallback result with `error`
 * set instead of throwing, so the caller keeps its existing POST/poll path
 * as the only change in behavior. No tokens ever enter query strings; auth
 * stays bearer-header or the verified `bearer.<token>` WS subprotocol owned
 * by `openChatThreadStream`.
 */

export type ChatTurnPath = "standing" | "job" | "unknown";

export type StreamAbortHandle = { abort: () => void };

export type ThreadStreamHandlers = {
  onEvent: (event: string, payload: Record<string, unknown>) => void;
  onTransportError?: (error: Error) => void;
  onDone?: () => void;
};

export type OpenThreadStreamFn = (
  handlers: ThreadStreamHandlers,
  options?: { messages?: number; signal?: AbortSignal },
) => StreamAbortHandle;

export type SendChatMessageFn = (request: {
  content: string;
  requestId: string;
}) => Promise<{ turnId?: string; runName?: string }>;

export type ReadThreadReplyFn = () => Promise<
  { text: string; turnId?: string; runName?: string } | undefined
>;

export type StandingTurnRequest = {
  /** Durable thread id; token frames for other threads are ignored. */
  threadId: string;
  /** Frozen turn intent: user text + idempotency key (server dedupes by requestId). */
  content: string;
  requestId: string;
  /** Injectable transport: faked in tests, `openChatThreadStream` live. */
  openStream: OpenThreadStreamFn;
  sendMessage: SendChatMessageFn;
  /** Durable settle: re-read the thread until the assistant reply lands. */
  readThread: ReadThreadReplyFn;
  onDelta?: (text: string) => void;
  onStatus?: (status: string) => void;
  /** Clock override for tests. Defaults to Date.now. */
  now?: () => number;
  /** How long to wait for the snapshot before falling back. Defaults to 15s. */
  snapshotTimeoutMs?: number;
  /** How long to wait for terminal/done after send before falling back. Defaults to 120s. */
  terminalTimeoutMs?: number;
  /** Poll cadence for the durable settle read. Defaults to 1s. */
  pollIntervalMs?: number;
  /** Sleep injector for tests (fake clock). Defaults to setTimeout. */
  sleepMs?: (ms: number) => Promise<void>;
};

export type StandingTurnResult = {
  /** Progressive live text assembled from token frames (display only). */
  liveText: string;
  /** Durable assistant reply when the settle read observed one. */
  text?: string;
  path: ChatTurnPath;
  threadId: string;
  sessionName?: string;
  sessionHarness?: string;
  turnId?: string;
  runName?: string;
  /** ms from stream open to the first live token frame. */
  sendToFirstTokenMs?: number;
  /** False when the WS path could not deliver (caller keeps its POST path). */
  delivered: boolean;
  /**
   * True once the turn POST was accepted. A fallback must never re-POST with
   * a fresh requestId (the frozen outbox dedupes by requestId); it settles by
   * polling the durable thread instead.
   */
  sent: boolean;
  /** Short machine-friendly reason when delivered is false or settle fell back. */
  error?: string;
};

export type StandingSessionView = {
  sessionName: string;
  harness?: string;
  warm?: boolean;
};

/** Extract display text from a live stream payload. Server token frames use `token`; older SSE shapes use delta/text/content/message. */
export function tokenTextFromStreamPayload(payload: unknown): string {
  if (typeof payload === "string") {
    return payload;
  }
  if (!payload || typeof payload !== "object") {
    return "";
  }
  const record = payload as Record<string, unknown>;
  for (const key of ["token", "delta", "text", "content", "message"] as const) {
    const value = record[key];
    if (typeof value === "string" && value) {
      return value;
    }
  }
  return "";
}

/** A token frame with `done: true` ends the turn; the server still sends `terminal` after it. */
export function isTokenFrameDone(payload: unknown): boolean {
  if (!payload || typeof payload !== "object") {
    return false;
  }
  return (payload as Record<string, unknown>).done === true;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

/** Read the resumed standing session view from a snapshot payload. Undefined on the Job plane. */
export function standingSessionFromSnapshot(payload: unknown): StandingSessionView | undefined {
  const record = asRecord(payload);
  if (!record) {
    return undefined;
  }
  const standing = asRecord(record.standing);
  if (!standing) {
    return undefined;
  }
  const sessionName = typeof standing.sessionName === "string" ? standing.sessionName.trim() : "";
  if (!sessionName) {
    return undefined;
  }
  const view: StandingSessionView = { sessionName };
  if (typeof standing.harness === "string" && standing.harness.trim()) {
    view.harness = standing.harness.trim();
  }
  if (typeof standing.warm === "boolean") {
    view.warm = standing.warm;
  }
  return view;
}

/** Classify the delivery path from a snapshot payload: standing session present or Job plane. */
export function standingPathFromSnapshot(payload: unknown): ChatTurnPath {
  return standingSessionFromSnapshot(payload) ? "standing" : "job";
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function errorReason(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message.trim().slice(0, 240);
  }
  return String(error ?? "unknown stream error").slice(0, 240);
}

/**
 * Run one live turn over an already-subscribed thread stream. Never throws:
 * every transport failure resolves to `{delivered: false, error}` so the
 * caller falls back to its existing POST/poll path with the same requestId.
 */
export async function streamStandingChatTurn(request: StandingTurnRequest): Promise<StandingTurnResult> {
  const now = request.now ?? (() => Date.now());
  const threadId = request.threadId.trim();
  const snapshotTimeoutMs = request.snapshotTimeoutMs ?? 15000;
  const terminalTimeoutMs = request.terminalTimeoutMs ?? 120000;
  const pollIntervalMs = request.pollIntervalMs ?? 1000;
  const sleepFor = request.sleepMs ?? sleep;
  const startedAt = now();

  const fail = (error: string, partial?: Partial<StandingTurnResult>): StandingTurnResult => ({
    liveText: "",
    path: "unknown",
    threadId,
    delivered: false,
    sent: false,
    error,
    ...partial,
  });

  if (!threadId) {
    return fail("missing thread id");
  }
  if (!request.content.trim()) {
    return fail("empty message", { path: "unknown" });
  }

  request.onStatus?.("waiting");

  let snapshot: Record<string, unknown> | undefined;
  let snapshotDone = false;
  let terminalSeen = false;
  let streamError: string | undefined;
  let settled = false;
  let firstTokenAt: number | undefined;
  let liveText = "";
  let doneTurnId: string | undefined;
  let doneRunName: string | undefined;
  const notifyDone = () => {
    settled = true;
  };

  let stream: StreamAbortHandle | undefined;
  try {
    stream = request.openStream(
      {
        onEvent: (event, payload) => {
          if (settled) {
            return;
          }
          const type = typeof payload.type === "string" ? payload.type : event;
          if (type === "snapshot" && !snapshotDone) {
            snapshotDone = true;
            snapshot = payload;
            return;
          }
          if (type === "token") {
            const frameThread =
              typeof payload.threadId === "string" ? payload.threadId : "";
            if (frameThread && frameThread !== threadId) {
              return;
            }
            if (typeof payload.turnId === "string" && payload.turnId) {
              doneTurnId = payload.turnId;
            }
            if (typeof payload.runName === "string" && payload.runName) {
              doneRunName = payload.runName;
            }
            if (isTokenFrameDone(payload)) {
              return;
            }
            const chunk = tokenTextFromStreamPayload(payload);
            if (!chunk) {
              return;
            }
            if (firstTokenAt === undefined) {
              firstTokenAt = now();
            }
            liveText += chunk;
            request.onStatus?.("running");
            request.onDelta?.(chunk);
            return;
          }
          if (type === "terminal") {
            terminalSeen = true;
            notifyDone();
          }
        },
        onTransportError: (error) => {
          if (settled) {
            return;
          }
          streamError = errorReason(error);
        },
        onDone: () => {
          notifyDone();
        },
      },
      { messages: 50 },
    );
  } catch (error) {
    return fail(`open stream failed: ${errorReason(error)}`);
  }

  const abort = () => {
    settled = true;
    try {
      stream?.abort();
    } catch {
      // Closing a dead stream must never break chat.
    }
  };

  try {
    const snapshotDeadline = startedAt + Math.max(0, snapshotTimeoutMs);
    while (!snapshotDone && !settled) {
      if (streamError) {
        abort();
        return fail(`open stream failed: ${streamError}`);
      }
      if (now() >= snapshotDeadline) {
        abort();
        return fail("stream snapshot timeout");
      }
      await sleepFor(Math.min(50, Math.max(1, pollIntervalMs)));
    }
    if (!snapshotDone || !snapshot) {
      abort();
      return fail(
        streamError ? `open stream failed: ${streamError}` : "stream closed before snapshot",
      );
    }

    const session = standingSessionFromSnapshot(snapshot);
    const path: ChatTurnPath = session ? "standing" : "job";
    const base: Partial<StandingTurnResult> = {
      path,
      sessionName: session?.sessionName,
      sessionHarness: session?.harness,
    };
    if (streamError) {
      abort();
      return fail(`open stream failed: ${streamError}`, base);
    }
    // Job-plane streams close after snapshot + terminal by contract: they
    // carry no live tokens, so the existing POST/poll path stays the turn
    // path and this attempt only classified the plane.
    if (!session) {
      abort();
      return fail("thread stays on the Job plane; use the POST turn path", base);
    }

    let sentTurnId: string | undefined;
    let sentRunName: string | undefined;
    try {
      const sent = await request.sendMessage({
        content: request.content,
        requestId: request.requestId,
      });
      sentTurnId = sent?.turnId;
      sentRunName = sent?.runName;
    } catch (error) {
      abort();
      return fail(`send failed: ${errorReason(error)}`, base);
    }
    if (!sentTurnId && doneTurnId) {
      sentTurnId = doneTurnId;
    }
    if (!sentRunName && doneRunName) {
      sentRunName = doneRunName;
    }
    const sentContext = { sent: true } as const;

    const terminalDeadline = now() + Math.max(0, terminalTimeoutMs);
    while (!settled && !terminalSeen) {
      if (streamError && firstTokenAt === undefined && !liveText) {
        break;
      }
      if (now() >= terminalDeadline) {
        break;
      }
      await sleepFor(Math.min(100, Math.max(1, pollIntervalMs)));
    }
    abort();

    const sendToFirstTokenMs =
      firstTokenAt === undefined ? undefined : firstTokenAt - startedAt;
    const context: Partial<StandingTurnResult> = {
      ...base,
      ...sentContext,
      threadId,
      turnId: sentTurnId ?? doneTurnId,
      runName: sentRunName ?? doneRunName,
      liveText,
      sendToFirstTokenMs,
    };

    // Settle from the durable record: the turn's assistant reply is the
    // source of truth even when every live frame arrived.
    const settleDeadline = now() + Math.min(Math.max(0, terminalTimeoutMs), 120000);
    for (;;) {
      try {
        const reply = await request.readThread();
        if (reply && reply.text.trim()) {
          return { ...context, text: reply.text, delivered: true, path } as StandingTurnResult;
        }
      } catch {
        // A failed settle read falls through to the live text below.
        break;
      }
      if (now() >= settleDeadline) {
        break;
      }
      await sleepFor(Math.min(1000, Math.max(1, pollIntervalMs)));
    }
    if (liveText.trim()) {
      return { ...context, text: liveText, delivered: true, path } as StandingTurnResult;
    }
    return fail(terminalSeen ? "durable reply not observed" : "terminal timeout", {
      ...context,
    });
  } finally {
    abort();
  }
}
