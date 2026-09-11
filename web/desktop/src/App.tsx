import { useCallback, useEffect, useMemo, useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { fetchSnapshot, savePrefs } from "./api/client";
import type { Prefs, Snapshot } from "./api/types";
import { ChatPage } from "./pages/ChatPage";
import { ClusterPage } from "./pages/ClusterPage";
import { HarnessesPage } from "./pages/HarnessesPage";
import { PRODUCT_TITLE } from "./product";

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const next = await fetchSnapshot();
      setSnapshot(next);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

	useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    document.title = snapshot?.productTitle ?? PRODUCT_TITLE;
  }, [snapshot]);

  const persist = useCallback(
    async (prefs: Prefs) => {
      setBusy(true);
      try {
        const next = await savePrefs(prefs);
        setSnapshot(next);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setBusy(false);
      }
    },
    [],
  );

  const presentCount = useMemo(
    () => snapshot?.harnesses.filter((item) => item.present).length ?? 0,
    [snapshot],
  );

  const contextLabel = snapshot?.cluster.selectedContext || snapshot?.cluster.currentContext || "no context";
  const operatorOk = snapshot?.cluster.operator.apiGroupPresent;

  return (
    <div className="desktop-shell">
      <div className="titlebar">
        <div className="traffic" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <div className="titlebar-title">{snapshot?.productTitle ?? PRODUCT_TITLE}</div>
        <div className="titlebar-meta">
          <span className={`pill ${operatorOk ? "pill-ok" : "pill-mute"}`}>{contextLabel}</span>
          <button type="button" className="btn btn-ghost" onClick={() => void refresh()} disabled={busy}>
            {busy ? "Refreshing" : "Refresh"}
          </button>
        </div>
      </div>
      <nav className="rail">
        <NavLink to="/" end className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Local
          <span className="rail-count">{presentCount}</span>
        </NavLink>
        <NavLink to="/cluster" className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Cluster
        </NavLink>
        <NavLink to="/chat" className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Chat
        </NavLink>
      </nav>
      <main className="desktop-main">
        {error ? <div className="banner banner-error">{error}</div> : null}
        {snapshot ? (
          <Routes>
            <Route path="/" element={<HarnessesPage snapshot={snapshot} />} />
            <Route path="/cluster" element={<ClusterPage snapshot={snapshot} onSave={persist} busy={busy} />} />
            <Route path="/chat" element={<ChatPage snapshot={snapshot} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        ) : (
          <div className="empty">Loading local harnesses and kubecontexts…</div>
        )}
      </main>
    </div>
  );
}
