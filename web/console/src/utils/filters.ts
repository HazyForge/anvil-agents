import type { AgentRunView } from "../api/types";
import { latestReportSummary, sourceLabel } from "./format";

export interface RunFilters {
  search: string;
  phase: string;
  application: string;
  applicationTarget: string;
  backend: string;
  source: string;
  onlyRunning: boolean;
  onlyFailed: boolean;
}

export const emptyFilters = (): RunFilters => ({
  search: "",
  phase: "",
  application: "",
  applicationTarget: "",
  backend: "",
  source: "",
  onlyRunning: false,
  onlyFailed: false,
});

export function filterRuns(runs: AgentRunView[], filters: RunFilters): AgentRunView[] {
  const search = filters.search.trim().toLowerCase();
  return runs.filter((run) => {
    if (filters.onlyRunning && run.phase !== "Running") {
      return false;
    }
    if (filters.onlyFailed && run.phase !== "Failed") {
      return false;
    }
    if (filters.phase && run.phase !== filters.phase) {
      return false;
    }
    if (filters.application && (run.application ?? "") !== filters.application) {
      return false;
    }
    if (filters.applicationTarget && (run.applicationTarget ?? "") !== filters.applicationTarget) {
      return false;
    }
    if (filters.backend && (run.backend ?? "") !== filters.backend) {
      return false;
    }
    if (filters.source) {
      const label = sourceLabel(run);
      const kindName = `${run.source?.kind ?? ""}/${run.source?.name ?? ""}`;
      if (label !== filters.source && kindName !== filters.source) {
        return false;
      }
    }
    if (!search) {
      return true;
    }
    const haystack = [
      run.name,
      run.namespace,
      run.intent,
      run.error,
      run.application,
      run.applicationTarget,
      run.backend,
      run.phase,
      sourceLabel(run),
      latestReportSummary(run),
      run.pullRequestURL,
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return haystack.includes(search);
  });
}

export function uniqueSorted(values: Array<string | undefined | null>): string[] {
  const set = new Set<string>();
  for (const value of values) {
    const trimmed = value?.trim();
    if (trimmed) {
      set.add(trimmed);
    }
  }
  return [...set].sort((a, b) => a.localeCompare(b));
}
