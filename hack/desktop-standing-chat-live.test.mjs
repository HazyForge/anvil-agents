/**
 * Unit tests for hack/desktop-standing-chat-live.mjs (no cluster, no OIDC).
 *
 * Run: node --experimental-strip-types --test hack/desktop-standing-chat-live.test.mjs
 */
import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";

import {
  apiErrorCode,
  apiURL,
  argValue,
  classifyDenial,
  isAppendAccepted,
  LIVE_SOURCE_DEFAULT,
  loadProbeToken,
  newRequestId,
  parseProbeArgs,
  redactToken,
  settleReply,
  snapshotStanding,
  wsURL,
  ACCESS_TOKEN_ENV,
} from "./desktop-standing-chat-live.mjs";

// ---- unit tests (no cluster, no OIDC) ----

test("live probe defaults to the signed-in source and Kind-local origin", () => {
  const opts = parseProbeArgs([]);
  assert.equal(opts.live, false);
  assert.equal(opts.source, "desktop-chat-live-signed-in");
  assert.equal(opts.apiOrigin, "http://127.0.0.1:18080");
  assert.equal(opts.namespace, "agents");
  assert.equal(opts.message, "reply briefly: hi");
  assert.ok(opts.out.endsWith(join(".runtime", "chat-latency.jsonl")));
});

test("loadProbeToken prefers env and never requires argv tokens", () => {
  assert.deepEqual(loadProbeToken({ env: { [ACCESS_TOKEN_ENV]: "  tok-1 " } }), { token: "tok-1" });
  const missing = loadProbeToken({ env: {} });
  assert.ok(missing.error && missing.error.includes(ACCESS_TOKEN_ENV));
  assert.deepEqual(loadProbeToken({ env: {}, tokenFile: "/t", readFile: () => " file-tok \n" }), {
    token: "file-tok",
  });
  assert.ok(loadProbeToken({ env: {}, tokenFile: "/t", readFile: () => "  " }).error);
  assert.ok(loadProbeToken({ env: {}, tokenFile: "/t" }).error);
});

test("API/WS URLs never carry tokens", () => {
  const url = apiURL("http://127.0.0.1:18080/", "/api/v1/namespaces/agents/chat/threads/t1");
  assert.equal(url, "http://127.0.0.1:18080/api/v1/namespaces/agents/chat/threads/t1");
  assert.ok(!url.includes("token"));
  assert.equal(wsURL("http://127.0.0.1:18080", "/x/stream"), "ws://127.0.0.1:18080/x/stream");
  assert.equal(wsURL("https://agents.example.com", "/x/stream"), "wss://agents.example.com/x/stream");
  assert.throws(() => apiURL("ftp://x", "/x"), /http\(s\)/);
  assert.throws(() => apiURL("http://x", "no-slash"), /start with \//);
});

test("classifyDenial maps stub-OIDC 401 and verifier 503 to skip", () => {
  assert.deepEqual(classifyDenial({ status: 401 }).outcome, "skip");
  assert.match(classifyDenial({ status: 401 }).reason, /stub-oidc-denied/);
  assert.deepEqual(classifyDenial({ status: 503, code: "authentication_unavailable" }).outcome, "skip");
  assert.equal(classifyDenial({ status: 503, code: "other" }).outcome, "fail");
  assert.equal(classifyDenial({ status: 403 }).outcome, "fail");
  assert.equal(classifyDenial({ status: 404 }).outcome, "fail");
  assert.equal(apiErrorCode({ error: { code: "authentication_unavailable" } }), "authentication_unavailable");
  assert.equal(apiErrorCode(undefined), "");
});

test("snapshotStanding reads the resumed session and rejects the Job plane", () => {
  assert.deepEqual(
    snapshotStanding({ type: "snapshot", standing: { sessionName: "standing-t1", harness: "codex" } }),
    { sessionName: "standing-t1", harness: "codex" },
  );
  assert.equal(snapshotStanding({ type: "snapshot", messages: [] }), undefined);
  assert.equal(snapshotStanding({ type: "snapshot", standing: {} }), undefined);
  assert.equal(snapshotStanding(null), undefined);
});

test("settleReply prefers the assistant message after our user sequence", () => {
  const messages = [
    { role: "user", sequence: 3, content: "hi" },
    { role: "assistant", sequence: 4, content: "hello back" },
  ];
  assert.equal(settleReply(messages, 3).content, "hello back");
  assert.equal(settleReply(messages, 4), undefined);
  assert.equal(settleReply([{ role: "assistant", content: "late" }], undefined).content, "late");
  assert.equal(settleReply([], 1), undefined);
  assert.equal(settleReply(undefined, 1), undefined);
});

test("redactToken scrubs the bearer from log strings", () => {
  const token = "secret-bearer-value";
  assert.equal(redactToken(`failed: ${token} denied`, token), "failed: [redacted-token] denied");
  assert.equal(redactToken("no token here", token), "no token here");
  assert.equal(redactToken(undefined, token), "");
});

test("append accepts 200/201/202 (chat append returns 202 Accepted)", () => {
  assert.equal(isAppendAccepted(200), true);
  assert.equal(isAppendAccepted(201), true);
  assert.equal(isAppendAccepted(202), true);
  assert.equal(isAppendAccepted(404), false);
  assert.equal(isAppendAccepted(401), false);
  const decision = classifyDenial({ status: 404, code: "runs_create_disabled" });
  assert.equal(decision.outcome, "fail");
  assert.match(decision.reason, /not found/);
});

test("newRequestId emits UUIDs for the frozen-turn idempotency key", () => {
  assert.match(newRequestId(), /^[0-9a-f-]{36}$/);
});

