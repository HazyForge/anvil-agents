import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  createAgentRun,
  createAgentRunProfile,
  getAgentRun,
  GROK_BACKEND,
  AUDITOR_GROK_PROFILE,
  DESKTOP_GROK_PEER_PROFILE,
  isBlockedPeerProfile,
  listAgentRuns,
  listRunProfiles,
  pickGrokPeerProfileName,
  type AgentRunView,
  type CompositionDocument,
} from "../api/client";
import type { Snapshot } from "../api/types";
import type { UIConfig } from "../auth/config";
import { MidrunProofPanel } from "../components/MidrunProofPanel";
import { RequestPeerMonitor } from "../components/RequestPeerMonitor";
import { personaLabel } from "../names";
import { loadNamespace, saveNamespace } from "../state/namespace";
import { WRAPPER_PROFILE_NAME } from "../wrapper/intent";
import { grokRequestPeerProofPrompt } from "../wrapper/requestPeer";

interface Props {
  snapshot: Snapshot;
  token: string;
  config: UIConfig;
}

export function WrapperPage({ token, config }: Props) {
  const [params] = useSearchParams();
  const lab = params.get("lab") === "1";
  const fallbackNs = config.defaultNamespaces[0] || "";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [prompt, setPrompt] = useState("");
  const [profileName, setProfileName] = useState("");
  const [peerProfileName, setPeerProfileName] = useState(DESKTOP_GROK_PEER_PROFILE);
  const [runName, setRunName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [requestPeerStatus, setRequestPeerStatus] = useState<string | null>(null);
  const [apiOutput, setApiOutput] = useState("");
  const [runs, setRuns] = useState<AgentRunView[]>([]);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [requestPeerSource, setRequestPeerSource] = useState<string | null>(null);
  const displayedPeerProfile =
    !peerProfileName.trim() || isBlockedPeerProfile(peerProfileName) || peerProfileName.trim() === profileName.trim()
      ? pickGrokPeerProfileName(profiles, profileName) || DESKTOP_GROK_PEER_PROFILE
      : peerProfileName;

  const entityProfiles = useMemo(
    () => profiles.filter((doc) => doc.metadata.name !== WRAPPER_PROFILE_NAME),
    [profiles],
  );

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
        setError(`Could not load runs: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
  }

  useEffect(() => {
    if (!namespace || !token) {
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const items = await listRunProfiles(token, namespace);
        if (!cancelled) {
          setProfiles(items);
        }
      } catch {
        // roster is optional for the run list
      }
      try {
        const items = await listAgentRuns(token, namespace, 50);
        if (!cancelled) {
          setRuns(items);
          setError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setError(`Could not load runs: ${err instanceof Error ? err.message : String(err)}`);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, token]);

  useEffect(() => {
    if (profileName.trim()) {
      return;
    }
    const first = entityProfiles[0]?.metadata.name;
    if (first) {
      setProfileName(first);
    }
  }, [entityProfiles, profileName]);

  useEffect(() => {
    if (!lab) {
      return;
    }
    setPeerProfileName((current) => {
      if (!current.trim() || isBlockedPeerProfile(current) || current.trim() === profileName.trim()) {
        const next = pickGrokPeerProfileName(profiles, profileName) || DESKTOP_GROK_PEER_PROFILE;
        return isBlockedPeerProfile(next) ? DESKTOP_GROK_PEER_PROFILE : next;
      }
      return current;
    });
  }, [lab, profiles, profileName]);

  useEffect(() => {
    if (!lab || !namespace || !token || !requestPeerSource) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshRuns(true);
    }, 4000);
    return () => window.clearInterval(id);
  }, [lab, namespace, token, requestPeerSource]);

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

  async function onGetRun() {
    const name = runName.trim();
    if (!name) {
      setError("Enter a run name");
      return;
    }
    await runAPI("get run", () => getAgentRun(token, namespace, name));
  }

  async function onCreateRun() {
    if (!config.runs.createEnabled) {
      setError("Starting a run is turned off on this server");
      return;
    }
    if (!prompt.trim() || !profileName.trim()) {
      setError("Choose an agent and write what they should do");
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

  async function onStartRequestPeerProof() {
    if (!config.runs.createEnabled) {
      setError("AgentRun create is disabled on this API");
      return;
    }
    const grokProfile = profileName.trim() || AUDITOR_GROK_PROFILE;
    let peerProfile = displayedPeerProfile.trim();
    if (!peerProfile || isBlockedPeerProfile(peerProfile) || peerProfile === grokProfile) {
      peerProfile = pickGrokPeerProfileName(profiles, grokProfile) || DESKTOP_GROK_PEER_PROFILE;
    }
    if (isBlockedPeerProfile(peerProfile) || peerProfile === grokProfile) {
      peerProfile = DESKTOP_GROK_PEER_PROFILE;
    }
    setPeerProfileName(peerProfile);
    setBusy(true);
    setError(null);
    setRequestPeerStatus(null);
    setRequestPeerSource(null);
    try {
      setRequestPeerStatus("Creating grok proof run…");
      const created = await createAgentRun(token, namespace, {
        generateName: "desktop-reqpeer-",
        profileName: grokProfile,
        prompt: grokRequestPeerProofPrompt(peerProfile || undefined),
        backend: GROK_BACKEND,
      });
      const name =
        created && typeof created === "object" && "name" in created && typeof created.name === "string"
          ? created.name
          : "";
      if (!name) {
        throw new Error("API did not return source run name");
      }
      setRequestPeerSource(name);
      setApiOutput(JSON.stringify(created, null, 2));
      setRequestPeerStatus(`Watching ${name} for requestPeer while Running…`);
      await refreshRuns(true);
    } catch (err) {
      setError(`requestPeer proof: ${err instanceof Error ? err.message : String(err)}`);
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
    <div className="human-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Runs</h1>
          <p className="page-sub">Cluster work. See what is running, or start a run.</p>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}

      <section className="panel">
        <div className="panel-header">
          <h2 className="panel-title">Now</h2>
          <span className="muted">{runs.length}</span>
        </div>
        <div className="panel-body">
          {runs.length === 0 ? (
            <p className="muted">No runs yet in this workspace.</p>
          ) : (
            <table className="run-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Agent</th>
                </tr>
              </thead>
              <tbody>
                {runs.slice(0, 20).map((run) => (
                  <tr key={run.name}>
                    <td className="mono">{run.name}</td>
                    <td>{run.phase || "—"}</td>
                    <td>
                      {run.resolvedComposition?.profileRef?.name
                        ? personaLabel(run.resolvedComposition.profileRef.name)
                        : run.backend || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">Start a run</h2>
        </div>
        <div className="panel-body">
          <label className="field">
            <span className="label">Agent</span>
            <select
              className="select"
              value={profileName}
              onChange={(event) => setProfileName(event.target.value)}
            >
              {entityProfiles.length === 0 ? <option value="">Loading agents…</option> : null}
              {entityProfiles.map((doc) => (
                <option key={doc.metadata.name} value={doc.metadata.name}>
                  {personaLabel(doc.metadata.name)}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span className="label">What should they do?</span>
            <textarea
              className="input textarea"
              rows={3}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Describe the work"
            />
          </label>
          {config.runs.createEnabled ? (
            <div className="btn-row">
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy || !namespace || !prompt.trim() || !profileName.trim()}
                onClick={() => void onCreateRun()}
              >
                {busy ? "Starting…" : "Start run"}
              </button>
            </div>
          ) : (
            <p className="muted">Starting a run is turned off on this server.</p>
          )}
        </div>
      </section>

      {lab ? (
        <div className="lab-chrome">
          <div className="banner banner-warn" style={{ marginTop: "0.75rem" }}>
            Lab controls. Not the default Runs page.
          </div>
          <MidrunProofPanel
            token={token}
            namespace={namespace}
            onLoaded={(pair) => setApiOutput(JSON.stringify(pair, null, 2))}
          />
          <section className="panel" style={{ marginTop: "0.75rem" }}>
            <div className="panel-header">
              <h2 className="panel-title">Grok requestPeer proof</h2>
            </div>
            <div className="panel-body">
              <label className="field">
                <span className="label">grok AgentRunProfile</span>
                <input
                  className="input"
                  value={profileName}
                  onChange={(event) => setProfileName(event.target.value)}
                  placeholder={AUDITOR_GROK_PROFILE}
                />
              </label>
              <label className="field">
                <span className="label">peerProfileName</span>
                <input
                  className="input"
                  value={displayedPeerProfile}
                  onChange={(event) => {
                    const next = event.target.value;
                    if (!next.trim() || isBlockedPeerProfile(next)) {
                      setPeerProfileName(DESKTOP_GROK_PEER_PROFILE);
                      return;
                    }
                    setPeerProfileName(next);
                  }}
                  placeholder={DESKTOP_GROK_PEER_PROFILE}
                />
              </label>
              {config.runs.createEnabled ? (
                <button
                  type="button"
                  className="btn"
                  disabled={busy || !namespace || !profileName.trim()}
                  onClick={() => void onStartRequestPeerProof()}
                >
                  {busy ? "Starting grok proof…" : "Start grok requestPeer proof"}
                </button>
              ) : null}
              {requestPeerStatus ? <p className="muted">{requestPeerStatus}</p> : null}
            </div>
          </section>
          <section className="panel" style={{ marginTop: "0.75rem" }}>
            <div className="panel-header">
              <h2 className="panel-title">Lab API</h2>
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
              <label className="field">
                <span className="label">AgentRun name (get)</span>
                <input className="input" value={runName} onChange={(event) => setRunName(event.target.value)} />
              </label>
              <div className="btn-row">
                <button type="button" className="btn" disabled={busy || !namespace} onClick={() => void refreshRuns()}>
                  List runs
                </button>
                <button type="button" className="btn" disabled={busy || !namespace} onClick={() => void onGetRun()}>
                  Get run
                </button>
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
              </div>
              {apiOutput ? <pre className="hint">{apiOutput}</pre> : null}
            </div>
          </section>
          {requestPeerSource ? (
            <RequestPeerMonitor
              token={token}
              namespace={namespace}
              sourceRun={requestPeerSource}
              createEnabled={Boolean(config.runs.createEnabled)}
              profiles={profiles}
            />
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
