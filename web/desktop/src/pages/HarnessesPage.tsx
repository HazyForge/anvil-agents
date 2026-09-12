import type { Prefs, Snapshot } from "../api/types";
import { LocalRuntimeCard } from "../components/LocalRuntimeCard";

export function HarnessesPage({
  snapshot,
  busy,
  onSavePrefs,
}: {
  snapshot: Snapshot;
  busy?: boolean;
  onSavePrefs: (patch: Prefs) => Promise<void> | void;
}) {
  const harnesses = snapshot.harnesses.filter((item) => item.kind === "harness");
  const others = snapshot.harnesses.filter((item) => item.kind !== "harness");
  const sourceLabel = snapshot.harnessTarget === "wsl" ? "on WSL" : "on PATH";
  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Local harnesses</h1>
          <p className="page-sub">
            Anvil Agents Desktop discovers CLIs already on this workstation so the wrapper agent can
            delegate work to them. Choose native PATH or Operate on WSL. Cluster AgentRuns still use
            runner images. This inventory is not a Kubernetes control plane.
          </p>
        </div>
      </div>
      <LocalRuntimeCard snapshot={snapshot} busy={busy} onSavePrefs={onSavePrefs} />
      <div className="card-grid" style={{ marginTop: "0.75rem" }}>
        {harnesses.map((item) => (
          <article key={item.id} className={`card ${item.present ? "card-present" : "card-missing"}`}>
            <div className="card-kicker">{item.backend || "harness"}</div>
            <h2 className="card-title">{item.displayName}</h2>
            <div className="chip-row">
              <span className={`chip ${item.present ? "chip-ok" : ""}`}>
                {item.present ? sourceLabel : "not found"}
              </span>
              {item.source === "wsl" || item.wslDistro ? (
                <span className="chip">{item.wslDistro ? `WSL · ${item.wslDistro}` : "WSL"}</span>
              ) : null}
              {item.delegatable ? <span className="chip chip-ok">delegatable</span> : <span className="chip">inventory</span>}
              {item.version ? <span className="chip mono">{item.version}</span> : null}
            </div>
            {item.path ? <div className="mono path">{item.path}</div> : <div className="muted">Looked for {item.binaries.join(", ")}</div>}
            <p className="card-notes">{item.notes}</p>
            {item.authFileHint ? <p className="muted">Local auth file: {item.authFileHint}</p> : null}
          </article>
        ))}
      </div>
      {others.length > 0 ? (
        <>
          <h2 className="section-title">Also on this machine</h2>
          <div className="card-grid">
            {others.map((item) => (
              <article key={item.id} className={`card ${item.present ? "card-present" : "card-missing"}`}>
                <div className="card-kicker">{item.kind}</div>
                <h2 className="card-title">{item.displayName}</h2>
                <div className="chip-row">
                  <span className={`chip ${item.present ? "chip-ok" : ""}`}>{item.present ? sourceLabel : "not found"}</span>
                  {item.version ? <span className="chip mono">{item.version}</span> : null}
                </div>
                {item.path ? <div className="mono path">{item.path}</div> : <div className="muted">Looked for {item.binaries.join(", ")}</div>}
                <p className="card-notes">{item.notes}</p>
              </article>
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
