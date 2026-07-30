import assert from "node:assert/strict";
import test from "node:test";
import {
  buildMCPServerSpec,
  buildSkillSpec,
  buildToolSpec,
  formFromMCPServerSpec,
  formFromSkillSpec,
  formFromToolSpec,
} from "../src/pages/library/capabilityForms.ts";
import { buildSkillSetSpec, formFromSkillSetSpec } from "../src/pages/library/skillSetForm.ts";
import { buildToolSetSpec, formFromToolSetSpec } from "../src/pages/library/toolSetForm.ts";

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

test("set forms replace ordered refs and preserve deprecated compatibility fields", () => {
  const legacySkill = { description: "old", skills: [{ name: "inline", content: "keep" }], tools: [{ name: "legacy-tool", verifyCommand: ["true"] }], subagents: [{ name: "reviewer" }], skillRefs: [{ name: "one" }] };
  const skillForm = formFromSkillSetSpec(legacySkill, "review");
  skillForm.skillRefs = ["two", "one"];
  assert.deepEqual(buildSkillSetSpec(skillForm), { ...legacySkill, skillRefs: [{ name: "two" }, { name: "one" }] });

  const legacyTool = { description: "old", tools: [{ name: "inline", setupScript: "true", verifyCommand: ["true"] }], toolRefs: [{ name: "one" }] };
  const toolForm = formFromToolSetSpec(legacyTool, "tools");
  toolForm.toolRefs = ["two", "one"];
  assert.deepEqual(buildToolSetSpec(toolForm), { ...legacyTool, toolRefs: [{ name: "two" }, { name: "one" }] });
});
