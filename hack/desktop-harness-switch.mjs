#!/usr/bin/env node
/**
 * Desktop harness-switch checks for the cluster harness picker.
 * Run: node --experimental-strip-types --test hack/desktop-harness-switch.mjs
 *
 * Covers the pure switcher helpers in web/desktop/src/api/client.ts:
 * backend-neutral model labels (no Codex-only assumptions) and the narrow
 * PATCH body/path used to switch AgentRunProfile.spec.harnessProfileRef.
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  backendKindFromComposition,
  harnessModelFromComposition,
  harnessOptionLabel,
  harnessRefFromRunProfile,
  patchRunProfileHarness,
} from "../web/desktop/src/api/client.ts";

const doc = (name, spec) => ({
  kind: "AgentHarnessProfile",
  metadata: { name, namespace: "agents" },
  spec,
});

test("harness model resolves across every backend envelope", () => {
  const cases = [
    [{ backend: { kind: "codex", codex: { model: "gpt-5.5" } } }, "gpt-5.5"],
    [{ backend: { kind: "openCode", openCode: { model: "openai/gpt-5.4" } } }, "openai/gpt-5.4"],
    [{ backend: { kind: "hermesAgent", hermesAgent: { model: "hermes-x" } } }, "hermes-x"],
    [{ backend: { kind: "openClaw", openClaw: { model: "openai/gpt-5.5" } } }, "openai/gpt-5.5"],
    [{ backend: { kind: "grokBuild", grokBuild: { model: "grok-4.5" } } }, "grok-4.5"],
    [{ backend: { kind: "piAgent", piAgent: { model: "grok-composer-2.5-fast" } } }, "grok-composer-2.5-fast"],
    [{ backend: { kind: "primeAgent", primeAgent: { model: "prime-1" } } }, "prime-1"],
    [{ backend: { kind: "agy", agy: { model: "gemini-3.8-flash" } } }, "gemini-3.8-flash"],
    // Legacy inline harness envelope nests the backend one level deeper.
    [{ harness: { backend: { kind: "piAgent", piAgent: { model: "pi-model" } } } }, "pi-model"],
    [{ backend: { kind: "codex" } }, ""],
    [{}, ""],
  ];
  for (const [spec, want] of cases) {
    assert.equal(harnessModelFromComposition(doc("h", spec)), want, JSON.stringify(spec));
  }
});

test("harness option labels never assume a Codex-only fleet", () => {
  assert.equal(
    harnessOptionLabel(doc("pi-large", { backend: { kind: "piAgent", piAgent: { model: "m" } } })),
    "pi-large (piAgent · m)",
  );
  assert.equal(
    harnessOptionLabel(doc("grok-build", { backend: { kind: "grokBuild" } })),
    "grok-build (grokBuild)",
  );
  assert.equal(harnessOptionLabel(doc("custom-run", { backend: {} })), "custom-run (custom)");
});

test("run-profile harness ref and backend kind read without rewriting inline fields", () => {
  const profile = {
    kind: "AgentRunProfile",
    metadata: { name: "hazy-trade-agent-manager", namespace: "agents" },
    spec: {
      harness: { intent: "observe", backend: { kind: "codex" } },
      harnessProfileRef: { name: "pi-large" },
    },
  };
  assert.equal(harnessRefFromRunProfile(profile), "pi-large");
  assert.equal(backendKindFromComposition(profile), "codex");
  assert.equal(harnessRefFromRunProfile({ kind: "AgentRunProfile", metadata: { name: "x", namespace: "a" }, spec: {} }), "");
});

test("harness switch PATCHes only the harness ref path", async () => {
  const calls = [];
  const updated = doc("scout", { harnessProfileRef: { name: "pi-large" } });
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return { ok: true, json: async () => updated };
  };
  try {
    const got = await patchRunProfileHarness("token", "agents", "hazy-trade-agent-manager", "  pi-large  ");
    assert.equal(got, updated);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url, "/api/v1/namespaces/agents/agent-run-profiles/hazy-trade-agent-manager/harness");
    assert.equal(calls[0].init.method, "PATCH");
    assert.deepEqual(JSON.parse(calls[0].init.body), { harnessProfileName: "pi-large" });
    const headers = calls[0].init.headers;
    const authorization = typeof headers?.get === "function" ? headers.get("Authorization") : headers?.Authorization;
    assert.equal(authorization, "Bearer token");
  } finally {
    // @ts-expect-error restore the pre-test fetch
    delete globalThis.fetch;
  }
});

test("harness switch surfaces API errors", async () => {
  globalThis.fetch = async () => ({
    ok: false,
    status: 403,
    statusText: "Forbidden",
    json: async () => ({ error: { code: "not_console_managed", message: "read-only" } }),
  });
  try {
    await assert.rejects(() => patchRunProfileHarness("token", "agents", "scout", "pi-large"), /read-only/);
  } finally {
    // @ts-expect-error restore the pre-test fetch
    delete globalThis.fetch;
  }
});
