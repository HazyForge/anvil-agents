import type { AgentRunReport, AgentRunView } from "../api/types";

export function formatTime(value?: string | null): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function formatDuration(run: AgentRunView): string {
  const start = parseTime(run.startedAt) ?? parseTime(run.createdAt);
  if (!start) {
    return "—";
  }
  const end = parseTime(run.completedAt) ?? (isTerminal(run.phase) ? start : Date.now());
  const ms = Math.max(0, end - start);
  if (ms < 1000) {
    return `${ms}ms`;
  }
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remSec = seconds % 60;
  if (minutes < 60) {
    return remSec ? `${minutes}m ${remSec}s` : `${minutes}m`;
  }
  const hours = Math.floor(minutes / 60);
  const remMin = minutes % 60;
  return remMin ? `${hours}h ${remMin}m` : `${hours}h`;
}

export function isTerminal(phase?: string): boolean {
  return phase === "Succeeded" || phase === "Failed" || phase === "NeedsHuman";
}

export function shortError(error?: string, max = 96): string {
  if (!error) {
    return "";
  }
  const oneLine = error.replace(/\s+/g, " ").trim();
  if (oneLine.length <= max) {
    return oneLine;
  }
  return `${oneLine.slice(0, max - 1)}…`;
}

export function latestReportSummary(run: AgentRunView): string {
  const reports = run.reports;
  if (!reports?.length) {
    return "";
  }
  const sorted = [...reports].sort((a, b) => {
    const at = parseTime(a.observedAt) ?? 0;
    const bt = parseTime(b.observedAt) ?? 0;
    return bt - at;
  });
  return sorted[0]?.summary?.trim() ?? "";
}

export function latestHumanFollowUp(reports?: AgentRunReport[]): string {
  if (!reports?.length) {
    return "";
  }
  const withFollowUp = [...reports]
    .filter((r) => r.humanFollowUp?.trim())
    .sort((a, b) => (parseTime(b.observedAt) ?? 0) - (parseTime(a.observedAt) ?? 0));
  return withFollowUp[0]?.humanFollowUp?.trim() ?? "";
}

export function sourceLabel(run: AgentRunView): string {
  const kind = run.source?.kind?.trim() || "—";
  const name = run.source?.name?.trim() || "—";
  return `${kind}/${name}`;
}

function parseTime(value?: string | null): number | null {
  if (!value) {
    return null;
  }
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    try {
      const area = document.createElement("textarea");
      area.value = text;
      area.style.position = "fixed";
      area.style.left = "-9999px";
      document.body.appendChild(area);
      area.select();
      const ok = document.execCommand("copy");
      document.body.removeChild(area);
      return ok;
    } catch {
      return false;
    }
  }
}
