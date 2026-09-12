import type { Prefs, Snapshot } from "../api/types";

interface Props {
  snapshot: Snapshot;
  busy?: boolean;
  onSavePrefs: (patch: Prefs) => Promise<void> | void;
}

export function LocalRuntimeCard({ snapshot, busy, onSavePrefs }: Props) {
  const selected = snapshot.prefs.harnessTarget || snapshot.harnessTarget;
  const distros = snapshot.wsl.distros?.length
    ? snapshot.wsl.distros
    : snapshot.wsl.defaultDistro
      ? [snapshot.wsl.defaultDistro]
      : [];
  const distro = snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro || "";

  return (
    <section className="panel runtime-card">
      <div className="panel-header">
        <h2 className="panel-title">Local execution</h2>
        <span className={`chip ${snapshot.harnessTarget === "wsl" ? "chip-ok" : ""}`}>
          {snapshot.harnessTarget === "wsl" ? "operate on WSL" : "native PATH"}
        </span>
      </div>
      <div className="panel-body">
        <p className="muted">
          Optional on-machine work, not Primaris chat. Catalog CLIs already installed on native PATH
          or inside WSL can be activated here. A Windows-hosted process uses <code>wsl.exe</code>{" "}
          and the default distro PATH when Operate on WSL is selected. The OIDC token is never copied
          into those processes.
        </p>
        <div className="seg" role="group" aria-label="Harness execution target">
          <button
            type="button"
            className={`seg-btn ${selected === "native" ? "active" : ""}`}
            disabled={busy}
            onClick={() => void onSavePrefs({ harnessTarget: "native" })}
          >
            Native PATH
          </button>
          <button
            type="button"
            className={`seg-btn ${selected === "wsl" ? "active" : ""}`}
            disabled={busy}
            onClick={() => void onSavePrefs({ harnessTarget: "wsl" })}
          >
            Operate on WSL
          </button>
        </div>
        {selected === "wsl" && distros.length > 1 ? (
          <label className="field">
            <span className="label">WSL distro</span>
            <select
              className="select"
              value={distro}
              disabled={busy}
              onChange={(event) => void onSavePrefs({ harnessTarget: "wsl", wslDistro: event.target.value })}
            >
              {distros.map((name) => (
                <option key={name} value={name}>
                  {name}
                  {name === snapshot.wsl.defaultDistro ? " (default)" : ""}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        <div className={`banner ${snapshot.wsl.available || snapshot.wsl.insideWSL ? "banner-ok" : "banner-info"}`}>
          {snapshot.wsl.message ||
            (selected === "wsl"
              ? "WSL is selected. Install a distro or switch to native PATH if discovery is empty."
              : "Native PATH uses this process environment, not the WSL distro.")}
        </div>
      </div>
    </section>
  );
}
