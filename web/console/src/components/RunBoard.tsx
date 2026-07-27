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

/** AgentRun is a CRD — browse as cards, not a dense spreadsheet. */
export function RunBoard({ runs, namespace, emptyHint }: Props) {
  if (runs.length === 0) {
    return (
      <div className="empty">
        {emptyHint || `No AgentRuns match the current filters in ${namespace}.`}
      </div>
    );
  }

  return (
    <div className="card-grid card-grid-runs">
      {runs.map((run) => {
        const report = latestReportSummary(run);
        const error = shortError(run.error);
        const href = `/ns/${encodeURIComponent(run.namespace)}/runs/${encodeURIComponent(run.name)}`;
        return (
          <article key={`${run.namespace}/${run.name}/${run.uid}`} className="agent-card run-card">
            <header className="agent-card-header">
              <div className="chip-row">
                <PhaseBadge phase={run.phase} />
                <span className="chip chip-mute">AgentRun</span>
                {run.backend ? <span className="chip mono">{run.backend}</span> : null}
              </div>
              <h2 className="agent-card-title mono">
                <Link to={href}>{run.name}</Link>
              </h2>
            </header>
            <p className="agent-card-desc">
              {report || error || run.intent || "No report summary yet."}
            </p>
            <div className="agent-card-meta mono">
              {[
                run.intent || null,
                run.application || null,
                run.applicationTarget || null,
                sourceLabel(run) || null,
              ]
                .filter(Boolean)
                .join(" · ")}
            </div>
            <div className="agent-card-meta">
              created {formatTime(run.createdAt)}
              {run.startedAt ? ` · started ${formatTime(run.startedAt)}` : ""}
              {run.completedAt ? ` · done ${formatTime(run.completedAt)}` : ""}
              {" · "}
              {formatDuration(run)}
            </div>
            {error ? (
              <div className="run-card-error" title={run.error}>
                {error}
              </div>
            ) : null}
            <footer className="agent-card-actions">
              <Link className="btn btn-primary" to={href}>
                Open
              </Link>
              {run.pullRequestURL ? (
                <a
                  className="btn"
                  href={run.pullRequestURL}
                  target="_blank"
                  rel="noreferrer"
                  onClick={(event) => event.stopPropagation()}
                >
                  PR
                </a>
              ) : null}
            </footer>
          </article>
        );
      })}
    </div>
  );
}
