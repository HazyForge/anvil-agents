#!/usr/bin/env node
/**
 * Desktop Jev manager-create affordance checks (Wrapper/manager-only).
 * Run: node --experimental-strip-types --test hack/desktop-jev-manager-create.mjs
 *
 * The API turn path marks classified, non-unclear create_agent_request
 * turns with user-message `jevNeedsManagerCreate: true` plus the
 * `control.anvil.hazyforge.io/jev-needs-manager-create=true` AgentRun
 * annotation (see docs/jev-intent-routing.md). EntityChatPage surfaces a
 * manager-facing "Requested create" receipt that prefills the existing
 * CreateAgentPanel; peers keep the read-only jevIntent caption only.
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  JEV_NEEDS_MANAGER_CREATE_ANNOTATION,
  JEV_NEEDS_MANAGER_CREATE_METADATA_KEY,
  latestManagerCreateMessage,
  mayShowCreateAffordance,
  messageNeedsManagerCreate,
  runNeedsManagerCreate,
  suggestCreateAgentInput,
} from "../web/desktop/src/wrapper/jevManagerCreate.ts";

test("metadata key and annotation match the API turn path", () => {
  assert.equal(JEV_NEEDS_MANAGER_CREATE_METADATA_KEY, "jevNeedsManagerCreate");
  assert.equal(JEV_NEEDS_MANAGER_CREATE_ANNOTATION, "control.anvil.hazyforge.io/jev-needs-manager-create");
});

test("message flag detection is strict boolean true only", () => {
  assert.equal(messageNeedsManagerCreate({ metadata: { jevNeedsManagerCreate: true } }), true);
  assert.equal(messageNeedsManagerCreate({ metadata: {} }), false);
  assert.equal(messageNeedsManagerCreate({ metadata: { jevNeedsManagerCreate: false } }), false);
  assert.equal(messageNeedsManagerCreate({ metadata: { jevNeedsManagerCreate: "true" } }), false);
  assert.equal(messageNeedsManagerCreate({ metadata: null }), false);
  assert.equal(messageNeedsManagerCreate({}), false);
  assert.equal(messageNeedsManagerCreate(null), false);
  assert.equal(messageNeedsManagerCreate(undefined), false);
});

test("run annotation detection is exact string true only", () => {
  assert.equal(
    runNeedsManagerCreate({ [JEV_NEEDS_MANAGER_CREATE_ANNOTATION]: "true" }),
    true,
  );
  assert.equal(runNeedsManagerCreate({}), false);
  assert.equal(
    runNeedsManagerCreate({ [JEV_NEEDS_MANAGER_CREATE_ANNOTATION]: "True" }),
    false,
  );
  assert.equal(runNeedsManagerCreate(null), false);
  assert.equal(runNeedsManagerCreate(undefined), false);
});

test("latest flagged message wins", () => {
  const flagged = (id) => ({ id, content: id, metadata: { jevNeedsManagerCreate: true } });
  const plain = (id) => ({ id, content: id, metadata: {} });
  const messages = [flagged("u1"), plain("u2"), flagged("u3")];
  assert.equal(latestManagerCreateMessage(messages)?.id, "u3");
  assert.equal(latestManagerCreateMessage([plain("u1")]), undefined);
  assert.equal(latestManagerCreateMessage([]), undefined);
  assert.equal(latestManagerCreateMessage(null), undefined);
});

test("affordance shows for Wrapper/manager only, never peers", () => {
  const flagged = { content: "create an agent named scout", metadata: { jevNeedsManagerCreate: true } };
  const unflagged = { content: "create an agent named scout", metadata: { jevIntent: "create_agent_request" } };
  assert.equal(mayShowCreateAffordance(flagged, "anvil-desktop-wrapper"), true);
  assert.equal(mayShowCreateAffordance(flagged, "desktop-manager"), true);
  assert.equal(mayShowCreateAffordance(flagged, "payments-agent-manager"), true);
  assert.equal(mayShowCreateAffordance(flagged, "scout"), false);
  assert.equal(mayShowCreateAffordance(flagged, ""), false);
  assert.equal(mayShowCreateAffordance(flagged, undefined), false);
  // Flag off: even a manager gets caption only, no create button.
  assert.equal(mayShowCreateAffordance(unflagged, "desktop-manager"), false);
  assert.equal(mayShowCreateAffordance(null, "desktop-manager"), false);
});

test("prefill parses an explicit name, never invents one", () => {
  const parsed = suggestCreateAgentInput("create an agent named scout that reviews pull requests");
  assert.equal(parsed?.name, "scout");
  assert.ok((parsed?.description || "").length > 0);
  assert.equal(suggestCreateAgentInput("how do I create agents?"), null);
  assert.equal(suggestCreateAgentInput(""), null);
  assert.equal(suggestCreateAgentInput("   "), null);
});
