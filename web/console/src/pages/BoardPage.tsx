import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  APIError,
  getUIConfig,
  listAgentRunArchives,
  listAgentRuns,
  purgeAgentRuns,
} from "../api/client";
import type { AgentRunArchiveItem, AgentRunView } from "../api/types";
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
  const [purging, setPurging] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [refreshedAt, setRefreshedAt] = useState<string>("");
  const [purgeEnabled, setPurgeEnabled] = useState(false);
  const [archiveStoreAvailable, setArchiveStoreAvailable] = useState(false);
  const [archives, setArchives] = useState<AgentRunArchiveItem[]>([]);
  const [showArchives, setShowArchives] = useState(false);
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

  useEffect(() => {
    const controller = new AbortController();
    void (async () => {
      try {
        const cfg = await getUIConfig(controller.signal);
        if (controller.signal.aborted) {
          return;
        }
        setPurgeEnabled(Boolean(cfg.runs?.purgeEnabled));
        setArchiveStoreAvailable(Boolean(cfg.runs?.archiveStoreAvailable));
      } catch {
        if (!controller.signal.aborted) {
          setPurgeEnabled(false);
          setArchiveStoreAvailable(false);
        }
      }
    })();
    return () => controller.abort();
  }, []);

  const onPurge = useCallback(
    async (dryRun: boolean) => {
      if (!namespace || !purgeEnabled) {
        return;
      }
      if (!dryRun) {
        const ok = window.confirm(
          `Purge older terminal AgentRuns in ${namespace}?\n\n` +
            "Only runs already archived to PostgreSQL are deleted from the live board.\n" +
            "History remains in the prod archive database.",
        );
        if (!ok) {
          return;
        }
      }
      setPurging(true);
      setError(null);
      setNotice(null);
      try {
        const result = await purgeAgentRuns(token, namespace, {
          keepLatest: 20,
          keepPerSchedule: 3,
          dryRun,
        });
        const skipped = result.skipped?.length ?? 0;
        setNotice(
          `${dryRun ? "Dry-run" : "Purged"}: would-delete/deleted ${result.deleted.length}, kept ${result.kept}, skipped ${skipped}` +
            (result.archiveStoreAvailable ? " · postgres archive verified" : " · status.archive only"),
        );
        if (!dryRun) {
          await load("refresh");
        }
      } catch (err) {
        if (err instanceof APIError) {
          setError(`${err.code}: ${err.message}`);
        } else {
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        setPurging(false);
      }
    },
    [token, namespace, purgeEnabled, load],
  );

  const onLoadArchives = useCallback(async () => {
    if (!namespace || !archiveStoreAvailable) {
      return;
    }
    setError(null);
    try {
      const items = await listAgentRunArchives(token, namespace, 50);
      setArchives(items);
      setShowArchives(true);
    } catch (err) {
      if (err instanceof APIError) {
        setError(`${err.code}: ${err.message}`);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  }, [token, namespace, archiveStoreAvailable]);

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
            disabled={initialLoading || refreshing || purging}
          >
            {initialLoading ? "Loading…" : refreshing ? "Refreshing…" : "Refresh"}
          </button>
          {purgeEnabled ? (
            <>
              <button
                type="button"
                className="btn"
                onClick={() => void onPurge(true)}
                disabled={initialLoading || purging || !namespace}
                title="Preview which archived terminal runs would leave the live board"
              >
                {purging ? "Working…" : "Dry-run purge"}
              </button>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => void onPurge(false)}
                disabled={initialLoading || purging || !namespace}
                title="Delete older terminal live AgentRuns already stored in PostgreSQL"
              >
                Archive old runs
              </button>
            </>
          ) : null}
          {archiveStoreAvailable ? (
            <button
              type="button"
              className="btn"
              onClick={() => void onLoadArchives()}
              disabled={!namespace}
              title="List historical rows from the prod PostgreSQL archive"
            >
              {showArchives ? "Reload archives" : "Show archives"}
            </button>
          ) : null}
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
      {notice ? <div className="banner banner-ok">{notice}</div> : null}
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
      {showArchives ? (
        <div className="panel" style={{ marginTop: "1rem" }}>
          <div className="panel-header">
            <div>
              <h3 className="panel-title">PostgreSQL archives</h3>
              <div className="panel-subtitle mono">
                {namespace} · anvilhub_agent_run_archives
              </div>
            </div>
            <button type="button" className="btn" onClick={() => setShowArchives(false)}>
              Hide
            </button>
          </div>
          {archives.length === 0 ? (
            <div className="panel-body empty">No archived rows returned.</div>
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Completed</th>
                    <th>Name</th>
                    <th>Phase</th>
                    <th>Schedule</th>
                    <th>Error</th>
                  </tr>
                </thead>
                <tbody>
                  {archives.map((row) => (
                    <tr key={`${row.uid}-${row.name}`}>
                      <td className="mono">
                        {row.completedAt
                          ? new Date(row.completedAt).toLocaleString()
                          : new Date(row.archivedAt).toLocaleString()}
                      </td>
                      <td className="mono">{row.name}</td>
                      <td>{row.phase}</td>
                      <td className="mono">{row.scheduleName || row.sourceName || "—"}</td>
                      <td>{row.error || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}
