#!/usr/bin/env node
/**
 * Desktop chat latency measurement (stub/API harness).
 * Measures send → waiting → first token → running → reply ready
 * without a live Desktop or cluster.
 *
 * Run: node --experimental-strip-types --test hack/desktop-chat-latency.mjs
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  createChatLatencyTracker,
  formatChatLatencyReport,
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
