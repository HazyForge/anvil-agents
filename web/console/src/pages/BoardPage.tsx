import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { APIError, listAgentRuns } from "../api/client";
import type { AgentRunView } from "../api/types";
import { FiltersBar } from "../components/FiltersBar";
import { RunBoard } from "../components/RunBoard";
import { emptyFilters, filterRuns, uniqueSorted, type RunFilters } from "../utils/filters";
import { sourceLabel } from "../utils/format";

interface Props {
  token: string;
  namespace: string;
}

const LIST_LIMIT = 200;
const POLL_MS = 15_000;

export function BoardPage({ token, namespace }: Props) {
  const [runs, setRuns] = useState<AgentRunView[]>([]);
  const [filters, setFilters] = useState<RunFilters>(emptyFilters);
  const [initialLoading, setInitialLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [refreshedAt, setRefreshedAt] = useState<string>("");
  const requestIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(
    async (mode: "initial" | "refresh" = "refresh") => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      const requestId = ++requestIdRef.current;

      if (!namespace) {
        if (requestId !== requestIdRef.current) {
          return;
        }
        setRuns([]);
        setRefreshedAt("");
        setInitialLoading(false);
        setRefreshing(false);
        setError("Select or add a namespace.");
        return;
      }

      if (mode === "initial") {
        // Drop prior namespace rows immediately so a failed switch cannot leave
        // another namespace's runs under the newly selected active namespace.
        setRuns([]);
        setRefreshedAt("");
        setInitialLoading(true);
      } else {
        setRefreshing(true);
      }
      setError(null);

      try {
        const items = await listAgentRuns(token, namespace, LIST_LIMIT, controller.signal);
        if (requestId !== requestIdRef.current) {
          return;
        }
        setRuns(items);
        setRefreshedAt(new Date().toISOString());
      } catch (err) {
        if (controller.signal.aborted || requestId !== requestIdRef.current) {
          return;
        }
        // Never keep prior results after a failed load for this namespace.
        setRuns([]);
        setRefreshedAt("");
        if (err instanceof APIError) {
          setError(`${err.code}: ${err.message}`);
        } else {
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (requestId === requestIdRef.current) {
          setInitialLoading(false);
          setRefreshing(false);
        }
      }
    },
    [token, namespace],
  );

  useEffect(() => {
    void load("initial");
    const id = window.setInterval(() => {
      void load("refresh");
    }, POLL_MS);
    return () => {
      window.clearInterval(id);
      abortRef.current?.abort();
      requestIdRef.current += 1;
    };
  }, [load]);

  const filtered = useMemo(() => filterRuns(runs, filters), [runs, filters]);
  const contradictoryPhaseFilters = filters.onlyRunning && filters.onlyFailed;

  const facets = useMemo(
    () => ({
      phases: uniqueSorted(runs.map((r) => r.phase)),
      applications: uniqueSorted(runs.map((r) => r.application)),
      targets: uniqueSorted(runs.map((r) => r.applicationTarget)),
      backends: uniqueSorted(runs.map((r) => r.backend)),
      sources: uniqueSorted(runs.map((r) => sourceLabel(r))),
    }),
    [runs],
  );

  return (
    <div className="panel">
      <div className="panel-header">
        <div>
          <h2 className="panel-title">Run board</h2>
          {namespace ? (
            <div className="panel-subtitle mono">
              {namespace} · AgentRun
            </div>
          ) : null}
        </div>
        <div className="toolbar-inline">
          <span className="count-pill">
            {filtered.length}/{runs.length}
            {runs.length >= LIST_LIMIT ? ` (limit ${LIST_LIMIT})` : ""}
          </span>
          {refreshedAt ? (
            <span className="count-pill">refreshed {new Date(refreshedAt).toLocaleTimeString()}</span>
          ) : null}
          <button
            type="button"
            className="btn"
            onClick={() => void load(runs.length === 0 ? "initial" : "refresh")}
            disabled={initialLoading || refreshing}
          >
            {initialLoading ? "Loading…" : refreshing ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </div>
      <FiltersBar
        filters={filters}
        phases={facets.phases}
        applications={facets.applications}
        targets={facets.targets}
        backends={facets.backends}
        sources={facets.sources}
        onChange={setFilters}
        onReset={() => setFilters(emptyFilters())}
      />
      {error ? <div className="banner banner-error">{error}</div> : null}
      {contradictoryPhaseFilters ? (
        <div className="banner banner-warn">
          Only running and only failed are both enabled — no phase can match both.
        </div>
      ) : null}
      <RunBoard
        runs={filtered}
        namespace={namespace}
        emptyHint={
          contradictoryPhaseFilters
            ? "Filters are contradictory (only running + only failed)."
            : undefined
        }
      />
    </div>
  );
}
