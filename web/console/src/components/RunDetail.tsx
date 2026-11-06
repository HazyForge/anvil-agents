import { Link } from "react-router-dom";
import type { AgentRunView, ResolvedObjectRef } from "../api/types";
import {
  formatDuration,
  formatTime,
  isTerminal,
  latestHumanFollowUp,
  sourceLabel,
} from "../utils/format";
import { Icon } from "./Icon";
import { LiveStream } from "./LiveStream";
import { MarkdownBody } from "./MarkdownBody";
import { PhaseBadge, phaseKey } from "./PhaseBadge";

interface Props {
  run: AgentRunView;
  token: string;
  onRunUpdate: (run: AgentRunView) => void;
}

export function RunDetail({ run, token, onRunUpdate }: Props) {
  const followUp = latestHumanFollowUp(run.reports);
  const composition = run.resolvedComposition;
  const tone = phaseKey(run.phase);
  // Live runs lead with the stream; finished runs lead with the decision.
  // LiveStream keeps one tree position so a phase change never reopens it.
  const live = !isTerminal(run.phase);
  const reports = [...(run.reports ?? [])].sort(
    (a, b) => Date.parse(a.observedAt ?? "") - Date.parse(b.observedAt ?? ""),
  );
  const conditions = run.conditions ?? [];
  const scopeLine = [
    run.intent,
    run.application ? `${run.application}${run.applicationTarget ? ` / ${run.applicationTarget}` : ""}` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <div className={`detail-grid run-detail${live ? " is-live" : ""}`}>
      <section className={`panel run-hero tone-${tone}${heroTone(tone)}`}>
        <div className="panel-header">
          <h2 className="panel-title">Run header</h2>
          <div className="meta-row">
            <PhaseBadge phase={run.phase} />
            {run.archived ? <span className="chip">archived</span> : null}
          </div>
        </div>
        <div className="run-hero-body">
          <div className="run-hero-id">
            <span className="page-kicker kicker-id">{run.namespace} · AgentRun</span>
            <h1 className="run-hero-name mono">{run.name}</h1>
            {scopeLine ? <p className="run-hero-line">{scopeLine}</p> : null}
          </div>
          <div className="run-hero-figures">
            <div className="figure figure-lead">
              <span className="cap">Duration</span>
              <strong className="mono">{formatDuration(run)}</strong>
            </div>
            <div className="figure">
              <span className="cap">Reports</span>
              <strong className="mono">{reports.length}</strong>
            </div>
            <div className="figure">
              <span className="cap">Conditions</span>
              <strong className="mono">{conditions.length}</strong>
            </div>
          </div>
        </div>
        <dl className="kv kv-grid">
          <div>
            <dt>Name</dt>
            <dd className="mono">{run.name}</dd>
          </div>
          <div>
            <dt>Namespace</dt>
            <dd className="mono">{run.namespace}</dd>
          </div>
          <div>
            <dt>Backend</dt>
            <dd>{run.backend || "—"}</dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd className="mono">{run.model || "—"}</dd>
          </div>
          <div>
            <dt>Intent</dt>
            <dd>{run.intent || "—"}</dd>
          </div>
          <div>
            <dt>Application</dt>
            <dd>
              {run.application || "—"}
              {run.applicationTarget ? ` / ${run.applicationTarget}` : ""}
            </dd>
          </div>
          <div>
            <dt>Source</dt>
            <dd className="mono">{sourceLabel(run)}</dd>
          </div>
          <div>
            <dt>Created</dt>
            <dd className="mono">{formatTime(run.createdAt)}</dd>
          </div>
          <div>
            <dt>Started</dt>
            <dd className="mono">{formatTime(run.startedAt)}</dd>
          </div>
          <div>
            <dt>Completed</dt>
            <dd className="mono">{formatTime(run.completedAt)}</dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd className="mono">{formatDuration(run)}</dd>
          </div>
          <div>
            <dt>Job / Pod</dt>
            <dd className="mono">
              {run.job?.name || "—"} / {run.runnerPod?.name || "—"}
            </dd>
          </div>
          <div className="kv-wide">
            <dt>Pull request</dt>
            <dd>
              {run.pullRequestURL ? (
                <a href={run.pullRequestURL} target="_blank" rel="noreferrer">
                  {run.pullRequestURL}
                </a>
              ) : (
                "—"
              )}
            </dd>
          </div>
        </dl>
        {run.error ? (
          <div className="run-hero-error">
            <div className="banner banner-error">{run.error}</div>
          </div>
        ) : null}
      </section>

      <LiveStream
        token={token}
        namespace={run.namespace}
        name={run.name}
        onRunUpdate={onRunUpdate}
      />

      <div className="detail-columns">
        <div className="detail-main">
          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Decision</h2>
              {run.decision?.classification ? (
                <span className="chip chip-info">{run.decision.classification}</span>
              ) : null}
            </div>
            <div className="panel-body">
              {run.decision ? (
                <dl className="kv">
                  <dt>Classification</dt>
                  <dd>{run.decision.classification || "—"}</dd>
                  <dt>Action</dt>
                  <dd>{run.decision.action || "—"}</dd>
                  <dt>Summary</dt>
                  <dd>{run.decision.summary ? <MarkdownBody content={run.decision.summary} /> : "—"}</dd>
                  <dt>Residual risk</dt>
                  <dd>{run.decision.residualRisk || "—"}</dd>
                </dl>
              ) : (
                <div className="empty">No decision recorded.</div>
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Reports timeline</h2>
              <span className="count-pill">{reports.length}</span>
            </div>
            <div className="panel-body">
              {reports.length === 0 ? (
                <div className="empty">No reports.</div>
              ) : (
                <ol className="timeline">
                  {reports.map((report, index) => (
                    <li
                      key={`${report.observedAt ?? ""}-${index}`}
                      className={`timeline-item${report.needsHuman ? " is-human" : ""}${
                        report.level === "error" ? " is-error" : ""
                      }`}
                    >
                      <article className="report-card">
                        <header>
                          <span className="mono report-time">{formatTime(report.observedAt)}</span>
                          <span className="chip chip-info">{report.type || "report"}</span>
                          {report.level ? <span className="chip">{report.level}</span> : null}
                          {report.stage ? <span className="chip mono">{report.stage}</span> : null}
                          {report.needsHuman ? <span className="phase phase-needshuman">needs human</span> : null}
                        </header>
                        {report.summary ? <MarkdownBody content={report.summary} /> : null}
                        {report.action ? <div className="muted">action: {report.action}</div> : null}
                        {report.detail ? <div className="pre pre-markdown"><MarkdownBody content={report.detail} /></div> : null}
                        {report.residualRisk ? <div className="muted">risk: {report.residualRisk}</div> : null}
                        {report.humanFollowUp ? (
                          <div className="banner banner-warn">
                            <MarkdownBody content={report.humanFollowUp} />
                          </div>
                        ) : null}
                        {report.pullRequestURL ? (
                          <div>
                            <a href={report.pullRequestURL} target="_blank" rel="noreferrer">
                              {report.pullRequestURL}
                            </a>
                          </div>
                        ) : null}
                      </article>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Output</h2>
            </div>
            <div className="panel-body">
              {run.output ? <div className="pre pre-markdown"><MarkdownBody content={run.output} /></div> : <div className="empty">No output.</div>}
            </div>
          </section>
        </div>

        <div className="detail-side">
          <section className={`panel${followUp ? " warn" : ""}`}>
            <div className="panel-header">
              <h2 className="panel-title">
                {followUp ? <Icon name="alert" className="title-icon-warn" /> : null}
                Human follow-up
              </h2>
            </div>
            <div className="panel-body">
              {followUp ? (
                <MarkdownBody content={followUp} />
              ) : (
                <div className="empty">No humanFollowUp on reports.</div>
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Conditions</h2>
              <span className="count-pill">{conditions.length}</span>
            </div>
            <div className="panel-body">
              {conditions.length === 0 ? (
                <div className="empty">No conditions.</div>
              ) : (
                conditions.map((condition, index) => (
                  <article key={`${condition.type}-${index}`} className="condition-card">
                    <header>
                      <strong>{condition.type}</strong>
                      <span className={`chip ${conditionTone(condition.status)}`}>{condition.status}</span>
                      <span className="condition-reason">{condition.reason}</span>
                      <span className="mono condition-time">{formatTime(condition.lastTransitionTime)}</span>
                    </header>
                    {condition.message ? <div className="condition-message">{condition.message}</div> : null}
                  </article>
                ))
              )}
            </div>
          </section>

          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Resolved composition</h2>
            </div>
            <div className="panel-body">
              {!composition ? (
                <div className="empty">No resolved composition.</div>
              ) : (
                <dl className="kv kv-stack">
                  <dt>Resolved at</dt>
                  <dd className="mono">{formatTime(composition.resolvedAt)}</dd>
                  <dt>Profile</dt>
                  <dd className="mono">
                    {compositionRefLink(run.namespace, "profiles", composition.profileRef)}
                  </dd>
                  <dt>Harness profile</dt>
                  <dd className="mono">
                    {compositionRefLink(run.namespace, "harness-profiles", composition.harnessProfileRef)}
                  </dd>
                  <dt>Council</dt>
                  <dd className="mono">
                    {compositionRefLink(run.namespace, "councils", composition.councilRef)}
                  </dd>
                  <dt>Skill sets</dt>
                  <dd>
                    {(composition.skillSetRefs ?? []).length > 0
                      ? (composition.skillSetRefs ?? []).map((ref, index) => (
                          <span key={`${ref.name}-${index}`} className="composition-ref-item">
                            {index > 0 ? " " : null}
                            {compositionRefLink(run.namespace, "skill-sets", ref)}
                          </span>
                        ))
                      : "—"}
                  </dd>
                  <dt>Tool sets</dt>
                  <dd>
                    {(composition.toolSetRefs ?? []).length > 0
                      ? (composition.toolSetRefs ?? []).map((ref, index) => (
                          <span key={`${ref.name}-${index}`} className="composition-ref-item">
                            {index > 0 ? " " : null}
                            {compositionRefLink(run.namespace, "tool-sets", ref)}
                          </span>
                        ))
                      : "—"}
                  </dd>
                  <dt>Scope</dt>
                  <dd>
                    {composition.scope?.application || "—"}
                    {composition.scope?.applicationTarget
                      ? ` / ${composition.scope.applicationTarget}`
                      : ""}
                  </dd>
                  <dt>Effective digest</dt>
                  <dd className="mono digest">{composition.effectiveDigest || "—"}</dd>
                  <dt>Payload digest</dt>
                  <dd className="mono digest">{composition.payloadDigest || "—"}</dd>
                </dl>
              )}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function heroTone(tone: string): string {
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

/** Condition polarity varies by type, so True/False stay neutral; Unknown is waiting. */
function conditionTone(status?: string): string {
  if (status === "True") {
    return "chip-info";
  }
  if (status === "False") {
    return "chip-mute";
  }
  return "chip-warn";
}

function compositionRefLink(
  runNamespace: string,
  kindRoute: string,
  ref?: ResolvedObjectRef,
): React.ReactNode {
  if (!ref?.name) {
    return "—";
  }
  const ns = ref.namespace || runNamespace;
  const digest = ref.digest ? `@${ref.digest.slice(0, 12)}` : "";
  const label = `${ref.namespace ? `${ref.namespace}/` : ""}${ref.name}${digest}`;
  return (
    <span className="composition-ref-link-wrap mono">
      <Link to={`/ns/${encodeURIComponent(ns)}/${kindRoute}/${encodeURIComponent(ref.name)}`}>
        {label}
      </Link>
      {ref.global ? (
        <span className="chip chip-global" title="Attached as namespace-global skill/tool set">
          global
        </span>
      ) : null}
    </span>
  );
}
