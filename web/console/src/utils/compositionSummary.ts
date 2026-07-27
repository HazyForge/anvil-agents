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
    case "AgentRunProfile": {
      const harness =
        typeof spec.harnessProfileRef === "object" && spec.harnessProfileRef
          ? String((spec.harnessProfileRef as { name?: string }).name ?? "")
          : "";
      const skills = refNames(asRecord(spec.skillSets).refs);
      const tools = refNames(asRecord(spec.toolSets).refs);
      const intent = String(asRecord(spec.harness).intent ?? "");
      return (
        [
          harness ? `harness:${harness}` : null,
          skills.length ? `skills:${skills.length}` : null,
          tools.length ? `tools:${tools.length}` : null,
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
      const skills = Array.isArray(spec.skills) ? spec.skills.length : 0;
      return skills ? `${skills} skill${skills === 1 ? "" : "s"}` : "skill set";
    }
    case "AgentToolSet": {
      const tools = Array.isArray(spec.tools) ? spec.tools.length : 0;
      return tools ? `${tools} tool${tools === 1 ? "" : "s"}` : "tool set";
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
