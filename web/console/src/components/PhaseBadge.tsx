import type { AgentRunPhase } from "../api/types";

/** Phase as coloured state: a lit LED (pulsing while Running) plus the phase name. */
export function PhaseBadge({ phase }: { phase?: AgentRunPhase }) {
  const value = phase?.trim() || "Unknown";
  const cls = `phase phase-${phaseKey(value)}`;
  return <span className={cls}>{value}</span>;
}

/** CSS-safe phase key shared by badges, card tones and stat tiles. */
export function phaseKey(phase?: string): string {
  return (phase ?? "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "") || "unknown";
}
