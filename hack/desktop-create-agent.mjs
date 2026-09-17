#!/usr/bin/env node
/**
 * Desktop create-agent skill/tool checks (Wrapper/manager allowlist + peer request).
 * Run: node --experimental-strip-types --test hack/desktop-create-agent.mjs
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import { extractChipsFromPayload } from "../web/desktop/src/api/chatParse.ts";
import {
  CREATE_AGENT_PEER_REFUSAL,
  CREATE_AGENT_TOOL,
  createAgentChips,
  createAgentRequestFromPeer,
  isCreateAgentPrincipal,
  isManagerPrincipal,
  isWrapperPrincipal,
  parseCreateAgentFromStatusBodies,
  parseCreateAgentIntent,
  parseCreateAgentRequestFromLogLine,
  parseCreateAgentToolCalls,
  peerIsCreateAgentRequest,
  requestedCreateChips,
} from "../web/desktop/src/wrapper/createAgentPolicy.ts";
import { parseRequestPeerFromLogLine, STATUS_JSON_PREFIX } from "../web/desktop/src/wrapper/requestPeer.ts";
import { WRAPPER_PROFILE_NAME } from "../web/desktop/src/wrapper/intent.ts";

test("create-agent tool name is baked in", () => {
  assert.equal(CREATE_AGENT_TOOL, "create-agent");
});

test("only wrapper and manager principals may create-agent", () => {
  assert.equal(isWrapperPrincipal(WRAPPER_PROFILE_NAME), true);
  assert.equal(isCreateAgentPrincipal(WRAPPER_PROFILE_NAME), true);
  assert.equal(isManagerPrincipal("desktop-manager"), true);
  assert.equal(isCreateAgentPrincipal("hazy-trade-agent-manager"), true);
  assert.equal(isCreateAgentPrincipal("payments-agent-manager"), true);
  assert.equal(isCreateAgentPrincipal("coord", { "control.anvil.hazyforge.io/role": "manager" }), true);
  assert.equal(isCreateAgentPrincipal("special", { "control.anvil.hazyforge.io/create-agent": "allow" }), true);
  assert.equal(isCreateAgentPrincipal("scout"), false);
  assert.equal(isCreateAgentPrincipal("release-manager"), false);
  assert.equal(
    isCreateAgentPrincipal("reviewer", { "control.anvil.hazyforge.io/chat-role": "project-manager" }),
    false,
  );
});

test("Grok Bot-style named create parses name and description", () => {
  const parsed = parseCreateAgentIntent("create an agent named scout that reviews pull requests");
  assert.equal(parsed.length, 1);
  assert.equal(parsed[0].name, "scout");
  assert.equal(parsed[0].description, "reviews pull requests");
});

test("Chat does not invent names from a vague create-agents question", () => {
  assert.deepEqual(parseCreateAgentIntent("how do I create agents?"), []);
});

test("Wrapper may generate names when asked to create agents", () => {
  const parsed = parseCreateAgentIntent("create two agents", [], { generateNames: true });
  assert.equal(parsed.length, 2);
  assert.ok(parsed.every((item) => item.name));
});

test("create-agent tool calls parse name and optional harness", () => {
  const parsed = parseCreateAgentToolCalls({
    type: "tool_call",
    name: "create-agent",
    arguments: { name: "cartographer", description: "Maps the repo", harnessProfileName: "grok-4-5" },
  });
  assert.equal(parsed[0]?.name, "cartographer");
  assert.equal(parsed[0]?.harnessProfileName, "grok-4-5");
});

test("peer requestPeer create-agent is a request, not a create", () => {
  const line = `${STATUS_JSON_PREFIX}${JSON.stringify({
    type: "decision",
    action: "requestPeer",
    request: "create-agent",
    name: "scout",
    description: "Reviews pull requests",
    peerProfileName: "desktop-manager",
  })}`;
  const peer = parseRequestPeerFromLogLine(line);
  assert.equal(peerIsCreateAgentRequest(peer), true);
  const request = parseCreateAgentRequestFromLogLine(line);
  assert.equal(request?.name, "scout");
  assert.equal(request?.peerProfileName, "desktop-manager");
  const { creates, requests } = parseCreateAgentFromStatusBodies(line);
  assert.equal(creates.length, 0);
  assert.equal(requests.length, 1);
  const chips = requestedCreateChips(request);
  assert.ok(chips.some((chip) => chip.label === "Requested create: scout"));
});

test("manager STATUS_JSON create-agent is a create, not a peer request", () => {
  const line = `${STATUS_JSON_PREFIX}${JSON.stringify({
    type: "decision",
    action: "create-agent",
    name: "navigator",
    description: "Finds the next task",
  })}`;
  assert.equal(parseCreateAgentRequestFromLogLine(line), null);
  const { creates, requests } = parseCreateAgentFromStatusBodies(line);
  assert.equal(requests.length, 0);
  assert.equal(creates[0]?.name, "navigator");
});

test("createAgentRequestFromPeer ignores ordinary grok requestPeer", () => {
  const peer = parseRequestPeerFromLogLine(
    `${STATUS_JSON_PREFIX}${JSON.stringify({
      type: "decision",
      action: "requestPeer",
      peerProfileName: "hazy-trade-desktop-grok-peer",
    })}`,
  );
  assert.equal(createAgentRequestFromPeer(peer), null);
});

test("refused create-agent is a visible receipt, not a silent no-op", () => {
  const chips = createAgentChips({
    ok: false,
    refused: true,
    reason: CREATE_AGENT_PEER_REFUSAL,
    principal: "scout",
    input: { name: "cartographer" },
  });
  assert.ok(chips.some((chip) => chip.label === "Refused create: cartographer"));
  assert.ok(chips.some((chip) => chip.status === "failed"));
});

test("create-agent chips from a manager tool call", () => {
  const chips = extractChipsFromPayload({
    type: "tool_call",
    name: "create-agent",
    arguments: { name: "scout", description: "Reviews PRs" },
  });
  const labels = chips.map((chip) => chip.label);
  assert.ok(labels.includes("create-agent"));
  assert.ok(labels.includes("Created: scout"));
});

test("requestPeer chips include Requested create when request=create-agent", () => {
  const chips = extractChipsFromPayload({
    type: "tool_call",
    name: "requestPeer",
    arguments: {
      peerProfileName: "desktop-manager",
      request: "create-agent",
      name: "scout",
    },
  });
  const labels = chips.map((chip) => chip.label);
  assert.ok(labels.includes("Routing: requestPeer"));
  assert.ok(labels.includes("To: desktop-manager"));
  assert.ok(labels.includes("Requested create: scout"));
});
