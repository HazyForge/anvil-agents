import { useEffect, useMemo, useState } from "react";
import { getAgentRun, type AgentRunView } from "../api/client";
import { isRunningPhase, peerSignalText } from "../wrapper/collaboration";
import { LiveStream } from "./LiveStream";

interface Props {
  token: string;
  namespace: string;
  runA: string;
  runB: string;
  objective: string;
}

export function CollaborationMonitor({ token, namespace, runA, runB, objective }: Props) {
  const [phaseA, setPhaseA] = useState("");
  const [phaseB, setPhaseB] = useState("");
  const [peerNotes, setPeerNotes] = useState<string[]>([]);

  useEffect(() => {
    if (!namespace || !runA || !runB) {
      return;
    }
    let cancelled = false;
    const poll = async () => {
      try {
        const [a, b] = await Promise.all([
          getAgentRun(token, namespace, runA),
          getAgentRun(token, namespace, runB),
        ]);
        if (cancelled) {
          return;
        }
        setPhaseA(a.phase || "");
        setPhaseB(b.phase || "");
        const hits = collectPeerNotes(a, b);
        if (hits.length > 0) {
          setPeerNotes((prev) => uniqueStrings([...prev, ...hits]));
        }
      } catch {
        // keep last known phases
      }
    };
    void poll();
    const id = window.setInterval(() => void poll(), 3000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [token, namespace, runA, runB]);

  const bothRunning = isRunningPhase(phaseA) && isRunningPhase(phaseB);
  const peerDetected = peerNotes.length > 0;

  const banner = useMemo(() => {
    if (bothRunning && peerDetected) {
      return { kind: "ok" as const, text: "Both runs are Running and a peer conferral/interrupt signal was observed." };
    }
    if (bothRunning) {
      return {
        kind: "info" as const,
        text: "Both grok AgentRuns are Running on the shared objective — watching streams for conferral or interrupt.",
      };
    }
    return {
      kind: "info" as const,
      text: `Overlap window: A=${phaseA || "—"}, B=${phaseB || "—"} (need both Running).`,
    };
  }, [bothRunning, peerDetected, phaseA, phaseB]);

  return (
    <section className="panel" style={{ marginTop: "0.75rem" }}>
      <div className="panel-header">
        <h2 className="panel-title">Shared objective · in-flight collaboration</h2>
        <span className={`chip ${bothRunning ? "chip-ok" : ""}`}>
          {bothRunning ? "both running" : "waiting overlap"}
        </span>
        {peerDetected ? <span className="chip chip-ok">peer signal</span> : null}
      </div>
      <div className="panel-body">
        <p className="muted">{objective}</p>
        <div className={`banner banner-${banner.kind === "ok" ? "info" : "info"}`}>{banner.text}</div>
        <div className="collab-run-cards">
          <RunCard name={runA} phase={phaseA} />
          <RunCard name={runB} phase={phaseB} />
        </div>
        {peerNotes.length > 0 ? (
          <ul className="collab-peer-notes">
            {peerNotes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        ) : null}
        <div className="stream-dual">
          <LiveStream token={token} namespace={namespace} name={runA} title={`${runA} (A)`} />
          <LiveStream token={token} namespace={namespace} name={runB} title={`${runB} (B)`} />
        </div>
      </div>
    </section>
  );
}

function RunCard({ name, phase }: { name: string; phase: string }) {
  const running = isRunningPhase(phase);
  return (
    <div className={`collab-run-card ${running ? "collab-run-card-running" : ""}`}>
      <span className="mono">{name}</span>
      <span className={`chip ${running ? "chip-ok" : ""}`}>{phase || "—"}</span>
    </div>
  );
}

function collectPeerNotes(a: AgentRunView, b: AgentRunView): string[] {
  const out: string[] = [];
  for (const run of [a, b]) {
    for (const report of run.reports ?? []) {
      const blob = [report.type, report.summary, report.detail].filter(Boolean).join(" ");
      if (peerSignalText(blob)) {
        out.push(`${run.name}: ${blob.trim()}`);
      }
    }
    if (run.decision?.summary && peerSignalText(run.decision.summary)) {
      out.push(`${run.name} decision: ${run.decision.summary}`);
    }
  }
  return out;
}

function uniqueStrings(items: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    if (!item || seen.has(item)) {
      continue;
    }
    seen.add(item);
    out.push(item);
  }
  return out;
}
