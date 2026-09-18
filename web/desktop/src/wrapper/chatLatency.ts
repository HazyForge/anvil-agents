/**
 * Desktop chat snappiness measurement helpers.
 * Stub/API-first: wrap streamDesktopChat callbacks to mark
 * send → waiting → first token → running → reply ready.
 * No cluster/Desktop required for unit tests.
 */

export type ChatLatencyMarks = {
  sendStartedAt: number;
  waitingAt?: number;
  firstTokenAt?: number;
  runningAt?: number;
  replyReadyAt?: number;
  failedAt?: number;
};

export type ChatLatencyReport = {
  marks: ChatLatencyMarks;
  /** ms from send to first onStatus("waiting") */
  waitingMs?: number;
  /** ms from send to first onDelta chunk (first token) */
  firstTokenMs?: number;
  /** ms from send to onStatus("running") */
  runningMs?: number;
  /** ms from send to reply settled */
  replyReadyMs?: number;
  failedMs?: number;
};

export type ChatLatencyTracker = {
  marks: ChatLatencyMarks;
  onStatus: (status: string) => void;
  onDelta: (chunk: string) => void;
  markReplyReady: () => void;
  report: () => ChatLatencyReport;
};

/** Create a latency tracker. Pass a fake `now` in tests. */
export function createChatLatencyTracker(now: () => number = () => Date.now()): ChatLatencyTracker {
  const marks: ChatLatencyMarks = { sendStartedAt: now() };

  return {
    marks,
    onStatus(status: string) {
      const s = (status || "").trim().toLowerCase();
      if (s === "waiting" && marks.waitingAt === undefined) {
        marks.waitingAt = now();
      }
      if (s === "running" && marks.runningAt === undefined) {
        marks.runningAt = now();
      }
      if (s === "failed" && marks.failedAt === undefined) {
        marks.failedAt = now();
      }
    },
    onDelta(_chunk: string) {
      if (marks.firstTokenAt === undefined) {
        marks.firstTokenAt = now();
      }
    },
    markReplyReady() {
      if (marks.replyReadyAt === undefined) {
        marks.replyReadyAt = now();
      }
    },
    report(): ChatLatencyReport {
      const base = marks.sendStartedAt;
      const delta = (t?: number) => (t === undefined ? undefined : t - base);
      return {
        marks: { ...marks },
        waitingMs: delta(marks.waitingAt),
        firstTokenMs: delta(marks.firstTokenAt),
        runningMs: delta(marks.runningAt),
        replyReadyMs: delta(marks.replyReadyAt),
        failedMs: delta(marks.failedAt),
      };
    },
  };
}

type StreamCallbacks = {
  onDelta?: (text: string) => void;
  onStatus?: (status: string) => void;
};

/**
 * Wire tracker into streamDesktopChat-style callbacks without changing
 * call sites beyond wrapping opts.
 */
export function withChatLatency<T extends StreamCallbacks>(
  opts: T,
  tracker: ChatLatencyTracker = createChatLatencyTracker(),
): { opts: T; tracker: ChatLatencyTracker } {
  return {
    tracker,
    opts: {
      ...opts,
      onStatus: (status: string) => {
        tracker.onStatus(status);
        opts.onStatus?.(status);
      },
      onDelta: (text: string) => {
        tracker.onDelta(text);
        opts.onDelta?.(text);
      },
    },
  };
}

/** Format a one-line summary for Desktop logs / pulse reports. */
export function formatChatLatencyReport(report: ChatLatencyReport): string {
  const parts: string[] = ["chat-latency"];
  if (report.waitingMs !== undefined) parts.push(`waiting=${report.waitingMs}ms`);
  if (report.firstTokenMs !== undefined) parts.push(`firstToken=${report.firstTokenMs}ms`);
  if (report.runningMs !== undefined) parts.push(`running=${report.runningMs}ms`);
  if (report.replyReadyMs !== undefined) parts.push(`replyReady=${report.replyReadyMs}ms`);
  if (report.failedMs !== undefined) parts.push(`failed=${report.failedMs}ms`);
  return parts.join(" ");
}
