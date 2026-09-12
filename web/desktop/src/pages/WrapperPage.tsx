import { Link } from "react-router-dom";
import { useState } from "react";
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
import { personaLabel } from "../names";
import { loadNamespace, saveNamespace } from "../state/namespace";

interface Props {
  snapshot: Snapshot;
  token: string;
  config: UIConfig;
}

export function WrapperPage({ snapshot, token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [prompt, setPrompt] = useState("");
  const [profileName, setProfileName] = useState("");
  const [runName, setRunName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [apiOutput, setApiOutput] = useState("");
  const [runs, setRuns] = useState<AgentRunView[]>([]);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);

  const apiTools = snapshot.wrapper.tools.filter((tool) => tool.id !== "local-harness");
  const localTool = snapshot.wrapper.tools.find((tool) => tool.id === "local-harness");

  function setNs(next: string) {
    setNamespace(next);
    saveNamespace(next);
  }

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
    const items = (await runAPI("list runs", () => listAgentRuns(token, namespace, 50))) as AgentRunView[] | null;
    if (items) {
      setRuns(items);
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
            <span className="label">Prompt</span>
            <textarea
              className="input textarea"
              rows={4}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Prompt for a new AgentRun on Primaris. The OIDC token is never appended."
            />
          </label>
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
            ) : (
              <span className="muted">composition write is disabled; cannot POST AgentRunProfiles.</span>
            )}
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
          <label className="field">
            <span className="label">AgentRunProfile name (create)</span>
            <input
              className="input"
              value={profileName}
              onChange={(event) => setProfileName(event.target.value)}
              placeholder="herald"
            />
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
      {error ? (
        <div className="banner banner-error" style={{ marginTop: "0.75rem" }}>
          {error}
        </div>
      ) : null}
    </div>
  );
}
