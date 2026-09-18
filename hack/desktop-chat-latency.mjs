#!/usr/bin/env node
/**
 * Desktop chat latency measurement (stub/API harness).
 * Measures send → waiting → first token → running → reply ready
 * without a live Desktop or cluster.
 *
 * Run: node --experimental-strip-types --test hack/desktop-chat-latency.mjs
 */
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  appendChatLatencyReport,
  CHAT_LATENCY_JSONL_ENV,
  createChatLatencyTracker,
  formatChatLatencyJsonLine,
  formatChatLatencyReport,
  isAbsoluteLatencyPath,
  persistChatLatencyReport,
  resolveChatLatencyJsonlPath,
  withChatLatency,
} from "../web/desktop/src/wrapper/chatLatency.ts";

test("tracker records send→waiting→firstToken→running→replyReady with fake clock", () => {
  let t = 1_000;
  const tracker = createChatLatencyTracker(() => t);

  tracker.onStatus("waiting");
  t = 1_050;
  tracker.onDelta("H");
  t = 1_080;
  tracker.onStatus("running");
  t = 1_500;
  tracker.onDelta("ello");
  tracker.markReplyReady();

  const report = tracker.report();
  assert.equal(report.waitingMs, 0);
  assert.equal(report.firstTokenMs, 50);
  assert.equal(report.runningMs, 80);
  assert.equal(report.replyReadyMs, 500);
  assert.equal(report.failedMs, undefined);
});

test("first token is only the first onDelta", () => {
  let t = 0;
  const tracker = createChatLatencyTracker(() => t);
  t = 10;
  tracker.onDelta("a");
  t = 99;
  tracker.onDelta("b");
  assert.equal(tracker.report().firstTokenMs, 10);
});

test("withChatLatency forwards callbacks and marks status/delta", () => {
  let t = 0;
  const seen = { status: [], deltas: [] };
  const wrapped = withChatLatency(
    {
      onStatus: (s) => seen.status.push(s),
      onDelta: (d) => seen.deltas.push(d),
    },
    createChatLatencyTracker(() => t),
  );
  t = 5;
  wrapped.opts.onStatus?.("waiting");
  t = 25;
  wrapped.opts.onDelta?.("hi");
  t = 40;
  wrapped.opts.onStatus?.("running");
  wrapped.tracker.markReplyReady();

  assert.deepEqual(seen.status, ["waiting", "running"]);
  assert.deepEqual(seen.deltas, ["hi"]);
  const report = wrapped.tracker.report();
  assert.equal(report.waitingMs, 5);
  assert.equal(report.firstTokenMs, 25);
  assert.equal(report.runningMs, 40);
  assert.equal(report.replyReadyMs, 40);
});

test("failed path records failedMs", () => {
  let t = 0;
  const tracker = createChatLatencyTracker(() => t);
  t = 12;
  tracker.onStatus("waiting");
  t = 200;
  tracker.onStatus("failed");
  const report = tracker.report();
  assert.equal(report.failedMs, 200);
  assert.equal(report.replyReadyMs, undefined);
});

test("formatChatLatencyReport is pulse-friendly", () => {
  const line = formatChatLatencyReport({
    marks: { sendStartedAt: 0, waitingAt: 1, firstTokenAt: 40, runningAt: 50, replyReadyAt: 300 },
    waitingMs: 1,
    firstTokenMs: 40,
    runningMs: 50,
    replyReadyMs: 300,
  });
  assert.equal(line, "chat-latency waiting=1ms firstToken=40ms running=50ms replyReady=300ms");
});

test("live send wiring shape marks waiting/firstToken/running/replyReady end-to-end", () => {
  let t = 0;
  const seen = { status: [], deltas: [] };
  let latencyReport;
  // Shape mirrors streamDesktopChat opts: onDelta/onStatus plus an onLatency observer.
  const liveOpts = {
    onDelta: (d) => seen.deltas.push(d),
    onStatus: (s) => seen.status.push(s),
    onLatency: (r) => {
      latencyReport = r;
    },
  };
  const { opts: tracked, tracker } = withChatLatency(
    {
      onDelta: liveOpts.onDelta,
      onStatus: (s) => liveOpts.onStatus(s),
    },
    createChatLatencyTracker(() => t),
  );
  const settleLatency = (failed) => {
    if (!failed) {
      tracker.markReplyReady();
    }
    const report = tracker.report();
    liveOpts.onLatency(report);
    return formatChatLatencyReport(report);
  };

  // Replay the exact emission order of streamDesktopChat + proxyManagerHarnessChat.
  t = 2;
  tracked.onStatus?.("waiting"); // streamDesktopChat top
  t = 3;
  tracked.onStatus?.("waiting"); // proxyManagerHarnessChat top (deduped)
  t = 40;
  tracked.onStatus?.("running"); // first stream chunk arrives
  tracked.onDelta?.("hel"); // first token, same ms
  t = 90;
  tracked.onDelta?.("lo"); // later chunk, firstToken stays
  t = 95;
  tracked.onStatus?.("running"); // chips arrive
  t = 210;
  const line = settleLatency(false); // harness reply settles successfully

  assert.deepEqual(seen.status, ["waiting", "waiting", "running", "running"]);
  assert.deepEqual(seen.deltas, ["hel", "lo"]);
  assert.equal(latencyReport.waitingMs, 2);
  assert.equal(latencyReport.firstTokenMs, 40);
  assert.equal(latencyReport.runningMs, 40);
  assert.equal(latencyReport.replyReadyMs, 210);
  assert.equal(line, "chat-latency waiting=2ms firstToken=40ms running=40ms replyReady=210ms");
});

test("live send failure settles with failed report", () => {
  let t = 0;
  const seen = [];
  const { opts: tracked, tracker } = withChatLatency(
    { onStatus: (s) => seen.push(s) },
    createChatLatencyTracker(() => t),
  );
  t = 5;
  tracked.onStatus?.("waiting");
  t = 120;
  tracked.onStatus?.("failed");
  const report = tracker.report();
  const line = formatChatLatencyReport(report);
  assert.deepEqual(seen, ["waiting", "failed"]);
  assert.equal(report.replyReadyMs, undefined);
  assert.equal(line, "chat-latency waiting=5ms failed=120ms");
});

test("streamDesktopChat is wired to the latency tracker", () => {
  const here = dirname(fileURLToPath(import.meta.url));
  const src = readFileSync(
    join(here, "..", "web", "desktop", "src", "wrapper", "harnessChat.ts"),
    "utf8",
  );
  for (const needle of [
    "withChatLatency",
    "createChatLatencyTracker",
    "markReplyReady",
    "formatChatLatencyReport",
    "persistChatLatencyReport",
    "ANVIL_CHAT_LATENCY_JSONL",
    "onLatency",
    "streamDesktopChat",
  ]) {
    assert.ok(src.includes(needle), `harnessChat.ts should reference ${needle}`);
  }
});

test("JSONL sink resolves only absolute ANVIL_CHAT_LATENCY_JSONL paths", () => {
  assert.equal(CHAT_LATENCY_JSONL_ENV, "ANVIL_CHAT_LATENCY_JSONL");
  assert.equal(resolveChatLatencyJsonlPath({}), undefined);
  assert.equal(resolveChatLatencyJsonlPath({ [CHAT_LATENCY_JSONL_ENV]: "" }), undefined);
  assert.equal(resolveChatLatencyJsonlPath({ [CHAT_LATENCY_JSONL_ENV]: "relative/latency.jsonl" }), undefined);
  assert.equal(
    resolveChatLatencyJsonlPath({ [CHAT_LATENCY_JSONL_ENV]: join(tmpdir(), "chat-latency.jsonl") }),
    join(tmpdir(), "chat-latency.jsonl"),
  );
  assert.ok(isAbsoluteLatencyPath(join(tmpdir(), "x.jsonl")));
  assert.equal(isAbsoluteLatencyPath("relative/x.jsonl"), false);
  assert.equal(isAbsoluteLatencyPath(""), false);
});

test("formatChatLatencyJsonLine emits ISO ts + durations + source as one line", () => {
  const line = formatChatLatencyJsonLine(
    {
      marks: { sendStartedAt: 0 },
      waitingMs: 12,
      firstTokenMs: 48,
      runningMs: 60,
      replyReadyMs: 300,
    },
    { source: "desktop-chat", now: () => Date.parse("2026-09-18T04:20:00.000Z") },
  );
  assert.ok(line.endsWith("\n"));
  assert.equal(line.trim().split("\n").length, 1);
  const parsed = JSON.parse(line);
  assert.equal(parsed.ts, "2026-09-18T04:20:00.000Z");
  assert.equal(parsed.waitingMs, 12);
  assert.equal(parsed.firstTokenMs, 48);
  assert.equal(parsed.runningMs, 60);
  assert.equal(parsed.replyReadyMs, 300);
  assert.equal(parsed.failedMs, undefined);
  assert.equal(parsed.source, "desktop-chat");
});

test("appendChatLatencyReport appends one JSONL line per settled report", () => {
  const dir = mkdtempSync(join(tmpdir(), "chat-latency-"));
  const path = join(dir, "chat-latency.jsonl");
  const report = {
    marks: { sendStartedAt: 0 },
    waitingMs: 5,
    firstTokenMs: 40,
    runningMs: 42,
    replyReadyMs: 210,
  };
  assert.equal(appendChatLatencyReport(report, { path, source: "stub-test" }), true);
  assert.equal(
    appendChatLatencyReport({ marks: { sendStartedAt: 0 }, failedMs: 99 }, { path }),
    true,
  );
  const lines = readFileSync(path, "utf8").trim().split("\n");
  assert.equal(lines.length, 2);
  assert.equal(JSON.parse(lines[0]).replyReadyMs, 210);
  assert.equal(JSON.parse(lines[0]).source, "stub-test");
  assert.equal(JSON.parse(lines[1]).failedMs, 99);
  assert.equal("source" in JSON.parse(lines[1]), false);
});

test("appendChatLatencyReport never throws and skips non-absolute paths", () => {
  const report = { marks: { sendStartedAt: 0 }, waitingMs: 1 };
  assert.equal(appendChatLatencyReport(report, { path: "relative/latency.jsonl" }), false);
  assert.equal(appendChatLatencyReport(report, { path: "" }), false);
  assert.equal(
    appendChatLatencyReport(report, {
      path: join(tmpdir(), "chat-latency.jsonl"),
      appendFileSync: () => {
        throw new Error("disk unavailable");
      },
    }),
    false,
  );
});

test("persistChatLatencyReport writes only when the env path is set", () => {
  const dir = mkdtempSync(join(tmpdir(), "chat-latency-env-"));
  const path = join(dir, "chat-latency.jsonl");
  const report = { marks: { sendStartedAt: 0 }, waitingMs: 2, replyReadyMs: 50 };
  assert.equal(persistChatLatencyReport(report, { env: {} }), false);
  assert.equal(
    persistChatLatencyReport(report, { env: { [CHAT_LATENCY_JSONL_ENV]: "relative.jsonl" } }),
    false,
  );
  let calls = 0;
  assert.equal(
    persistChatLatencyReport(report, {
      env: {},
      appendFileSync: () => {
        calls += 1;
      },
    }),
    false,
  );
  assert.equal(calls, 0);
  assert.equal(
    persistChatLatencyReport(report, {
      env: { [CHAT_LATENCY_JSONL_ENV]: path },
      source: "desktop-chat",
    }),
    true,
  );
  const parsed = JSON.parse(readFileSync(path, "utf8").trim());
  assert.equal(parsed.replyReadyMs, 50);
  assert.equal(parsed.source, "desktop-chat");
});
