import type { CompositionDocument, CompositionKindName } from "../api/types.composition";

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? (value as Record<string, unknown>) : {};
}

function refNames(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => {
      if (typeof item === "object" && item && "name" in item) {
        return String((item as { name?: string }).name ?? "").trim();
      }
      return "";
    })
    .filter(Boolean);
}

/** One-line summary for a composition CR card. */
export function compositionCardSummary(doc: CompositionDocument): string {
  const kind = doc.kind as CompositionKindName;
  const spec = doc.spec ?? {};

  switch (kind) {
    case "AgentSkill":
      return spec.github ? `github:${String(asRecord(spec.github).repository ?? "")}` : "inline Markdown";
    case "AgentTool": {
      const source = asRecord(spec.source);
      const acquisition = source.httpArtifact ? "https" : source.ociArtifact ? "oci" : source.inlineScript ? "inline script" : "setup script";
      return `${String(asRecord(spec.executable).name ?? "tool")} · ${acquisition}`;
    }
    case "AgentMCPServer":
      return asRecord(spec.transport).streamableHTTP ? "Streamable HTTP" : "stdio";
    case "AgentMCPSet": {
      const servers = Array.isArray(spec.serverRefs) ? spec.serverRefs.length : 0;
      return servers ? `${servers} MCP server${servers === 1 ? "" : "s"}` : "MCP set";
    }
    case "AgentRunProfile": {
      const harness =
        typeof spec.harnessProfileRef === "object" && spec.harnessProfileRef
          ? String((spec.harnessProfileRef as { name?: string }).name ?? "")
          : "";
      const skills = refNames(asRecord(spec.skillSets).refs);
      const tools = refNames(asRecord(spec.toolSets).refs);
      const intent = String(asRecord(spec.harness).intent ?? "");
      const capabilities = asRecord(spec.capabilities);
      const canonicalSkills = Array.isArray(asRecord(capabilities.skills).selections) ? (asRecord(capabilities.skills).selections as unknown[]).length : 0;
      const canonicalTools = Array.isArray(asRecord(capabilities.tools).selections) ? (asRecord(capabilities.tools).selections as unknown[]).length : 0;
      const canonicalMCPs = Array.isArray(asRecord(capabilities.mcpServers).selections) ? (asRecord(capabilities.mcpServers).selections as unknown[]).length : 0;
      return (
        [
          harness ? `harness:${harness}` : null,
          canonicalSkills ? `skills:${canonicalSkills}` : null,
          canonicalTools ? `tools:${canonicalTools}` : null,
          canonicalMCPs ? `mcp:${canonicalMCPs}` : null,
          !canonicalSkills && skills.length ? `legacy-skills:${skills.length}` : null,
          !canonicalTools && tools.length ? `legacy-tools:${tools.length}` : null,
          intent ? `intent:${intent}` : null,
        ]
          .filter(Boolean)
          .join(" · ") || "run profile"
      );
    }
    case "AgentHarnessProfile": {
      const backend = asRecord(spec.backend);
      const kindName = String(backend.kind ?? "backend");
      const sa = String(asRecord(spec.execution).serviceAccountName ?? "");
      const image = String(backend.image ?? "");
      return (
        [
          kindName,
          sa ? `sa:${sa}` : null,
          image ? "custom-image" : "default-image",
        ]
          .filter(Boolean)
          .join(" · ") || "harness"
      );
    }
    case "AgentSkillSet": {
      const refs = Array.isArray(spec.skillRefs) ? spec.skillRefs.length : 0;
      const legacy = Array.isArray(spec.skills) ? spec.skills.length : 0;
      return refs ? `${refs} atomic skill${refs === 1 ? "" : "s"}` : legacy ? `${legacy} legacy skill${legacy === 1 ? "" : "s"}` : "skill set";
    }
    case "AgentToolSet": {
      const refs = Array.isArray(spec.toolRefs) ? spec.toolRefs.length : 0;
      const legacy = Array.isArray(spec.tools) ? spec.tools.length : 0;
      return refs ? `${refs} atomic tool${refs === 1 ? "" : "s"}` : legacy ? `${legacy} legacy tool${legacy === 1 ? "" : "s"}` : "tool set";
    }
    case "VolumeProfile": {
      const volumes = Array.isArray(spec.volumes) ? spec.volumes.length : 0;
      return volumes ? `${volumes} volume shape${volumes === 1 ? "" : "s"}` : "volume profile";
    }
    case "AgentDataVolume": {
      const agent = String(spec.agentName ?? "");
      const backend = String(spec.backend ?? "");
      const size = String(spec.size ?? "");
      return (
        [agent || null, backend || null, size || null].filter(Boolean).join(" · ") || "data volume"
      );
    }
    default:
      return String(spec.description ?? "").trim() || kind;
  }
}
