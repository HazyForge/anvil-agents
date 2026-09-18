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

/** Env var holding the absolute JSONL path for durable chat-latency reports. */
export const CHAT_LATENCY_JSONL_ENV = "ANVIL_CHAT_LATENCY_JSONL";

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
      runningMs: report.runningMs,
      replyReadyMs: report.replyReadyMs,
      failedMs: report.failedMs,
    };
    const source = (opts?.source || "").trim();
    if (source) {
      line["source"] = source;
    }
    for (const key of ["waitingMs", "firstTokenMs", "runningMs", "replyReadyMs", "failedMs"]) {
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
