import { useEffect, useState } from "react";
import { getAgentRun, type AgentRunView } from "../api/client";
import { stickAgentRunStatus } from "../wrapper/runStatus";
import { AgentRunStatusCard } from "./AgentRunStatusCard";
import { LiveStream } from "./LiveStream";

export const MIDRUN_OBJECTIVE = "midrun-proof-bc98af8-20260912";
export const MIDRUN_RUN_A = "midrun-peer-a-frkt2";
export const MIDRUN_RUN_B = "midrun-peer-b-kl5xx";
export const MIDRUN_NAMESPACE = "hazy-trade";

interface Props {
  token: string;
  namespace: string;
  onLoaded?: (runs: { a: AgentRunView; b: AgentRunView }) => void;
}

export function MidrunProofPanel({ token, namespace, onLoaded }: Props) {
  const [runs, setRuns] = useState<{ a: AgentRunView; b: AgentRunView } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function load() {
    setBusy(true);
    setError(null);
    try {
      const a = await getAgentRun(token, namespace, MIDRUN_RUN_A);
      const b = await getAgentRun(token, namespace, MIDRUN_RUN_B);
      setRuns({ a, b });
      onLoaded?.({ a, b });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setRuns(null);
    } finally {
      setBusy(false);
    }
  }

  const watching = Boolean(runs);

  useEffect(() => {
    if (!watching || !token || !namespace) {
      return;
    }
    let cancelled = false;
    const poll = async () => {
      try {
        const [a, b] = await Promise.all([
          getAgentRun(token, namespace, MIDRUN_RUN_A),
          getAgentRun(token, namespace, MIDRUN_RUN_B),
        ]);
        if (cancelled) {
          return;
        }
        setRuns((prev) =>
          prev
            ? { a: stickAgentRunStatus(prev.a, a), b: stickAgentRunStatus(prev.b, b) }
            : { a, b },
        );
      } catch {
        // keep last sticky GET
      }
    };
    const id = window.setInterval(() => void poll(), 3000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [watching, token, namespace]);

  return (
    <section className="panel" style={{ marginTop: "0.75rem" }}>
      <div className="panel-header">
        <h2 className="panel-title">Midrun grok proof (existing)</h2>
        <span className="chip mono">{MIDRUN_OBJECTIVE}</span>
      </div>
      <div className="panel-body">
        <p className="muted">
          GET and stream the two grok AgentRuns that already emitted mid-run decisions. Does not create new
          runs.
        </p>
        <div className="btn-row">
          <button type="button" className="btn btn-primary" disabled={busy || !namespace} onClick={() => void load()}>
            {busy ? "Loading…" : "GET midrun proof runs + streams"}
          </button>
        </div>
        {error ? <div className="banner banner-error">{error}</div> : null}
        {runs ? (
          <>
            <div className="collab-run-cards">
              <RunDecisionCard run={runs.a} label="A GET" />
              <RunDecisionCard run={runs.b} label="B GET" />
            </div>
            <div className="stream-dual">
              <LiveStream token={token} namespace={namespace} name={MIDRUN_RUN_A} title={`${MIDRUN_RUN_A} (A)`} />
              <LiveStream token={token} namespace={namespace} name={MIDRUN_RUN_B} title={`${MIDRUN_RUN_B} (B)`} />
            </div>
          </>
        ) : null}
      </div>
    </section>
  );
}

function RunDecisionCard({ run, label }: { run: AgentRunView; label: string }) {
  const action = run.decision?.action || "—";
  const summary = run.decision?.summary || "";
  return (
    <div className="collab-run-card">
      <AgentRunStatusCard run={run} label={label} />
      <span className="chip mono">action={action}</span>
      {summary ? <p className="muted">{summary}</p> : null}
    </div>
  );
}
