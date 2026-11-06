import { Link } from "react-router-dom";
import type { AgentRunView } from "../api/types";
import {
  formatDuration,
  formatTime,
  latestReportSummary,
  shortError,
  sourceLabel,
} from "../utils/format";
import { Icon } from "./Icon";
import { PhaseBadge, phaseKey } from "./PhaseBadge";

interface Props {
  runs: AgentRunView[];
  namespace: string;
  emptyHint?: string;
}

/** AgentRun is a CRD — browse as cards, not a dense spreadsheet. */
export function RunBoard({ runs, namespace, emptyHint }: Props) {
  if (runs.length === 0) {
    return (
      <div className="empty-state plate">
        <Icon name="runs" />
        <p className="empty">
          {emptyHint || `No AgentRuns match the current filters in ${namespace}.`}
        </p>
      </div>
    );
  }

  return (
    <div className="card-grid card-grid-runs">
      {runs.map((run) => {
        const report = latestReportSummary(run);
        const error = shortError(run.error);
        const href = `/ns/${encodeURIComponent(run.namespace)}/runs/${encodeURIComponent(run.name)}`;
        const tone = phaseKey(run.phase);
        return (
          <article
            key={`${run.namespace}/${run.name}/${run.uid}`}
            className={`agent-card run-card card tone-${tone}${toneClass(tone)}`}
          >
            <span className="run-card-rail" aria-hidden="true" />
            <header className="run-card-head">
              <PhaseBadge phase={run.phase} />
              <span className="run-card-duration mono" title="Duration">
                {formatDuration(run)}
              </span>
            </header>
            <h2 className="agent-card-title run-card-title mono">
              <Link to={href}>{run.name}</Link>
            </h2>
            <p className="agent-card-desc">
              {report || error || run.intent || "No report summary yet."}
            </p>
            {error ? (
              <div className="run-card-error" title={run.error}>
                {error}
              </div>
            ) : null}
            <div className="chip-row">
              <span className="chip chip-mute mono">AgentRun</span>
              {run.backend ? <span className="chip mono">{run.backend}</span> : null}
              {run.model ? (
                <span className="chip mono chip-info" title="Model">
                  {run.model}
                </span>
              ) : null}
            </div>
            <div className="agent-card-meta mono">
              {[
                run.model ? `model:${run.model}` : null,
                run.intent || null,
                run.application || null,
                run.applicationTarget || null,
                sourceLabel(run) || null,
              ]
                .filter(Boolean)
                .join(" · ")}
            </div>
            <div className="agent-card-meta run-card-time mono">
              created {formatTime(run.createdAt)}
              {run.startedAt ? ` · started ${formatTime(run.startedAt)}` : ""}
              {run.completedAt ? ` · done ${formatTime(run.completedAt)}` : ""}
            </div>
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
                  <Icon name="external" />
                </a>
              ) : null}
            </footer>
          </article>
        );
      })}
    </div>
  );
}

/** Plate edge tone for a phase: live, failed, and waiting-on-a-human read at a glance. */
function toneClass(tone: string): string {
  switch (tone) {
    case "running":
      return " live";
    case "failed":
      return " danger";
    case "needshuman":
      return " warn";
    default:
      return "";
  }
}
