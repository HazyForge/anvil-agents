#!/usr/bin/env node
/**
 * Desktop Jev peer-handoff affordance checks (Wrapper/manager-only).
 * Run: node --experimental-strip-types --test hack/desktop-jev-peer-handoff.mjs
 *
 * The API turn path marks classified, non-unclear peer_handoff turns with
 * user-message `jevNeedsPeerHandoff: true` plus the
 * `control.anvil.hazyforge.io/jev-needs-peer-handoff=true` AgentRun
 * annotation (see docs/jev-intent-routing.md). EntityChatPage surfaces a
 * Wrapper/manager-facing handoff receipt that deep-links the named
 * existing peer's standing conversation; peers keep the read-only
 * jevIntent caption only. Handoff never creates teammates.
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  handoffStatusTargets,
  JEV_NEEDS_PEER_HANDOFF_ANNOTATION,
  JEV_NEEDS_PEER_HANDOFF_METADATA_KEY,
  latestPeerHandoffMessage,
  mayShowHandoffAffordance,
  messageHasHandoffHint,
  messageNeedsHandoffAttention,
  messageNeedsPeerHandoff,
  peerHandoffTargetFromMessage,
  runNeedsPeerHandoff,
  suggestPeerHandoffTarget,
} from "../web/desktop/src/wrapper/jevPeerHandoff.ts";

test("metadata key and annotation match the API turn path", () => {
  assert.equal(JEV_NEEDS_PEER_HANDOFF_METADATA_KEY, "jevNeedsPeerHandoff");
  assert.equal(JEV_NEEDS_PEER_HANDOFF_ANNOTATION, "control.anvil.hazyforge.io/jev-needs-peer-handoff");
});

test("message flag detection is strict boolean true only", () => {
  assert.equal(messageNeedsPeerHandoff({ metadata: { jevNeedsPeerHandoff: true } }), true);
  assert.equal(messageNeedsPeerHandoff({ metadata: {} }), false);
  assert.equal(messageNeedsPeerHandoff({ metadata: { jevNeedsPeerHandoff: false } }), false);
  assert.equal(messageNeedsPeerHandoff({ metadata: { jevNeedsPeerHandoff: "true" } }), false);
  assert.equal(messageNeedsPeerHandoff({ metadata: null }), false);
  assert.equal(messageNeedsPeerHandoff({}), false);
  assert.equal(messageNeedsPeerHandoff(null), false);
  assert.equal(messageNeedsPeerHandoff(undefined), false);
});

test("run annotation detection is exact string true only", () => {
  assert.equal(
    runNeedsPeerHandoff({ [JEV_NEEDS_PEER_HANDOFF_ANNOTATION]: "true" }),
    true,
  );
  assert.equal(runNeedsPeerHandoff({}), false);
  assert.equal(
    runNeedsPeerHandoff({ [JEV_NEEDS_PEER_HANDOFF_ANNOTATION]: "True" }),
    false,
  );
  assert.equal(runNeedsPeerHandoff(null), false);
  assert.equal(runNeedsPeerHandoff(undefined), false);
});

test("latest flagged message wins", () => {
  const flagged = (id) => ({ id, content: id, metadata: { jevNeedsPeerHandoff: true } });
  const plain = (id) => ({ id, content: id, metadata: {} });
  const messages = [flagged("u1"), plain("u2"), flagged("u3")];
  assert.equal(latestPeerHandoffMessage(messages)?.id, "u3");
  assert.equal(latestPeerHandoffMessage([plain("u1")]), undefined);
  assert.equal(latestPeerHandoffMessage([]), undefined);
  assert.equal(latestPeerHandoffMessage(null), undefined);
});

test("STATUS_JSON handoff hint names the existing peer, never create-agent", () => {
  const handoff =
    'working\nANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","peerProfileName":"scout","summary":"needs research"}';
  const targets = handoffStatusTargets(handoff);
  assert.equal(targets.length, 1);
  assert.equal(targets[0].peerProfileName, "scout");
  assert.equal(targets[0].summary, "needs research");
  assert.equal(messageHasHandoffHint({ content: handoff, metadata: {} }), true);
  assert.deepEqual(peerHandoffTargetFromMessage({ content: handoff, metadata: {} }), {
    peerProfileName: "scout",
    summary: "needs research",
  });

  // Create-agent requests belong to the manager-create affordance, not handoff.
  const createLine =
    'ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","request":"create-agent","name":"scout","peerProfileName":"desktop-manager"}';
  assert.deepEqual(handoffStatusTargets(createLine), []);
  assert.equal(messageHasHandoffHint({ content: createLine, metadata: {} }), false);

  // requestPeer without a named peer is not an actionable handoff hint.
  const unnamed =
    'ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","summary":"help"}';
  assert.deepEqual(handoffStatusTargets(unnamed), []);
  assert.equal(messageHasHandoffHint({ content: "just chatting", metadata: {} }), false);
  assert.equal(messageHasHandoffHint({ content: "", metadata: {} }), false);
  assert.equal(messageHasHandoffHint(null), false);
});

test("flag or STATUS_JSON hint both count as handoff attention", () => {
  const flagged = { content: "hand off to scout", metadata: { jevNeedsPeerHandoff: true } };
  const hintOnly = {
    content:
      'ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","peerProfileName":"scout"}',
    metadata: {},
  };
  const plain = { content: "hello", metadata: { jevIntent: "peer_handoff" } };
  assert.equal(messageNeedsHandoffAttention(flagged), true);
  assert.equal(messageNeedsHandoffAttention(hintOnly), true);
  assert.equal(messageNeedsHandoffAttention(plain), false);
  assert.equal(messageNeedsHandoffAttention(null), false);
});

test("affordance shows for Wrapper/manager only, never peers", () => {
  const flagged = { content: "hand off to scout", metadata: { jevNeedsPeerHandoff: true } };
  const hintOnly = {
    content:
      'ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","peerProfileName":"scout"}',
    metadata: {},
  };
  const unflagged = { content: "hand off to scout", metadata: { jevIntent: "peer_handoff" } };
  assert.equal(mayShowHandoffAffordance(flagged, "anvil-desktop-wrapper"), true);
  assert.equal(mayShowHandoffAffordance(flagged, "desktop-manager"), true);
  assert.equal(mayShowHandoffAffordance(hintOnly, "desktop-manager"), true);
  assert.equal(mayShowHandoffAffordance(flagged, "payments-agent-manager"), true);
  assert.equal(mayShowHandoffAffordance(flagged, "scout"), false);
  assert.equal(mayShowHandoffAffordance(hintOnly, "scout"), false);
  assert.equal(mayShowHandoffAffordance(flagged, ""), false);
  assert.equal(mayShowHandoffAffordance(flagged, undefined), false);
  // Flag off and no STATUS_JSON hint: even a manager gets caption only.
  assert.equal(mayShowHandoffAffordance(unflagged, "desktop-manager"), false);
  assert.equal(mayShowHandoffAffordance(null, "desktop-manager"), false);
});

test("peer suggestion matches the roster only, never invents", () => {
  const roster = ["desktop-manager", "scout", "researcher"];
  assert.equal(suggestPeerHandoffTarget("please hand off to scout for research", roster), "scout");
  assert.equal(suggestPeerHandoffTarget("HAND OFF TO SCOUT", roster), "scout");
  assert.equal(suggestPeerHandoffTarget("hand off to newcomer for research", roster), null);
  assert.equal(suggestPeerHandoffTarget("", roster), null);
  assert.equal(suggestPeerHandoffTarget("hand off to scout", []), null);
  assert.equal(suggestPeerHandoffTarget("hand off to scout", null), null);
});
