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
  /**
   * Alias of firstTokenMs under the standing-latency vocabulary
   * (Desktop send → first live token). Always equals firstTokenMs when both
   * are present; emitted so `.runtime/chat-latency.jsonl` carries the
   * `sendToFirstTokenMs` field the Desktop e2e documents.
   */
  sendToFirstTokenMs?: number;
  /** ms from send to onStatus("running") */
  runningMs?: number;
  /** ms from send to reply settled */
  replyReadyMs?: number;
  failedMs?: number;
  /** Durable thread id the turn ran on, when known. */
  threadId?: string;
  /** Standing session name from the stream snapshot, when known. */
  sessionId?: string;
  /** Delivery plane the turn took: standing WS tokens vs the Job POST path. */
  path?: string;
  /** Short machine-friendly failure reason, when the turn did not deliver. */
  error?: string;
};

export type ChatLatencyTurnContext = {
  threadId?: string;
  sessionId?: string;
  path?: string;
};

export type ChatLatencyTracker = {
  marks: ChatLatencyMarks;
  onStatus: (status: string) => void;
  onDelta: (chunk: string) => void;
  markReplyReady: () => void;
  /** Record which thread/session/plane the turn ran on for the JSONL line. */
  setTurnContext: (context: ChatLatencyTurnContext) => void;
  /** Record a short failure reason for the JSONL line. Never throws. */
  markError: (message: string) => void;
  report: () => ChatLatencyReport;
};

/** Create a latency tracker. Pass a fake `now` in tests. */
export function createChatLatencyTracker(now: () => number = () => Date.now()): ChatLatencyTracker {
  const marks: ChatLatencyMarks = { sendStartedAt: now() };
  let turnContext: ChatLatencyTurnContext = {};
  let errorMessage: string | undefined;

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
    setTurnContext(context: ChatLatencyTurnContext) {
      try {
        turnContext = {
          ...turnContext,
          ...(context?.threadId?.trim() ? { threadId: context.threadId.trim() } : {}),
          ...(context?.sessionId?.trim() ? { sessionId: context.sessionId.trim() } : {}),
          ...(context?.path?.trim() ? { path: context.path.trim() } : {}),
        };
      } catch {
        // Context must never break chat.
      }
    },
    markError(message: string) {
      try {
        const trimmed = (message || "").trim().slice(0, 240);
        if (trimmed && errorMessage === undefined) {
          errorMessage = trimmed;
        }
      } catch {
        // Error context must never break chat.
      }
    },
    report(): ChatLatencyReport {
      const base = marks.sendStartedAt;
      const delta = (t?: number) => (t === undefined ? undefined : t - base);
      const firstTokenMs = delta(marks.firstTokenAt);
      const report: ChatLatencyReport = {
        marks: { ...marks },
        waitingMs: delta(marks.waitingAt),
        firstTokenMs,
        runningMs: delta(marks.runningAt),
        replyReadyMs: delta(marks.replyReadyAt),
        failedMs: delta(marks.failedAt),
      };
      if (firstTokenMs !== undefined) {
        report.sendToFirstTokenMs = firstTokenMs;
      }
      if (turnContext.threadId) {
        report.threadId = turnContext.threadId;
      }
      if (turnContext.sessionId) {
        report.sessionId = turnContext.sessionId;
      }
      if (turnContext.path) {
        report.path = turnContext.path;
      }
      if (errorMessage) {
        report.error = errorMessage;
      }
      return report;
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

/** Env var holding the absolute JSONL path for durable chat-latency reports. */
export const CHAT_LATENCY_JSONL_ENV = "ANVIL_CHAT_LATENCY_JSONL";

/**
 * Same-origin loopback endpoint on the Go `anvil-desktop` host that appends
 * one JSONL line to the host's ANVIL_CHAT_LATENCY_JSONL sink. The Desktop UI
 * runs in the browser (or Electron with nodeIntegration: false), where
 * process.env/Node fs are unavailable, so live chat must POST here instead.
 */
export const CHAT_LATENCY_ENDPOINT = "/local/v1/chat-latency";

export type ChatLatencyJsonLineOptions = {
  /** Optional source label recorded on the JSONL line (e.g. "desktop-chat"). */
  source?: string;
  /** Clock override for tests. Defaults to Date.now. */
  now?: () => number;
};

export type ChatLatencyAppendOptions = ChatLatencyJsonLineOptions & {
  /** Absolute file path to append one JSON line to. */
  path: string;
  /** FS injector for tests. Defaults to a lazy Node fs lookup (browser-safe no-op). */
  appendFileSync?: (path: string, data: string, encoding?: string) => void;
  /** FS injector for parent-dir creation. Defaults to a lazy Node fs lookup. */
  mkdirSync?: (path: string, opts?: { recursive: boolean }) => void;
};

export type ChatLatencyPersistOptions = ChatLatencyJsonLineOptions & {
  /** Env record override for tests. Defaults to globalThis process.env. */
  env?: Record<string, string | undefined>;
  appendFileSync?: (path: string, data: string, encoding?: string) => void;
  mkdirSync?: (path: string, opts?: { recursive: boolean }) => void;
};

/** Absolute-path check without a node:path import so browser bundles stay intact. */
export function isAbsoluteLatencyPath(path: string): boolean {
  if (!path) {
    return false;
  }
  if (path.startsWith("/")) {
    return true;
  }
  if (/^[A-Za-z]:[\\/]/.test(path)) {
    return true;
  }
  return path.startsWith("\\\\");
}

function latencyEnvRecord(
  env?: Record<string, string | undefined>,
): Record<string, string | undefined> | undefined {
  if (env) {
    return env;
  }
  try {
    const proc = (globalThis as unknown as { process?: { env?: Record<string, string | undefined> } }).process;
    return proc?.env;
  } catch {
    return undefined;
  }
}

/** Resolve the configured JSONL sink path. Returns undefined when unset or not absolute. */
export function resolveChatLatencyJsonlPath(env?: Record<string, string | undefined>): string | undefined {
  try {
    const record = latencyEnvRecord(env);
    const raw = (record?.[CHAT_LATENCY_JSONL_ENV] || "").trim();
    if (!raw || !isAbsoluteLatencyPath(raw)) {
      return undefined;
    }
    return raw;
  } catch {
    return undefined;
  }
}

/** Build one JSONL line: ISO timestamp + report durations + optional source. Ends with \n. */
export function formatChatLatencyJsonLine(
  report: ChatLatencyReport,
  opts?: ChatLatencyJsonLineOptions,
): string {
  try {
    const now = opts?.now ?? (() => Date.now());
    const line: Record<string, string | number | undefined> = {
      ts: new Date(now()).toISOString(),
      waitingMs: report.waitingMs,
      firstTokenMs: report.firstTokenMs,
      sendToFirstTokenMs: report.sendToFirstTokenMs,
      runningMs: report.runningMs,
      replyReadyMs: report.replyReadyMs,
      failedMs: report.failedMs,
      threadId: report.threadId,
      sessionId: report.sessionId,
      path: report.path,
      error: report.error,
    };
    const source = (opts?.source || "").trim();
    if (source) {
      line["source"] = source;
    }
    for (const key of ["waitingMs", "firstTokenMs", "sendToFirstTokenMs", "runningMs", "replyReadyMs", "failedMs", "threadId", "sessionId", "path", "error"]) {
      if (line[key] === undefined) {
        delete line[key];
      }
    }
    return `${JSON.stringify(line)}\n`;
  } catch {
    return "";
  }
}

type NodeFsShim = {
  appendFileSync: (path: string, data: string, encoding?: string) => void;
  mkdirSync: (path: string, opts?: { recursive: boolean }) => void;
};

function lazyNodeFs(): NodeFsShim | undefined {
  try {
    const proc = (
      globalThis as unknown as {
        process?: { getBuiltinModule?: (id: string) => unknown };
        require?: (id: string) => unknown;
      }
    ).process;
    const getBuiltin = proc?.getBuiltinModule;
    if (typeof getBuiltin === "function") {
      const fs = getBuiltin.call(proc, "node:fs") as NodeFsShim | undefined;
      if (fs && typeof fs.appendFileSync === "function") {
        return fs;
      }
    }
    const req = (globalThis as unknown as { require?: (id: string) => unknown }).require;
    if (typeof req === "function") {
      const fs = req("node:fs") as NodeFsShim | undefined;
      if (fs && typeof fs.appendFileSync === "function") {
        return fs;
      }
    }
  } catch {
    // Browser or locked-down runtime: no Node fs available.
  }
  return undefined;
}

function parentDirOf(path: string): string | undefined {
  const slash = path.lastIndexOf("/");
  const backslash = path.lastIndexOf("\\");
  const idx = Math.max(slash, backslash);
  if (idx <= 0) {
    return undefined;
  }
  return path.slice(0, idx);
}

/**
 * Append one settled report as a single JSON line. Never throws: returns true
 * when a line was written, false when skipped (relative path, missing fs) or
 * when the write failed. Sync append is fine for Desktop.
 */
export function appendChatLatencyReport(
  report: ChatLatencyReport,
  opts: ChatLatencyAppendOptions,
): boolean {
  try {
    const path = (opts?.path || "").trim();
    if (!path || !isAbsoluteLatencyPath(path)) {
      return false;
    }
    const source = (opts?.source || "").trim();
    const line = formatChatLatencyJsonLine(report, { source: source || undefined, now: opts?.now });
    if (!line) {
      return false;
    }
    const appendFn = opts?.appendFileSync ?? lazyNodeFs()?.appendFileSync;
    if (typeof appendFn !== "function") {
      return false;
    }
    const mkdirFn = opts?.mkdirSync ?? lazyNodeFs()?.mkdirSync;
    const parent = parentDirOf(path);
    if (parent && typeof mkdirFn === "function") {
      try {
        mkdirFn(parent, { recursive: true });
      } catch {
        // Best effort: the append below still decides success.
      }
    }
    appendFn(path, line, "utf8");
    return true;
  } catch {
    return false;
  }
}

/**
 * Resolve ANVIL_CHAT_LATENCY_JSONL and append one JSONL line when configured.
 * Never throws: returns true when a line was written, false otherwise.
 */
export function persistChatLatencyReport(
  report: ChatLatencyReport,
  opts?: ChatLatencyPersistOptions,
): boolean {
  try {
    const path = resolveChatLatencyJsonlPath(opts?.env);
    if (!path) {
      return false;
    }
    return appendChatLatencyReport(report, {
      path,
      source: opts?.source,
      now: opts?.now,
      appendFileSync: opts?.appendFileSync,
      mkdirSync: opts?.mkdirSync,
    });
  } catch {
    return false;
  }
}

export type ChatLatencyLinePayload = {
  ts: string;
  waitingMs?: number;
  firstTokenMs?: number;
  sendToFirstTokenMs?: number;
  runningMs?: number;
  replyReadyMs?: number;
  failedMs?: number;
  source?: string;
  threadId?: string;
  sessionId?: string;
  path?: string;
  error?: string;
};

/** Build the JSON object POSTed to the loopback host (same fields as JSONL). */
export function buildChatLatencyPayload(
  report: ChatLatencyReport,
  opts?: ChatLatencyJsonLineOptions,
): ChatLatencyLinePayload {
  const now = opts?.now ?? (() => Date.now());
  const payload: ChatLatencyLinePayload = {
    ts: new Date(now()).toISOString(),
  };
  if (report.waitingMs !== undefined) payload.waitingMs = report.waitingMs;
  if (report.firstTokenMs !== undefined) payload.firstTokenMs = report.firstTokenMs;
  if (report.sendToFirstTokenMs !== undefined) payload.sendToFirstTokenMs = report.sendToFirstTokenMs;
  if (report.runningMs !== undefined) payload.runningMs = report.runningMs;
  if (report.replyReadyMs !== undefined) payload.replyReadyMs = report.replyReadyMs;
  if (report.failedMs !== undefined) payload.failedMs = report.failedMs;
  if (report.threadId !== undefined) payload.threadId = report.threadId;
  if (report.sessionId !== undefined) payload.sessionId = report.sessionId;
  if (report.path !== undefined) payload.path = report.path;
  if (report.error !== undefined) payload.error = report.error;
  const source = (opts?.source || "").trim();
  if (source) payload.source = source;
  return payload;
}

type ChatLatencyFetchResponse = {
  ok?: boolean;
  status?: number;
};

type ChatLatencyFetchFn = (
  input: string,
  init?: { method?: string; headers?: Record<string, string>; body?: string },
) => Promise<ChatLatencyFetchResponse>;

export type ChatLatencyPersistAsyncOptions = ChatLatencyPersistOptions & {
  /** Same-origin endpoint override for tests. Defaults to CHAT_LATENCY_ENDPOINT. */
  endpoint?: string;
  /** Fetch injector for tests. Defaults to globalThis.fetch. */
  fetchFn?: ChatLatencyFetchFn;
};

function resolveLatencyFetch(fetchFn?: ChatLatencyFetchFn): ChatLatencyFetchFn | undefined {
  if (typeof fetchFn === "function") {
    return fetchFn;
  }
  try {
    const impl = (globalThis as unknown as { fetch?: unknown }).fetch;
    if (typeof impl === "function") {
      return impl as ChatLatencyFetchFn;
    }
  } catch {
    return undefined;
  }
  return undefined;
}

/**
 * Browser-capable persist: try the Node fs/env path first (tests, record
 * script, Node harnesses), then POST the same payload to the loopback host
 * so live Desktop chat in the browser still captures JSONL when the host was
 * started with ANVIL_CHAT_LATENCY_JSONL set. Never throws: resolves true
 * when either path accepted the report, false otherwise.
 */
export async function persistChatLatencyReportAsync(
  report: ChatLatencyReport,
  opts?: ChatLatencyPersistAsyncOptions,
): Promise<boolean> {
  try {
    if (persistChatLatencyReport(report, opts)) {
      return true;
    }
  } catch {
    // Fall through to the loopback POST path.
  }
  try {
    const fetchImpl = resolveLatencyFetch(opts?.fetchFn);
    if (!fetchImpl) {
      return false;
    }
    const endpoint = (opts?.endpoint || CHAT_LATENCY_ENDPOINT).trim() || CHAT_LATENCY_ENDPOINT;
    const payload = buildChatLatencyPayload(report, { source: opts?.source, now: opts?.now });
    const response = await fetchImpl(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const status = response?.status;
    if (response?.ok) {
      return true;
    }
    // No-content / explicit 204 from the host counts as accepted.
    if (status === 204 || status === 200) {
      return true;
    }
    return false;
  } catch {
    return false;
  }
}
