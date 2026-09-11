import type { Snapshot } from "../api/types";

export function HarnessesPage({ snapshot }: { snapshot: Snapshot }) {
  const harnesses = snapshot.harnesses.filter((item) => item.kind === "harness");
  const others = snapshot.harnesses.filter((item) => item.kind !== "harness");
  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Local harnesses</h1>
          <p className="page-sub">
            Discover CLIs already on this machine. Cluster AgentRuns still use runner images; this
            inventory is how operators connect those workstation clients to a kubecontext.
          </p>
        </div>
      </div>
      <div className="card-grid">
        {harnesses.map((item) => (
          <article key={item.id} className={`card ${item.present ? "card-present" : "card-missing"}`}>
            <div className="card-kicker">{item.backend || "workstation"}</div>
            <h2 className="card-title">{item.displayName}</h2>
            <div className="chip-row">
              <span className={`chip ${item.present ? "chip-ok" : ""}`}>{item.present ? "on PATH" : "not found"}</span>
              {item.version ? <span className="chip mono">{item.version}</span> : null}
            </div>
            {item.path ? <div className="mono path">{item.path}</div> : <div className="muted">Looked for {item.binaries.join(", ")}</div>}
            <p className="card-notes">{item.notes}</p>
            {item.clusterHint ? <pre className="hint">{item.clusterHint}</pre> : null}
          </article>
        ))}
      </div>
      <h2 className="section-title">Cluster clients</h2>
      <div className="card-grid">
        {others.map((item) => (
          <article key={item.id} className={`card ${item.present ? "card-present" : "card-missing"}`}>
            <div className="card-kicker">{item.kind}</div>
            <h2 className="card-title">{item.displayName}</h2>
            <div className="chip-row">
              <span className={`chip ${item.present ? "chip-ok" : ""}`}>{item.present ? "on PATH" : "not found"}</span>
              {item.version ? <span className="chip mono">{item.version}</span> : null}
            </div>
            {item.path ? <div className="mono path">{item.path}</div> : <div className="muted">Looked for {item.binaries.join(", ")}</div>}
            <p className="card-notes">{item.notes}</p>
          </article>
        ))}
      </div>
    </div>
  );
}
