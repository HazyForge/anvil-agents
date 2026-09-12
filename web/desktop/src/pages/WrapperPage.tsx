import { Link } from "react-router-dom";
import { useEffect, useState } from "react";
import {
  createAgentRun,
  createAgentRunProfile,
  getAgentRun,
  listAgentRuns,
  listRunProfiles,
  type AgentRunView,
  type CompositionDocument,
} from "../api/client";
import type { Snapshot } from "../api/types";
import type { UIConfig } from "../auth/config";
import { CollaborationMonitor } from "../components/CollaborationMonitor";
import { personaLabel } from "../names";
import { loadNamespace, saveNamespace } from "../state/namespace";
import { startStaggeredPairedObjective } from "../wrapper/collaboration";

interface Props {
  snapshot: Snapshot;
  token: string;
  config: UIConfig;
}

export function WrapperPage({ snapshot, token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [prompt, setPrompt] = useState("");
  const [objective, setObjective] = useState(
    "Draft a one-paragraph Primaris desktop collab note; coordinate with the peer grok run so only one build happens.",
  );
  const [profileName, setProfileName] = useState("");
  const [runName, setRunName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [collabStatus, setCollabStatus] = useState<string | null>(null);
  const [apiOutput, setApiOutput] = useState("");
  const [runs, setRuns] = useState<AgentRunView[]>([]);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [pairedRuns, setPairedRuns] = useState<{ runA: string; runB: string; objective: string } | null>(null);

  const apiTools = snapshot.wrapper.tools.filter((tool) => tool.id !== "local-harness");
  const localTool = snapshot.wrapper.tools.find((tool) => tool.id === "local-harness");

  function setNs(next: string) {
    setNamespace(next);
    saveNamespace(next);
  }

  async function refreshRuns(silent = false) {
    if (!namespace) {
      return;
    }
    try {
      const items = await listAgentRuns(token, namespace, 50);
      setRuns(items);
      if (!silent) {
        setError(null);
      }
    } catch (err) {
      if (!silent) {
        setError(`list runs: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
  }

  useEffect(() => {
    if (!namespace || !token || !pairedRuns) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshRuns(true);
    }, 4000);
    return () => window.clearInterval(id);
  }, [namespace, token, pairedRuns]);

  async function runAPI(label: string, fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      const result = await fn();
      const text = JSON.stringify(result, null, 2);
      setApiOutput(text);
      return result;
    } catch (err) {
      setError(`${label}: ${err instanceof Error ? err.message : String(err)}`);
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function onListRuns() {
    setBusy(true);
    setError(null);
    try {
      await refreshRuns();
    } finally {
      setBusy(false);
    }
  }

  async function onGetRun() {
    const name = runName.trim();
    if (!name) {
      setError("enter an AgentRun name to get");
      return;
    }
    await runAPI("get run", () => getAgentRun(token, namespace, name));
  }

  async function onListProfiles() {
    const items = (await runAPI("list profiles", () => listRunProfiles(token, namespace))) as
      | CompositionDocument[]
      | null;
    if (items) {
      setProfiles(items);
    }
  }

  async function onCreateRun() {
    if (!config.runs.createEnabled) {
      setError("AgentRun create is disabled on this API");
      return;
    }
    await runAPI("create run", () =>
      createAgentRun(token, namespace, {
        generateName: "desktop-",
        prompt: prompt.trim(),
        profileName: profileName.trim(),
      }),
    );
    await refreshRuns(true);
  }

  async function onStartPairedObjective() {
    if (!config.runs.createEnabled) {
      setError("AgentRun create is disabled on this API");
      return;
    }
    setBusy(true);
    setError(null);
    setCollabStatus(null);
    setPairedRuns(null);
    try {
      const result = await startStaggeredPairedObjective({
        token,
        namespace,
        profileName: profileName.trim(),
        objective: objective.trim(),
        onStatus: (message) => setCollabStatus(message),
      });
      setPairedRuns({ ...result, objective: objective.trim() });
      setApiOutput(JSON.stringify(result, null, 2));
      await refreshRuns(true);
    } catch (err) {
      setError(`paired objective: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  }

  async function onCreateProfile() {
    const name = profileName.trim();
    if (!name) {
      setError("enter an AgentRunProfile name to create");
      return;
    }
    if (!config.composition.writeEnabled) {
      setError("composition write is disabled on this API");
      return;
    }
    await runAPI("create profile", () =>
      createAgentRunProfile(token, namespace, {
        name,
        description: "desktop-created entity",
      }),
    );
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Wrapper</h1>
          <p className="page-sub">{snapshot.wrapper.message}</p>
        </div>
      </div>

      <div className="split">
        {apiTools.map((tool) => (
          <section key={tool.id} className="panel">
            <div className="panel-header">
              <h2 className="panel-title">{tool.displayName}</h2>
              <span className="chip mono">{tool.id}</span>
            </div>
            <div className="panel-body">
              <p className="muted">{tool.notes}</p>
            </div>
          </section>
        ))}
        {localTool ? (
          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">{localTool.displayName}</h2>
              <span className="chip">second function</span>
            </div>
            <div className="panel-body">
              <p className="muted">{localTool.notes}</p>
              <Link to="/local" className="btn">
                Open Local
              </Link>
            </div>
          </section>
        ) : null}
      </div>

      <section className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">Session</h2>
        </div>
        <div className="panel-body">
          <label className="field">
            <span className="label">Namespace</span>
            <input
              className="input"
              value={namespace}
              onChange={(event) => setNs(event.target.value)}
              placeholder={fallbackNs || "namespace"}
            />
          </label>
          {config.defaultNamespaces.length > 0 ? (
            <p className="muted">
              From ui-config: {config.defaultNamespaces.join(", ")}. The API is namespaced; this is not a
              kube context picker.
            </p>
          ) : null}
          <label className="field">
            <span className="label">Prompt (single run)</span>
            <textarea
              className="input textarea"
              rows={3}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Prompt for a new AgentRun on Primaris. The OIDC token is never appended."
            />
          </label>
        </div>
      </section>

      <section className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">Paired grok objective</h2>
          <span className="chip">staggered overlap</span>
        </div>
        <div className="panel-body">
          <p className="muted">
            Starts worker A, waits until it is <strong>Running</strong>, then starts worker B on the same
            objective so conferral or interrupt can happen while both are in flight. Proof is live phases plus
            stream/report peer signals — not two jobs at one click.
          </p>
          <label className="field">
            <span className="label">Shared objective</span>
            <textarea
              className="input textarea"
              rows={3}
              value={objective}
              onChange={(event) => setObjective(event.target.value)}
            />
          </label>
          <label className="field">
            <span className="label">grok AgentRunProfile</span>
            <input
              className="input"
              value={profileName}
              onChange={(event) => setProfileName(event.target.value)}
              placeholder="list profiles first"
            />
          </label>
          {config.runs.createEnabled ? (
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy || !namespace || !profileName.trim() || !objective.trim()}
              onClick={() => void onStartPairedObjective()}
            >
              {busy ? "Starting paired objective…" : "Start paired grok objective"}
            </button>
          ) : (
            <span className="muted">AgentRun create is disabled on this API.</span>
          )}
          {collabStatus ? <p className="muted">{collabStatus}</p> : null}
        </div>
      </section>

      <section className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">anvil-api · Primaris OIDC</h2>
          <span className="chip">not Kubernetes</span>
        </div>
        <div className="panel-body">
          <div className="btn-row">
            <button type="button" className="btn btn-primary" disabled={busy || !namespace} onClick={() => void onListRuns()}>
              List runs
            </button>
            <button type="button" className="btn" disabled={busy || !namespace} onClick={() => void onGetRun()}>
              Get run
            </button>
            {config.composition.readEnabled ? (
              <button type="button" className="btn" disabled={busy || !namespace} onClick={() => void onListProfiles()}>
                List profiles
              </button>
            ) : null}
            {config.composition.writeEnabled ? (
              <button
                type="button"
                className="btn"
                disabled={busy || !namespace || !profileName.trim()}
                onClick={() => void onCreateProfile()}
              >
                Create profile
              </button>
            ) : null}
            {config.runs.createEnabled ? (
              <button
                type="button"
                className="btn"
                disabled={busy || !namespace || !prompt.trim() || !profileName.trim()}
                onClick={() => void onCreateRun()}
              >
                Create run
              </button>
            ) : null}
            <Link to="/chat" className="btn">
              Agent chat
            </Link>
          </div>
          <label className="field">
            <span className="label">AgentRun name (get)</span>
            <input className="input" value={runName} onChange={(event) => setRunName(event.target.value)} />
          </label>
          {profiles.length > 0 ? (
            <p className="muted">
              Profiles: {profiles.map((item) => personaLabel(item.metadata.name)).join(", ")}
            </p>
          ) : null}
          {runs.length > 0 ? (
            <>
              <p className="muted">Runs from the OIDC API, not kubectl.</p>
              <table className="run-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Phase</th>
                    <th>Backend</th>
                  </tr>
                </thead>
                <tbody>
                  {runs.slice(0, 20).map((run) => (
                    <tr key={run.name}>
                      <td className="mono">{run.name}</td>
                      <td>{run.phase || "—"}</td>
                      <td className="mono">{run.backend || "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          ) : null}
          {apiOutput ? <pre className="hint">{apiOutput}</pre> : null}
        </div>
      </section>

      {pairedRuns ? (
        <CollaborationMonitor
          token={token}
          namespace={namespace}
          runA={pairedRuns.runA}
          runB={pairedRuns.runB}
          objective={pairedRuns.objective}
        />
      ) : null}

      {error ? (
        <div className="banner banner-error" style={{ marginTop: "0.75rem" }}>
          {error}
        </div>
      ) : null}
    </div>
  );
}
