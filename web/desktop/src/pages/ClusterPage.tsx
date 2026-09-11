import { useMemo, useState } from "react";
import { openConsole } from "../api/client";
import type { Prefs, Snapshot } from "../api/types";

interface Props {
  snapshot: Snapshot;
  onSave: (prefs: Prefs) => Promise<void> | void;
  busy: boolean;
}

export function ClusterPage({ snapshot, onSave, busy }: Props) {
  const cluster = snapshot.cluster;
  const [contextName, setContextName] = useState(cluster.selectedContext || cluster.currentContext || "");
  const [consoleURL, setConsoleURL] = useState(cluster.console.url || snapshot.prefs.consoleURL || "");

  const selected = useMemo(
    () => cluster.contexts.find((item) => item.name === contextName),
    [cluster.contexts, contextName],
  );

  const agentctl = snapshot.harnesses.find((item) => item.id === "anvil-agentctl");
  const kubectl = snapshot.harnesses.find((item) => item.id === "kubectl");

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Kubernetes cluster</h1>
          <p className="page-sub">
            Select a kubecontext for <span className="mono">anvil-agentctl</span>. Wrap the existing
            Anvil Agents Console as a top-level window — it cannot be framed (CSP frame-ancestors none).
          </p>
        </div>
      </div>

      <div className="split">
        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Kubecontext</h2>
            <span className="muted mono">{cluster.kubeconfig || "default loading rules"}</span>
          </div>
          <div className="panel-body">
            {cluster.message ? <div className="banner banner-error">{cluster.message}</div> : null}
            {cluster.contexts.length === 0 ? (
              <p className="empty">No kubeconfig contexts found. Place a kubeconfig on this machine.</p>
            ) : (
              <label className="field">
                <span className="label">Context</span>
                <select className="select" value={contextName} onChange={(event) => setContextName(event.target.value)}>
                  {cluster.contexts.map((item) => (
                    <option key={item.name} value={item.name}>
                      {item.name}
                      {item.current ? " (current)" : ""}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <dl className="meta">
              <div>
                <dt>Namespace</dt>
                <dd className="mono">{selected?.namespace || cluster.namespace || "default"}</dd>
              </div>
              <div>
                <dt>Cluster</dt>
                <dd className="mono">{selected?.cluster || "—"}</dd>
              </div>
            </dl>
            <div className={`banner ${cluster.operator.apiGroupPresent ? "banner-ok" : "banner-info"}`}>
              {cluster.operator.message || "Operator not probed yet."}
            </div>
            <p className="muted">
              {agentctl?.present ? "anvil-agentctl is on PATH." : "anvil-agentctl is not on PATH."}{" "}
              {kubectl?.present ? "kubectl is on PATH." : "kubectl is not on PATH."}
            </p>
            <pre className="hint">
              anvil-agentctl --context {contextName || "CONTEXT"} run list -n {selected?.namespace || "default"}
            </pre>
          </div>
        </section>

        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Anvil Agents Console</h2>
          </div>
          <div className="panel-body">
            <label className="field">
              <span className="label">Console origin</span>
              <input
                className="input"
                placeholder="https://agents.anvil.hazyforge.io"
                value={consoleURL}
                onChange={(event) => setConsoleURL(event.target.value)}
              />
            </label>
            <div className={`banner ${cluster.console.reachable ? "banner-ok" : "banner-info"}`}>
              {cluster.console.message || "Save an origin to wrap the existing SPA."}
            </div>
            <p className="muted">
              Standing chat and future per-council chat live in that console. Anvil Agents Desktop
              does not fork a second UI.
            </p>
            <div className="btn-row">
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy}
                onClick={() => void onSave({ kubeContext: contextName, consoleURL, kubeconfig: snapshot.prefs.kubeconfig })}
              >
                Save connection
              </button>
              <button
                type="button"
                className="btn"
                disabled={!consoleURL}
                onClick={() => openConsole(consoleURL, "/")}
              >
                Open console
              </button>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
