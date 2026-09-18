#!/usr/bin/env node
/**
 * Desktop standing chat stream e2e (fake transport, CI-safe).
 *
 * Drives web/desktop/src/wrapper/standingChatTurn.ts with a faked thread
 * stream (snapshot + token frames + terminal) and asserts the Desktop client
 * records send → first-token timing and the standing vs Job path tag into
 * `.runtime/chat-latency.jsonl`-shaped JSONL. No live cluster, no OIDC.
 *
 * Run: node --experimental-strip-types --test hack/desktop-standing-chat-stream.mjs
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  isTokenFrameDone,
  standingPathFromSnapshot,
  standingSessionFromSnapshot,
  streamStandingChatTurn,
  tokenTextFromStreamPayload,
} from "../web/desktop/src/wrapper/standingChatTurn.ts";
import {
  createChatLatencyTracker,
  formatChatLatencyJsonLine,
  withChatLatency,
} from "../web/desktop/src/wrapper/chatLatency.ts";

const here = dirname(fileURLToPath(import.meta.url));
const readSource = (rel) => readFileSync(join(here, "..", rel), "utf8");

function fakeClock() {
  let t = 0;
  return {
    now: () => t,
    advance: (ms) => {
      t += ms;
    },
    sleepMs: async (ms) => {
      t += ms;
    },
  };
}

function standingSnapshot(threadId = "thread-1") {
  return {
    type: "snapshot",
    thread: { id: threadId },
    messages: [],
    standing: { sessionName: "standing-thread-1", harness: "openCode", warm: true, resumes: 2 },
  };
}

test("token frames use the server token key and done ends the turn", () => {
  assert.equal(tokenTextFromStreamPayload({ type: "token", token: "hel" }), "hel");
  assert.equal(tokenTextFromStreamPayload({ type: "token", token: "hel", text: "ignored" }), "hel");
  assert.equal(tokenTextFromStreamPayload({ delta: "d" }), "d");
  assert.equal(tokenTextFromStreamPayload({ text: "t" }), "t");
  assert.equal(tokenTextFromStreamPayload({ content: "c" }), "c");
  assert.equal(tokenTextFromStreamPayload({ message: "m" }), "m");
  assert.equal(tokenTextFromStreamPayload({ token: "" }), "");
  assert.equal(tokenTextFromStreamPayload("raw"), "raw");
  assert.equal(tokenTextFromStreamPayload(null), "");
  assert.equal(isTokenFrameDone({ done: true }), true);
  assert.equal(isTokenFrameDone({ done: false }), false);
  assert.equal(isTokenFrameDone({}), false);
});

test("snapshot classifies the standing vs Job path", () => {
  assert.equal(standingPathFromSnapshot(standingSnapshot()), "standing");
  assert.equal(standingPathFromSnapshot({ type: "snapshot", messages: [] }), "job");
  assert.deepEqual(standingSessionFromSnapshot(standingSnapshot()), {
    sessionName: "standing-thread-1",
    harness: "openCode",
    warm: true,
  });
  assert.equal(standingSessionFromSnapshot({ type: "snapshot" }), undefined);
});

test("standing live turn streams tokens and settles from the durable reply", async () => {
  const clock = fakeClock();
  const seen = { deltas: [], statuses: [] };
  let handlers;
  const openStream = (h) => {
    handlers = h;
    h.onEvent("snapshot", standingSnapshot("thread-1"));
    return { abort: () => {} };
  };
  const sendMessage = async ({ content, requestId }) => {
    assert.equal(content, "hello manager");
    assert.match(requestId, /^[0-9a-f-]{36}$/);
    clock.advance(50);
    handlers.onEvent("token", { type: "token", threadId: "thread-1", turnId: "turn-1", runName: "chat-turn-abc", seq: 0, token: "hel" });
    handlers.onEvent("token", { type: "token", threadId: "thread-1", turnId: "turn-1", runName: "chat-turn-abc", seq: 1, token: "lo" });
    // Peer-child frames for another thread must never leak into this turn.
    handlers.onEvent("token", { type: "token", threadId: "peer-child", turnId: "turn-9", seq: 0, token: "nope" });
    handlers.onEvent("token", { type: "token", threadId: "thread-1", turnId: "turn-1", runName: "chat-turn-abc", seq: 2, token: "", done: true });
    handlers.onEvent("terminal", { type: "terminal", code: "standing_ready" });
    return { turnId: "turn-1", runName: "chat-turn-abc" };
  };

  const { opts: tracked, tracker } = withChatLatency(
    {
      onDelta: (d) => seen.deltas.push(d),
      onStatus: (s) => seen.statuses.push(s),
    },
    createChatLatencyTracker(clock.now),
  );

  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello manager",
    requestId: "11111111-1111-4111-8111-111111111111",
    openStream,
    sendMessage,
    readThread: async () => ({ text: "hello back (durable)" }),
    onDelta: tracked.onDelta,
    onStatus: tracked.onStatus,
    now: clock.now,
    sleepMs: clock.sleepMs,
  });

  assert.equal(result.delivered, true);
  assert.equal(result.sent, true);
  assert.equal(result.path, "standing");
  assert.equal(result.sessionName, "standing-thread-1");
  assert.equal(result.threadId, "thread-1");
  assert.equal(result.turnId, "turn-1");
  assert.equal(result.runName, "chat-turn-abc");
  assert.equal(result.liveText, "hello");
  assert.equal(result.text, "hello back (durable)");
  assert.equal(result.sendToFirstTokenMs, 50);
  assert.deepEqual(seen.deltas, ["hel", "lo"]);

  tracker.setTurnContext({ threadId: result.threadId, sessionId: result.sessionName, path: result.path });
  tracker.markReplyReady();
  const report = tracker.report();
  assert.equal(report.firstTokenMs, 50);
  assert.equal(report.sendToFirstTokenMs, 50);
  const line = formatChatLatencyJsonLine(report, {
    source: "desktop-chat",
    now: () => Date.parse("2026-09-18T04:20:00.000Z"),
  });
  const parsed = JSON.parse(line);
  assert.equal(parsed.source, "desktop-chat");
  assert.equal(parsed.sendToFirstTokenMs, 50);
  assert.equal(parsed.firstTokenMs, 50);
  assert.equal(parsed.threadId, "thread-1");
  assert.equal(parsed.sessionId, "standing-thread-1");
  assert.equal(parsed.path, "standing");
  assert.equal(parsed.ts, "2026-09-18T04:20:00.000Z");
  assert.ok(line.endsWith("\n"));
});

test("Job-plane snapshot never sends: the POST path stays the turn path", async () => {
  const clock = fakeClock();
  let sends = 0;
  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello",
    requestId: "22222222-2222-4222-8222-222222222222",
    openStream: (h) => {
      h.onEvent("snapshot", { type: "snapshot", messages: [] });
      return { abort: () => {} };
    },
    sendMessage: async () => {
      sends += 1;
      return {};
    },
    readThread: async () => undefined,
    now: clock.now,
    sleepMs: clock.sleepMs,
  });
  assert.equal(result.delivered, false);
  assert.equal(result.sent, false);
  assert.equal(result.path, "job");
  assert.equal(sends, 0);
  assert.match(result.error ?? "", /Job plane/);
});

test("WS open failure falls back without sending", async () => {
  const clock = fakeClock();
  let sends = 0;
  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello",
    requestId: "33333333-3333-4333-8333-333333333333",
    openStream: () => {
      throw new Error("WebSocket blocked");
    },
    sendMessage: async () => {
      sends += 1;
      return {};
    },
    readThread: async () => undefined,
    now: clock.now,
    sleepMs: clock.sleepMs,
  });
  assert.equal(result.delivered, false);
  assert.equal(result.sent, false);
  assert.equal(sends, 0);
  assert.match(result.error ?? "", /open stream failed/);
});

test("denied send (stub OIDC) records the error without a second turn", async () => {
  const clock = fakeClock();
  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello",
    requestId: "44444444-4444-4444-8444-444444444444",
    openStream: (h) => {
      h.onEvent("snapshot", standingSnapshot("thread-1"));
      return { abort: () => {} };
    },
    sendMessage: async () => {
      throw new Error("unauthorized — sign in again");
    },
    readThread: async () => undefined,
    now: clock.now,
    sleepMs: clock.sleepMs,
  });
  assert.equal(result.delivered, false);
  assert.equal(result.sent, false);
  assert.equal(result.path, "standing");
  assert.match(result.error ?? "", /send failed.*unauthorized/);
});

test("accepted-but-unstreamed turn reports sent so the caller polls instead of re-POSTing", async () => {
  const clock = fakeClock();
  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello",
    requestId: "55555555-5555-4555-8555-555555555555",
    openStream: (h) => {
      h.onEvent("snapshot", standingSnapshot("thread-1"));
      return { abort: () => {} };
    },
    sendMessage: async () => ({ turnId: "turn-5", runName: "chat-turn-5" }),
    readThread: async () => undefined,
    now: clock.now,
    sleepMs: clock.sleepMs,
    terminalTimeoutMs: 500,
  });
  assert.equal(result.delivered, false);
  assert.equal(result.sent, true);
  assert.equal(result.path, "standing");
  assert.match(result.error ?? "", /terminal timeout/);
});

test("live text without a durable reply still delivers the streamed reply", async () => {
  const clock = fakeClock();
  let handlers;
  const result = await streamStandingChatTurn({
    threadId: "thread-1",
    content: "hello",
    requestId: "66666666-6666-4666-8666-666666666666",
    openStream: (h) => {
      handlers = h;
      h.onEvent("snapshot", standingSnapshot("thread-1"));
      return { abort: () => {} };
    },
    sendMessage: async () => {
      clock.advance(25);
      handlers.onEvent("token", { type: "token", threadId: "thread-1", turnId: "t", seq: 0, token: "hi" });
      handlers.onEvent("terminal", { type: "terminal", code: "standing_ready" });
      return { turnId: "t" };
    },
    readThread: async () => undefined,
    now: clock.now,
    sleepMs: clock.sleepMs,
    terminalTimeoutMs: 5000,
  });
  assert.equal(result.delivered, true);
  assert.equal(result.sent, true);
  assert.equal(result.text, "hi");
  assert.equal(result.sendToFirstTokenMs, 25);
});

test("Desktop send is wired to the standing WS live path with latency context", () => {
  const harness = readSource("web/desktop/src/wrapper/harnessChat.ts");
  for (const needle of [
    "openChatThreadStream",
    "streamStandingChatTurn",
    "useStandingStream",
    "setTurnContext",
    "markError",
    "desktop-chat",
  ]) {
    assert.ok(harness.includes(needle), `harnessChat.ts should reference ${needle}`);
  }
  const latency = readSource("web/desktop/src/wrapper/chatLatency.ts");
  for (const needle of ["sendToFirstTokenMs", "threadId", "sessionId", "path", "setTurnContext", "markError"]) {
    assert.ok(latency.includes(needle), `chatLatency.ts should reference ${needle}`);
  }
  const stream = readSource("web/desktop/src/wrapper/standingChatTurn.ts");
  for (const needle of ["openChatThreadStream", "snapshot", "terminal", "requestId"]) {
    assert.ok(stream.includes(needle), `standingChatTurn.ts should reference ${needle}`);
  }
});
