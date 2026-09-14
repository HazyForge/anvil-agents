import { useMemo, useState } from "react";
import { delegateHarness } from "../api/client";
import type { DelegateResult, Prefs, Snapshot } from "../api/types";
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
  const delegatable = useMemo(
    () => snapshot.harnesses.filter((item) => item.delegatable && item.present),
    [snapshot.harnesses],
  );
  const [harness, setHarness] = useState(delegatable[0]?.id ?? "codex");
  const [prompt, setPrompt] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<DelegateResult | null>(null);

  async function onRun() {
    const text = prompt.trim();
    if (!text) {
      setError("prompt is required");
      return;
    }
    setRunning(true);
    setError(null);
    try {
      setResult(await delegateHarness(harness, text));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRunning(false);
    }
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Local</h1>
          <p className="page-sub">Run a program already on this machine. Cluster agents stay on Chat and Runs.</p>
        </div>
      </div>
      <LocalRuntimeCard snapshot={snapshot} busy={busy} onSavePrefs={onSavePrefs} />
      <div className="local-layout">
        <div>
          <div className="card-grid">
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
                  {item.delegatable ? <span className="chip chip-ok">activatable</span> : <span className="chip">inventory</span>}
                  {item.version ? <span className="chip mono">{item.version}</span> : null}
                </div>
                {item.path ? <div className="mono path">{item.path}</div> : <div className="muted">Looked for {item.binaries.join(", ")}</div>}
                <p className="card-notes">{item.notes}</p>
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
        <section className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Activate</h2>
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
              <p className="muted">
                No catalog CLI was found {snapshot.harnessTarget === "wsl" ? "in WSL" : "on PATH"}.
                Install grok, Codex, or OpenCode, or switch Native PATH / Operate on WSL.
              </p>
            ) : (
              <p className="muted">
                Runs via {snapshot.harnessTarget === "wsl" ? "WSL (wsl.exe / distro PATH)" : "native PATH"}.
                Token is not passed.
              </p>
            )}
            <label className="field">
              <span className="label">Prompt</span>
              <textarea
                className="input textarea"
                rows={5}
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                placeholder="On-machine work for the selected CLI. Do not paste secrets."
              />
            </label>
            <div className="btn-row">
              <button
                type="button"
                className="btn btn-primary"
                disabled={running || busy || delegatable.length === 0}
                onClick={() => void onRun()}
              >
                {running ? "Running…" : "Run locally"}
              </button>
            </div>
            {error ? <div className="banner banner-error">{error}</div> : null}
            {result ? (
              <pre className="hint">
                exit {result.exitCode}
                {result.timedOut ? " (timed out)" : ""}
                {result.target ? ` · ${result.target}` : ""}
                {result.wslDistro ? ` · ${result.wslDistro}` : ""}
                {"\n"}
                {result.stdout || result.stderr || "(no output)"}
              </pre>
            ) : null}
          </div>
        </section>
      </div>
    </div>
  );
}
