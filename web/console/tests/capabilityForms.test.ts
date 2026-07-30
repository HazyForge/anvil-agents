import assert from "node:assert/strict";
import test from "node:test";
import {
  buildMCPServerSpec,
  buildMCPSetSpec,
  buildSkillSpec,
  buildToolSpec,
  formFromMCPServerSpec,
  formFromMCPSetSpec,
  formFromSkillSpec,
  formFromToolSpec,
  previewGitHubSkillPackage,
} from "../src/pages/library/capabilityForms.ts";
import { buildSkillSetSpec, formFromSkillSetSpec } from "../src/pages/library/skillSetForm.ts";
import { buildToolSetSpec, formFromToolSetSpec } from "../src/pages/library/toolSetForm.ts";
import { preserveCapabilitySelections, preserveNamedReference, preserveOrderedNamedReferences } from "../src/pages/profiles/profileReferenceMerge.ts";

test("atomic capability forms round-trip every source and transport shape", () => {
  const skill = { description: "review", github: { repository: "HazyForge/skills", ref: "a".repeat(40), path: "review/SKILL.md", referencePaths: ["review/references/checks.md"] } };
  assert.deepEqual(buildSkillSpec(formFromSkillSpec(skill, "review")), skill);

  const tool = { description: "query", executable: { name: "query", path: "bin/query" }, source: { httpArtifact: { artifacts: [{ platform: { os: "linux", arch: "amd64" }, url: "https://example.test/query", sha256: "b".repeat(64), format: "binary" }] } }, verifyCommand: ["query", "--version"] };
  assert.deepEqual(buildToolSpec(formFromToolSpec(tool, "query")), tool);

  const mcp = { description: "knowledge", transport: { streamableHTTP: { endpoint: "https://mcp.example.test/mcp", headers: [{ name: "Authorization", envVar: "MCP_AUTHORIZATION" }], requiredEnv: ["MCP_AUTHORIZATION"] } }, toolAllowlist: ["search"] };
  const rebuilt = buildMCPServerSpec(formFromMCPServerSpec(mcp, "knowledge"));
  assert.deepEqual(rebuilt, mcp);
  assert.equal(JSON.stringify(rebuilt).includes("secret"), false);
});

test("GitHub skill preview imports Markdown and reports ignored package assets", async () => {
  const form = formFromSkillSpec({ github: { repository: "HazyForge/skills", ref: "a".repeat(40), path: "review/SKILL.md" } }, "review");
  let requested = "";
  const result = await previewGitHubSkillPackage(form, async (input) => {
    requested = input;
    return {
      ok: true,
      status: 200,
      json: async () => ({ tree: [
        { type: "blob", path: "review/SKILL.md" },
        { type: "blob", path: "review/references/checks.md" },
        { type: "blob", path: "review/scripts/check.sh" },
        { type: "blob", path: "unrelated/README.md" },
      ] }),
    };
  });
  assert.match(requested, /\/repos\/HazyForge\/skills\/git\/trees\//);
  assert.deepEqual(result.referencePaths, ["review/references/checks.md"]);
  assert.deepEqual(result.ignoredPaths, ["review/scripts/check.sh"]);
});

test("set forms replace ordered refs and preserve deprecated compatibility fields", () => {
  const legacySkill = { description: "old", skills: [{ name: "inline", content: "keep" }], tools: [{ name: "legacy-tool", verifyCommand: ["true"] }], subagents: [{ name: "reviewer" }], skillRefs: [{ name: "one", namespace: "agents" }] };
  const skillForm = formFromSkillSetSpec(legacySkill, "review");
  skillForm.skillRefs = ["two", "one"];
  assert.deepEqual(buildSkillSetSpec(skillForm), { ...legacySkill, skillRefs: [{ name: "two" }, { name: "one", namespace: "agents" }] });

  const legacyTool = { description: "old", tools: [{ name: "inline", setupScript: "true", verifyCommand: ["true"] }], toolRefs: [{ name: "one", namespace: "agents" }] };
  const toolForm = formFromToolSetSpec(legacyTool, "tools");
  toolForm.toolRefs = ["two", "one"];
  assert.deepEqual(buildToolSetSpec(toolForm), { ...legacyTool, toolRefs: [{ name: "two" }, { name: "one", namespace: "agents" }] });

  const mcpSet = { description: "context", serverRefs: [{ name: "one", namespace: "agents" }] };
  const mcpForm = formFromMCPSetSpec(mcpSet, "context");
  mcpForm.serverRefs = ["two", "one"];
  assert.deepEqual(buildMCPSetSpec(mcpForm), { ...mcpSet, serverRefs: [{ name: "two" }, { name: "one", namespace: "agents" }] });
});

test("profile reference merges preserve explicit namespaces and future metadata", () => {
  assert.deepEqual(preserveNamedReference({ name: "harness", namespace: "agents", future: true }, "harness"), { name: "harness", namespace: "agents", future: true });
  assert.deepEqual(preserveOrderedNamedReferences([{ name: "one", namespace: "agents" }], ["two", "one"]), [{ name: "two" }, { name: "one", namespace: "agents" }]);
  assert.deepEqual(
    preserveCapabilitySelections(
      [{ toolSetRef: { name: "set", namespace: "agents" }, future: "keep" }],
      [{ type: "atomic", name: "tool" }, { type: "set", name: "set" }],
      "toolRef",
      "toolSetRef",
    ),
    [{ toolRef: { name: "tool" } }, { toolSetRef: { name: "set", namespace: "agents" }, future: "keep" }],
  );
});
