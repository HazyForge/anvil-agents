import type { AgentRunPhase } from "../api/types";

export function PhaseBadge({ phase }: { phase?: AgentRunPhase }) {
  const value = phase?.trim() || "Unknown";
  const cls = `phase phase-${value.toLowerCase().replace(/[^a-z0-9]+/g, "") || "unknown"}`;
  return <span className={cls}>{value}</span>;
}
