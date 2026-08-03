import { Link } from "react-router-dom";
import type { AgentRunView, ResolvedObjectRef } from "../api/types";
import {
  formatDuration,
  formatTime,
  latestHumanFollowUp,
  sourceLabel,
} from "../utils/format";
import { LiveStream } from "./LiveStream";
import { PhaseBadge } from "./PhaseBadge";

interface Props {
  run: AgentRunView;
  token: string;
  onRunUpdate: (run: AgentRunView) => void;
}

export function RunDetail({ run, token, onRunUpdate }: Props) {
  const followUp = latestHumanFollowUp(run.reports);
  const composition = run.resolvedComposition;

  return (
    <div className="detail-grid">
      <section className="panel span-2">
        <div className="panel-header">
          <h2 className="panel-title">Run header</h2>
          <div className="meta-row">
            <PhaseBadge phase={run.phase} />
            {run.archived ? <span className="chip">archived</span> : null}
          </div>
        </div>
        <div className="panel-body">
          <dl className="kv">
            <dt>Name</dt>
            <dd className="mono">{run.name}</dd>
            <dt>Namespace</dt>
            <dd className="mono">{run.namespace}</dd>
            <dt>Backend</dt>
            <dd>{run.backend || "—"}</dd>
            <dt>Model</dt>
            <dd className="mono">{run.model || "—"}</dd>
            <dt>Intent</dt>
            <dd>{run.intent || "—"}</dd>
            <dt>Application</dt>
            <dd>
              {run.application || "—"}
              {run.applicationTarget ? ` / ${run.applicationTarget}` : ""}
            </dd>
            <dt>Source</dt>
            <dd className="mono">{sourceLabel(run)}</dd>
            <dt>Created</dt>
            <dd className="mono">{formatTime(run.createdAt)}</dd>
            <dt>Started</dt>
            <dd className="mono">{formatTime(run.startedAt)}</dd>
            <dt>Completed</dt>
            <dd className="mono">{formatTime(run.completedAt)}</dd>
            <dt>Duration</dt>
            <dd className="mono">{formatDuration(run)}</dd>
            <dt>Job / Pod</dt>
            <dd className="mono">
              {run.job?.name || "—"} / {run.runnerPod?.name || "—"}
            </dd>
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
          </dl>
          {run.error ? (
            <div className="banner banner-error" style={{ marginTop: "0.75rem", marginBottom: 0 }}>
              {run.error}
            </div>
          ) : null}
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <h2 className="panel-title">Decision</h2>
        </div>
        <div className="panel-body">
          {run.decision ? (
            <dl className="kv">
              <dt>Classification</dt>
              <dd>{run.decision.classification || "—"}</dd>
              <dt>Action</dt>
              <dd>{run.decision.action || "—"}</dd>
              <dt>Summary</dt>
              <dd>{run.decision.summary || "—"}</dd>
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
          <h2 className="panel-title">Human follow-up</h2>
        </div>
        <div className="panel-body">
          {followUp ? (
            <p style={{ margin: 0, whiteSpace: "pre-wrap" }}>{followUp}</p>
          ) : (
            <div className="empty">No humanFollowUp on reports.</div>
          )}
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <h2 className="panel-title">Reports timeline</h2>
        </div>
        <div className="panel-body stack">
          {(run.reports ?? []).length === 0 ? (
            <div className="empty">No reports.</div>
          ) : (
            [...(run.reports ?? [])]
              .sort((a, b) => Date.parse(a.observedAt ?? "") - Date.parse(b.observedAt ?? ""))
              .map((report, index) => (
                <article key={`${report.observedAt ?? ""}-${index}`} className="report-card">
                  <header>
                    <span>{formatTime(report.observedAt)}</span>
                    <span>{report.type || "report"}</span>
                    {report.level ? <span>{report.level}</span> : null}
                    {report.stage ? <span>{report.stage}</span> : null}
                    {report.needsHuman ? <span className="phase phase-needshuman">needs human</span> : null}
                  </header>
                  {report.summary ? <div>{report.summary}</div> : null}
                  {report.action ? <div className="muted">action: {report.action}</div> : null}
                  {report.detail ? <pre className="pre">{report.detail}</pre> : null}
                  {report.residualRisk ? <div className="muted">risk: {report.residualRisk}</div> : null}
                  {report.humanFollowUp ? (
                    <div className="banner banner-warn" style={{ marginBottom: 0, marginTop: "0.4rem" }}>
                      {report.humanFollowUp}
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
              ))
          )}
        </div>
      </section>

      <section className="panel">
        <div className="panel-header">
          <h2 className="panel-title">Conditions</h2>
        </div>
        <div className="panel-body stack">
          {(run.conditions ?? []).length === 0 ? (
            <div className="empty">No conditions.</div>
          ) : (
            (run.conditions ?? []).map((condition, index) => (
              <article key={`${condition.type}-${index}`} className="condition-card">
                <header>
                  <strong>{condition.type}</strong>
                  <span>{condition.status}</span>
                  <span>{condition.reason}</span>
                  <span className="mono">{formatTime(condition.lastTransitionTime)}</span>
                </header>
                {condition.message ? <div>{condition.message}</div> : null}
              </article>
            ))
          )}
        </div>
      </section>

      <section className="panel span-2">
        <div className="panel-header">
          <h2 className="panel-title">Resolved composition</h2>
        </div>
        <div className="panel-body">
          {!composition ? (
            <div className="empty">No resolved composition.</div>
          ) : (
            <dl className="kv">
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
              <dd className="mono">{composition.effectiveDigest || "—"}</dd>
              <dt>Payload digest</dt>
              <dd className="mono">{composition.payloadDigest || "—"}</dd>
            </dl>
          )}
        </div>
      </section>

      <section className="panel span-2">
        <div className="panel-header">
          <h2 className="panel-title">Output</h2>
        </div>
        <div className="panel-body">
          {run.output ? <pre className="pre">{run.output}</pre> : <div className="empty">No output.</div>}
        </div>
      </section>

      <LiveStream
        token={token}
        namespace={run.namespace}
        name={run.name}
        onRunUpdate={onRunUpdate}
      />
    </div>
  );
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
