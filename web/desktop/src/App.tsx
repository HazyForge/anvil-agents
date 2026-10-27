import { useCallback, useEffect, useMemo, useState } from "react";
import { NavLink, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { fetchSnapshot, savePrefs } from "./api/client";
import type { Prefs, Snapshot } from "./api/types";
import { clearUIConfigCache, loadUIConfig, type UIConfig } from "./auth/config";
import { beginLogin, ensureAccessToken, logout } from "./auth/oidc";
import { clearLegacyToken, loadSession } from "./auth/session";
import { LoginGate } from "./components/LoginGate";
import { AuthCallbackPage } from "./pages/AuthCallbackPage";
import { EntityChatPage } from "./pages/EntityChatPage";
import { HarnessesPage } from "./pages/HarnessesPage";
import { LocalChatPage } from "./pages/LocalChatPage";
import { ManagerPage } from "./pages/ManagerPage";
import { WrapperPage } from "./pages/WrapperPage";
import { DesktopIcon, HazyForgeMark } from "./components/AgentAvatar";
import { PRODUCT_TITLE } from "./product";

export default function App() {
  const location = useLocation();
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState(() => loadSession()?.accessToken ?? "");
  const [config, setConfig] = useState<UIConfig | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [chatLocation, setChatLocation] = useState<'local' | 'remote'>(() => {
    try { return localStorage.getItem('anvil-agents-desktop.chat-location') === 'remote' ? 'remote' : 'local'; } catch { return 'local'; }
  });
  const [localChatBusy, setLocalChatBusy] = useState(false);

  const refresh = useCallback(async () => {
    setBusy(true);
    try {
      const next = await fetchSnapshot();
      setSnapshot(next);
      setError(null);
      if (next.prefs.apiOrigin) {
        try {
          const ui = await loadUIConfig(true);
          setConfig(ui);
          setConfigError(null);
        } catch (err) {
          setConfig(null);
          setConfigError(err instanceof Error ? err.message : String(err));
        }
      } else {
        clearUIConfigCache();
        setConfig(null);
        setConfigError(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    clearLegacyToken();
    void refresh();
  }, [refresh]);

  useEffect(() => {
    document.title = snapshot?.productTitle ?? PRODUCT_TITLE;
  }, [snapshot]);

  useEffect(() => {
    if (!token) {
      return;
    }
    const id = window.setInterval(() => {
      void ensureAccessToken().then((access) => {
        if (!access) {
          setToken("");
          return;
        }
        setToken(access);
      });
    }, 60_000);
    return () => window.clearInterval(id);
  }, [token]);

  useEffect(() => {
    let cancelled = false;
    void ensureAccessToken().then((access) => {
      if (!cancelled) {
        setToken(access ?? "");
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const persistPrefs = useCallback(async (patch: Prefs) => {
    setBusy(true);
    try {
      if (patch.apiOrigin !== undefined) {
        clearUIConfigCache();
      }
      const next = await savePrefs(patch);
      setSnapshot(next);
      setError(null);
      if (next.prefs.apiOrigin) {
        const ui = await loadUIConfig(true);
        setConfig(ui);
        setConfigError(null);
      } else if (patch.apiOrigin !== undefined) {
        setConfig(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  const persistOrigin = useCallback(
    async (origin: string) => {
      await persistPrefs({ apiOrigin: origin.trim() });
    },
    [persistPrefs],
  );

  const handleAuthenticated = useCallback((accessToken: string) => {
    setToken(accessToken);
    setError(null);
  }, []);

  const presentCount = useMemo(
    () => snapshot?.harnesses.filter((item) => item.present).length ?? 0,
    [snapshot],
  );

  const freshLocalAccessToken = useCallback(async () => {
    const access = await ensureAccessToken();
    setToken(access ?? '');
    return access;
  }, []);

  const originHost = snapshot?.api.origin?.replace(/^https?:\/\//, "") || "no API";
  const signedIn = Boolean(token);
  const onLocal = location.pathname === "/local";

  const login = snapshot ? (
    <LoginGate
      apiOrigin={snapshot.prefs.apiOrigin || ""}
      apiMessage={snapshot.api.message}
      apiReachable={snapshot.api.reachable}
      issuer={config?.oidc.issuer}
      oidcClientId={config?.oidc.clientId}
      oidcClientSource={config?.desktop?.oidcClientSource}
      stubSession={Boolean(config?.desktop?.stubSession)}
      error={configError}
      busy={busy}
      onSaveOrigin={persistOrigin}
      onAuthenticated={handleAuthenticated}
    />
  ) : (
    <div className="empty empty-loading">Loading Anvil Agents Desktop…</div>
  );

  const remoteChat = snapshot && signedIn && config ? (
    <EntityChatPage token={token} config={config} />
  ) : (
    login
  );
  const chooseLocation = (next: 'local' | 'remote') => {
    setChatLocation(next);
    try { localStorage.setItem('anvil-agents-desktop.chat-location', next); } catch { /* In-memory selection remains. */ }
  };
  const chat = <>
    <div className="chat-location" role="group" aria-label="Chat execution location">
      <span className="chat-location-label">Location</span>
      <div className="seg">
        <button type="button" aria-pressed={chatLocation === 'local'} className={`seg-btn ${chatLocation === 'local' ? 'active' : ''}`} disabled={localChatBusy} onClick={() => chooseLocation('local')}>Local</button>
        <button type="button" aria-pressed={chatLocation === 'remote'} className={`seg-btn ${chatLocation === 'remote' ? 'active' : ''}`} disabled={localChatBusy} onClick={() => chooseLocation('remote')}>Primaris</button>
      </div>
      <span className="chat-location-hint">{chatLocation === 'local' ? 'Harnesses on this machine' : 'Agents on the Primaris cluster'}</span>
    </div>
    {chatLocation === 'local' ? snapshot ? <LocalChatPage snapshot={snapshot} onBusyChange={setLocalChatBusy} signedIn={signedIn} config={config} getAccessToken={freshLocalAccessToken}/> : <div className="empty empty-loading">Loading local harnesses…</div> : remoteChat}
  </>;

  return (
    <div className={`desktop-shell ${location.pathname === "/chat" || location.pathname === "/manager" ? "desktop-shell-chat" : ""}`}>
      <aside className="sidebar">
        <div className="brand-lockup" title={snapshot?.productTitle ?? PRODUCT_TITLE}>
          <HazyForgeMark/>
          <div className="wordmark"><strong>Anvil Agents</strong><small>Desktop</small></div>
        </div>
        <nav className="rail" aria-label="Primary">
          <NavLink to="/chat" className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
            <DesktopIcon kind="chat"/><span className="rail-label">Chat</span>
          </NavLink>
          <NavLink to="/manager" aria-disabled={localChatBusy} onClick={e => {if(localChatBusy) e.preventDefault();}} className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
            <DesktopIcon kind="manager"/><span className="rail-label">Manager</span>
          </NavLink>
          <NavLink to="/wrapper" aria-disabled={localChatBusy} onClick={e => {if(localChatBusy) e.preventDefault();}} className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
            <DesktopIcon kind="activity"/><span className="rail-label">Activity</span>
          </NavLink>
          <NavLink to="/local" aria-disabled={localChatBusy} onClick={e => {if(localChatBusy) e.preventDefault();}} className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
            <DesktopIcon kind="harness"/><span className="rail-label">Harnesses</span>
            <span className="rail-count">{presentCount}</span>
          </NavLink>
        </nav>
        <div className="sidebar-foot">
          <div className="sidebar-status" aria-label="Connection">
            <span className={`pill ${snapshot?.api.reachable ? "pill-ok" : "pill-mute"}`} title={snapshot?.api.message}>{originHost}</span>
            {onLocal ? (
              <span className={`pill ${snapshot?.harnessTarget === "wsl" ? "pill-ok" : "pill-mute"}`}>
                {snapshot?.harnessTarget === "wsl"
                  ? `WSL${snapshot.wsl.defaultDistro || snapshot.prefs.wslDistro ? ` · ${snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro}` : ""}`
                  : "native PATH"}
              </span>
            ) : null}
            <span className={`pill ${signedIn ? "pill-ok" : "pill-mute"}`}>{signedIn ? "signed in" : "signed out"}</span>
          </div>
          <div className="sidebar-actions">
            <button type="button" className="btn btn-ghost" onClick={() => void refresh()} disabled={busy}>
              <DesktopIcon kind="refresh"/><span>{busy ? "Refreshing" : "Refresh"}</span>
            </button>
            {signedIn ? (
              <button type="button" className="btn btn-ghost" disabled={localChatBusy} onClick={() => void logout()}>
                <DesktopIcon kind="signout"/><span>Sign out</span>
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-ghost"
                disabled={localChatBusy}
                onClick={() => void beginLogin(location.pathname + location.search)}
              >
                <DesktopIcon kind="signin"/><span>Sign in</span>
              </button>
            )}
          </div>
        </div>
      </aside>
      <main className="desktop-main">
        {error ? <div className="banner banner-error">{error}</div> : null}
        <Routes>
          <Route path="/auth/callback" element={<AuthCallbackPage onAuthenticated={handleAuthenticated} />} />
          <Route path="/callback" element={<AuthCallbackPage onAuthenticated={handleAuthenticated} />} />
          <Route path="/chat" element={chat} />
          <Route path="/manager" element={<ManagerPage />} />
          <Route
            path="/wrapper"
            element={
              snapshot && signedIn && config ? (
                <WrapperPage snapshot={snapshot} token={token} config={config} />
              ) : (
                login
              )
            }
          />
          <Route
            path="/local"
            element={
              snapshot ? (
                <HarnessesPage snapshot={snapshot} busy={busy} onSavePrefs={persistPrefs} />
              ) : (
                <div className="empty empty-loading">Loading Anvil Agents Desktop…</div>
              )
            }
          />
          <Route path="/" element={<Navigate to="/chat" replace />} />
          <Route path="*" element={<Navigate to="/chat" replace />} />
        </Routes>
      </main>
    </div>
  );
}
