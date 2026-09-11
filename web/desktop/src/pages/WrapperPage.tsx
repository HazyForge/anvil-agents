import { useMemo, useState } from "react";
import {
  callChat,
  createAgentRun,
  delegateHarness,
  getAgentRun,
  listAgentRuns,
  listRunProfiles,
  type AgentRunView,
  type CompositionDocument,
} from "../api/client";
import type { DelegateResult, Snapshot } from "../api/types";
import type { UIConfig } from "../auth/config";
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
  const [harness, setHarness] = useState(
    snapshot.harnesses.find((item) => item.present && item.delegatable)?.id ?? "codex",
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [apiOutput, setApiOutput] = useState("");
  const [delegateOutput, setDelegateOutput] = useState<DelegateResult | null>(null);
  const [runs, setRuns] = useState<AgentRunView[]>([]);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);

  const delegatable = useMemo(
    () => snapshot.harnesses.filter((item) => item.delegatable && item.present),
    [snapshot.harnesses],
  );

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

  async function onChat() {
    const path = config.chat?.path || "/chat";
    await runAPI("chat", () => callChat(token, namespace, path));
  }

  async function onDelegate(extraPrompt?: string) {
    const text = (extraPrompt ?? prompt).trim();
    if (!text) {
      setError("prompt is required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const result = await delegateHarness(harness, text);
      setDelegateOutput(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onFetchThenDelegate() {
    setBusy(true);
    setError(null);
    try {
      const items = await listAgentRuns(token, namespace, 20);
      setRuns(items);
      const summary = JSON.stringify(
        items.map((item) => ({ name: item.name, phase: item.phase, backend: item.backend, error: item.error })),
        null,
        2,
      );
      setApiOutput(JSON.stringify(items, null, 2));
      const combined = [prompt.trim(), "Recent AgentRuns (metadata only; no OIDC token):", summary]
        .filter(Boolean)
        .join("\n\n");
      const result = await delegateHarness(harness, combined);
      setDelegateOutput(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Wrapper agent</h1>
          <p className="page-sub">{snapshot.wrapper.message}</p>
        </div>
      </div>

      <div className="split">
        {snapshot.wrapper.tools.map((tool) => (
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
              rows={6}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Work for the API or a local harness. The OIDC token is never appended."
            />
          </label>
        </div>
      </section>

      <div className="split" style={{ marginTop: "0.75rem" }}>
        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">anvil-api</h2>
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
              {config.runs.createEnabled ? (
                <button
                  type="button"
                  className="btn"
                  disabled={busy || !namespace || !prompt.trim() || !profileName.trim()}
                  onClick={() => void onCreateRun()}
                >
                  Create run
                </button>
              ) : (
                <span className="muted">Append-only create is disabled on this API.</span>
              )}
              {config.chat?.enabled ? (
                <button type="button" className="btn" disabled={busy || !namespace} onClick={() => void onChat()}>
                  Chat
                </button>
              ) : (
                <span className="muted">Chat is not enabled on this API.</span>
              )}
            </div>
            <label className="field">
              <span className="label">AgentRun name (get)</span>
              <input className="input" value={runName} onChange={(event) => setRunName(event.target.value)} />
            </label>
            {config.runs.createEnabled ? (
              <label className="field">
                <span className="label">Profile name (create)</span>
                {profiles.length > 0 ? (
                  <select className="select" value={profileName} onChange={(event) => setProfileName(event.target.value)}>
                    <option value="">select a profile</option>
                    {profiles.map((item) => (
                      <option key={item.metadata.name} value={item.metadata.name}>
                        {item.metadata.name}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    className="input"
                    value={profileName}
                    onChange={(event) => setProfileName(event.target.value)}
                    placeholder="AgentRunProfile name"
                  />
                )}
              </label>
            ) : null}
            {runs.length > 0 ? (
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
            ) : null}
            {apiOutput ? <pre className="hint">{apiOutput}</pre> : null}
          </div>
        </section>

        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">local-harness</h2>
          </div>
          <div className="panel-body">
            <label className="field">
              <span className="label">Harness</span>
              <select className="select" value={harness} onChange={(event) => setHarness(event.target.value)}>
                {delegatable.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.displayName}
                    {item.version ? ` (${item.version})` : ""}
                  </option>
                ))}
              </select>
            </label>
            {delegatable.length === 0 ? (
              <p className="muted">No delegatable harness CLI was found on PATH.</p>
            ) : null}
            <div className="btn-row">
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy || delegatable.length === 0}
                onClick={() => void onDelegate()}
              >
                Delegate prompt
              </button>
              <button
                type="button"
                className="btn"
                disabled={busy || delegatable.length === 0 || !namespace}
                onClick={() => void onFetchThenDelegate()}
              >
                List runs, then delegate
              </button>
            </div>
            {delegateOutput ? (
              <pre className="hint">
                exit {delegateOutput.exitCode}
                {delegateOutput.timedOut ? " (timed out)" : ""}
                {"\n"}
                {delegateOutput.stdout || delegateOutput.stderr || "(no output)"}
              </pre>
            ) : null}
          </div>
        </section>
      </div>
      {error ? <div className="banner banner-error" style={{ marginTop: "0.75rem" }}>{error}</div> : null}
    </div>
  );
}
