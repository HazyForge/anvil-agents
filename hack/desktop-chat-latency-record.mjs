#!/usr/bin/env node
/**
 * Record one sample chat-latency JSONL line without a live Desktop/cluster.
 *
 * Simulates a realistic send → waiting → firstToken → running → replyReady
 * sequence through createChatLatencyTracker, appends one JSON line to
 * .runtime/chat-latency.jsonl (repo root), and prints the one-line report.
 *
 * Run: node --experimental-strip-types hack/desktop-chat-latency-record.mjs [--out path] [--source label]
 *   --waiting-ms N --first-token-ms N --running-ms N --reply-ready-ms N --failed-ms N
 */
import { mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import {
  appendChatLatencyReport,
  CHAT_LATENCY_JSONL_ENV,
  createChatLatencyTracker,
  formatChatLatencyReport,
} from "../web/desktop/src/wrapper/chatLatency.ts";

function argValue(args, name) {
  const idx = args.indexOf(name);
  if (idx === -1 || idx + 1 >= args.length) {
    return undefined;
  }
  return args[idx + 1];
}

function numberOr(value, fallback) {
  if (value === undefined) {
    return fallback;
  }
  const n = Number(value);
  return Number.isFinite(n) && n >= 0 ? n : fallback;
}

const args = process.argv.slice(2);
if (args.includes("--help") || args.includes("-h")) {
  console.log(
    [
      "Usage: node --experimental-strip-types hack/desktop-chat-latency-record.mjs [options]",
      "",
      "  --out PATH            JSONL path (default: .runtime/chat-latency.jsonl).",
      `  --source LABEL        source label (default: record-sample). Env ${CHAT_LATENCY_JSONL_ENV} overrides --out when set.`,
      "  --waiting-ms N        send → waiting ms (default 120).",
      "  --first-token-ms N    send → first token ms (default 480).",
      "  --running-ms N        send → running ms (default 540).",
      "  --reply-ready-ms N    send → reply ready ms (default 1750).",
      "  --failed-ms N         when set, record a failed send instead of replyReady.",
    ].join("\n"),
  );
  process.exit(0);
}

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");
const defaultOut = join(repoRoot, ".runtime", "chat-latency.jsonl");
const out = resolve(argValue(args, "--out") || process.env[CHAT_LATENCY_JSONL_ENV] || defaultOut);
const source = argValue(args, "--source") || "record-sample";

const waitingMs = numberOr(argValue(args, "--waiting-ms"), 120);
const firstTokenMs = numberOr(argValue(args, "--first-token-ms"), 480);
const runningMs = numberOr(argValue(args, "--running-ms"), 540);
const replyReadyMs = numberOr(argValue(args, "--reply-ready-ms"), 1750);
const failedRaw = argValue(args, "--failed-ms");
const failedMs = failedRaw === undefined ? undefined : numberOr(failedRaw, 0);

const t0 = Date.now();
let t = t0;
const tracker = createChatLatencyTracker(() => t);

t = t0 + waitingMs;
tracker.onStatus("waiting");
if (failedMs !== undefined) {
  t = t0 + failedMs;
  tracker.onStatus("failed");
} else {
  t = t0 + firstTokenMs;
  tracker.onDelta("hel");
  t = t0 + runningMs;
  tracker.onStatus("running");
  t = t0 + replyReadyMs;
  tracker.onDelta("lo");
  tracker.markReplyReady();
}

const report = tracker.report();
mkdirSync(dirname(out), { recursive: true });
const wrote = appendChatLatencyReport(report, { path: out, source });
console.log(formatChatLatencyReport(report));
console.log(wrote ? `wrote ${out}` : `skip write ${out}`);
