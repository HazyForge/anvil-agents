import { openConsole } from "../api/client";
import type { Snapshot } from "../api/types";

export function ChatPage({ snapshot }: { snapshot: Snapshot }) {
  const origin = snapshot.cluster.console.url || snapshot.prefs.consoleURL || "";
  const path = snapshot.chat.standingChatPath || "/chat";
  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Standing chat</h1>
          <p className="page-sub">Aligned with the cluster console. Anvil Agents Desktop does not wait on chat designs to manage harnesses and kubecontexts.</p>
        </div>
      </div>
      <div className="split">
        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Cluster standing chat</h2>
          </div>
          <div className="panel-body">
            <p>{snapshot.chat.message}</p>
            <p className="muted">
              When the standing-chat API is enabled, the wrapped console serves{" "}
              <span className="mono">{path}</span>. Open that origin as a top-level window so OIDC
              and CSP stay intact.
            </p>
            <div className="btn-row">
              <button type="button" className="btn btn-primary" disabled={!origin} onClick={() => openConsole(origin, path)}>
                Open {path}
              </button>
            </div>
            {!origin ? <p className="muted">Set a console origin on the Cluster page first.</p> : null}
          </div>
        </section>
        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Per-council chat</h2>
            <span className="chip">future</span>
          </div>
          <div className="panel-body">
            <p>
              AgentCouncil objects remain workforce inventory. A later per-council conversation
              surface can hang off the same console wrap without changing Anvil Agents Desktop.
            </p>
            <p className="muted">Status: {snapshot.chat.councilChat}. Do not block local harness or kubecontext work on that design.</p>
          </div>
        </section>
      </div>
    </div>
  );
}
