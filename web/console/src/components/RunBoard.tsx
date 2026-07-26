import { Link } from "react-router-dom";
import type { AgentRunView } from "../api/types";
import {
  formatDuration,
  formatTime,
  latestReportSummary,
  shortError,
  sourceLabel,
} from "../utils/format";
import { PhaseBadge } from "./PhaseBadge";

interface Props {
  runs: AgentRunView[];
  namespace: string;
  emptyHint?: string;
}

export function RunBoard({ runs, namespace, emptyHint }: Props) {
  if (runs.length === 0) {
    return (
      <div className="empty">
        {emptyHint || `No AgentRuns match the current filters in ${namespace}.`}
      </div>
    );
  }

  return (
    <div className="table-wrap">
      <table className="runs">
        <thead>
          <tr>
            <th>Phase</th>
            <th>Name</th>
            <th>Intent</th>
            <th>Backend</th>
            <th>App / Target</th>
            <th>Source</th>
            <th>Created</th>
            <th>Started</th>
            <th>Completed</th>
            <th>Duration</th>
            <th>PR</th>
            <th>Report</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((run) => {
            const report = latestReportSummary(run);
            const error = shortError(run.error);
            return (
              <tr key={`${run.namespace}/${run.name}/${run.uid}`}>
                <td>
                  <PhaseBadge phase={run.phase} />
                </td>
                <td className="mono">
                  <Link to={`/ns/${encodeURIComponent(run.namespace)}/runs/${encodeURIComponent(run.name)}`}>
                    {run.name}
                  </Link>
                </td>
                <td className="intent-cell" title={run.intent}>
                  {run.intent || <span className="muted">—</span>}
                </td>
                <td>{run.backend || "—"}</td>
                <td>
                  <div>{run.application || "—"}</div>
                  <div className="muted">{run.applicationTarget || "—"}</div>
                </td>
                <td className="mono" title={sourceLabel(run)}>
                  {sourceLabel(run)}
                </td>
                <td className="mono muted">{formatTime(run.createdAt)}</td>
                <td className="mono muted">{formatTime(run.startedAt)}</td>
                <td className="mono muted">{formatTime(run.completedAt)}</td>
                <td className="mono">{formatDuration(run)}</td>
                <td>
                  {run.pullRequestURL ? (
                    <a href={run.pullRequestURL} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
                      PR
                    </a>
                  ) : (
                    <span className="muted">—</span>
                  )}
                </td>
                <td className="intent-cell" title={report}>
                  {report || <span className="muted">—</span>}
                </td>
                <td className="error-cell" title={run.error}>
                  {error || <span className="muted">—</span>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
