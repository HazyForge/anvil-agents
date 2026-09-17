#!/usr/bin/env node
/**
 * Desktop chat/peer reliability checks for pure parsers.
 * Run: node --experimental-strip-types --test hack/desktop-chat-reliability.mjs
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  assistantAfterUser,
  extractChipsFromPayload,
  normalizeChatMessages,
  peerTargetFromArgs,
  turnStatusFromRaw,
} from "../web/desktop/src/api/chatParse.ts";
import {
  parseRequestPeerFromLogLine,
  peerRunBelongsToSource,
  stickyConferralAction,
  STATUS_JSON_PREFIX,
} from "../web/desktop/src/wrapper/requestPeer.ts";

test("normalizeChatMessages treats null/undefined as empty", () => {
  assert.deepEqual(normalizeChatMessages(null), []);
  assert.deepEqual(normalizeChatMessages(undefined), []);
  assert.deepEqual(normalizeChatMessages([{ id: "1", threadId: "t", role: "user", content: "hi", createdAt: "", sequence: 1 }]).length, 1);
});

test("assistantAfterUser returns the persisted reply after the matching user turn", () => {
  const recovered = assistantAfterUser(
    [
      { id: "u1", threadId: "t", role: "user", content: "hello", createdAt: "", sequence: 1 },
      { id: "a1", threadId: "t", role: "assistant", content: "hi there", createdAt: "", sequence: 2 },
      { id: "u2", threadId: "t", role: "user", content: "hello", createdAt: "", sequence: 3 },
      { id: "a2", threadId: "t", role: "assistant", content: "recovered after restart", createdAt: "", sequence: 4 },
    ],
    "hello",
  );
  assert.equal(recovered?.id, "a2");
  assert.equal(recovered?.content, "recovered after restart");
});

test("requestPeer chips use peerProfileName, not a wrong profile or Steer", () => {
  const chips = extractChipsFromPayload({
    type: "tool_call",
    name: "requestPeer",
    arguments: {
      peerProfileName: "hazy-trade-desktop-grok-peer",
      peerRunName: "desktop-peer-abc",
    },
  });
  const labels = chips.map((chip) => chip.label);
  assert.ok(labels.includes("Routing: requestPeer"));
  assert.ok(labels.includes("To: hazy-trade-desktop-grok-peer"));
  assert.ok(labels.includes("Peer run: desktop-peer-abc"));
  assert.equal(labels.includes("Routing: Steer"), false);
  assert.equal(
    labels.some((label) => label.includes("hazy-trade-production-auditor")),
    false,
  );
});

test("interruptDuplicate chips point at the duplicate run", () => {
  const chips = extractChipsFromPayload({
    type: "function_call",
    name: "interruptDuplicate",
    arguments: { duplicateRunName: "desktop-reqpeer-xyz" },
  });
  const labels = chips.map((chip) => chip.label);
  assert.ok(labels.includes("Routing: interruptDuplicate"));
  assert.ok(labels.includes("To: desktop-reqpeer-xyz"));
});

test("peerTargetFromArgs prefers peerProfileName", () => {
  assert.equal(
    peerTargetFromArgs({ peerProfileName: "intended-peer", peer: "other", runName: "desktop-peer-1" }),
    "intended-peer",
  );
});

test("turnStatusFromRaw maps waiting/queued/running/failed", () => {
  assert.equal(turnStatusFromRaw("Waiting"), "waiting");
  assert.equal(turnStatusFromRaw("queued"), "queued");
  assert.equal(turnStatusFromRaw("Pending"), "queued");
  assert.equal(turnStatusFromRaw("Running"), "running");
  assert.equal(turnStatusFromRaw("Failed"), "failed");
});

test("parseRequestPeerFromLogLine reads peerProfileName from STATUS_JSON", () => {
  const line = `${STATUS_JSON_PREFIX}${JSON.stringify({
    type: "decision",
    action: "requestPeer",
    peerProfileName: "hazy-trade-desktop-grok-peer",
    summary: "Need a grok peer",
  })}`;
  const parsed = parseRequestPeerFromLogLine(line);
  assert.equal(parsed?.peerProfileName, "hazy-trade-desktop-grok-peer");
});

test("peerRunBelongsToSource matches the real source run, not a sibling", () => {
  assert.equal(
    peerRunBelongsToSource(
      { name: "desktop-peer-b", source: { name: "desktop-reqpeer-a", namespace: "hazy-trade" } },
      "desktop-reqpeer-a",
      "hazy-trade",
    ),
    true,
  );
  assert.equal(
    peerRunBelongsToSource(
      { name: "desktop-peer-b", source: { name: "desktop-reqpeer-other", namespace: "hazy-trade" } },
      "desktop-reqpeer-a",
      "hazy-trade",
    ),
    false,
  );
});

test("stickyConferralAction keeps requestPeer from output when CR later says observe", () => {
  const action = stickyConferralAction({
    output: `${STATUS_JSON_PREFIX}{"type":"decision","action":"requestPeer","peerProfileName":"hazy-trade-desktop-grok-peer"}`,
    decision: { action: "observe", summary: "finished" },
  });
  assert.equal(action, "requestPeer");
});
