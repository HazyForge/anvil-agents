#!/usr/bin/env node
/**
 * Stub/API-first: exercise createChatLatencyTracker + POST /local/v1/chat-latency
 * on a running anvil-desktop host. Emits source=desktop-chat-api-harness (not UI).
 *
 * Usage:
 *   node --experimental-strip-types hack/desktop-chat-latency-host-harness.mjs
 *   ANVIL_DESKTOP_ORIGIN=http://127.0.0.1:1738 node --experimental-strip-types hack/desktop-chat-latency-host-harness.mjs
 */
import {
  createChatLatencyTracker,
  formatChatLatencyJsonLine,
  CHAT_LATENCY_ENDPOINT,
} from "../web/desktop/src/wrapper/chatLatency.ts";

const origin = (process.env.ANVIL_DESKTOP_ORIGIN || "http://127.0.0.1:1738").replace(/\/$/, "");

let t = 1_000_000;
const now = () => t;
const tracker = createChatLatencyTracker(now);
tracker.onStatus("waiting");
t += 15;
tracker.onDelta("H");
t += 405;
tracker.onStatus("running");
t += 60;
tracker.markReplyReady();
t += 1620;
const report = tracker.report();
const line = formatChatLatencyJsonLine(report, {
  source: "desktop-chat-api-harness",
  now: () => Date.now(),
}).trim();

const res = await fetch(`${origin}${CHAT_LATENCY_ENDPOINT}`, {
  method: "POST",
  headers: { "content-type": "application/json" },
  body: line,
});
if (!res.ok && res.status !== 204) {
  console.error("POST failed", res.status, await res.text());
  process.exit(1);
}
console.log(JSON.stringify({ ok: true, status: res.status, line: JSON.parse(line) }));
