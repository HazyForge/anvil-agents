import { useCallback, useEffect, useMemo, useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { fetchSnapshot, savePrefs } from "./api/client";
import type { Snapshot } from "./api/types";
import { clearUIConfigCache, loadUIConfig, type UIConfig } from "./auth/config";
import { ensureAccessToken, logout } from "./auth/oidc";
import { clearLegacyToken, loadSession } from "./auth/session";
import { LoginGate } from "./components/LoginGate";
import { AuthCallbackPage } from "./pages/AuthCallbackPage";
import { EntityChatPage } from "./pages/EntityChatPage";
import { HarnessesPage } from "./pages/HarnessesPage";
import { WrapperPage } from "./pages/WrapperPage";
import { PRODUCT_TITLE } from "./product";

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState(() => loadSession()?.accessToken ?? "");
  const [config, setConfig] = useState<UIConfig | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);

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

  const persistOrigin = useCallback(async (origin: string) => {
    setBusy(true);
    try {
      clearUIConfigCache();
      const next = await savePrefs({ apiOrigin: origin.trim() });
      setSnapshot(next);
      setError(null);
      if (next.prefs.apiOrigin) {
        const ui = await loadUIConfig(true);
        setConfig(ui);
        setConfigError(null);
      } else {
        setConfig(null);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }, []);

  const handleAuthenticated = useCallback((accessToken: string) => {
    setToken(accessToken);
    setError(null);
  }, []);

  const presentCount = useMemo(
    () => snapshot?.harnesses.filter((item) => item.present).length ?? 0,
    [snapshot],
  );

  const originHost = snapshot?.api.origin?.replace(/^https?:\/\//, "") || "no API";
  const signedIn = Boolean(token);

  const login = snapshot ? (
    <LoginGate
      apiOrigin={snapshot.prefs.apiOrigin || ""}
      apiMessage={snapshot.api.message}
      apiReachable={snapshot.api.reachable}
      issuer={config?.oidc.issuer}
      stubSession={Boolean(config?.desktop?.stubSession)}
      error={configError}
      busy={busy}
      onSaveOrigin={persistOrigin}
      onAuthenticated={handleAuthenticated}
    />
  ) : (
    <div className="empty">Loading local harnesses…</div>
  );

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
          <span className={`pill ${snapshot?.api.reachable ? "pill-ok" : "pill-mute"}`}>{originHost}</span>
          <span className={`pill ${signedIn ? "pill-ok" : "pill-mute"}`}>{signedIn ? "signed in" : "signed out"}</span>
          <button type="button" className="btn btn-ghost" onClick={() => void refresh()} disabled={busy}>
            {busy ? "Refreshing" : "Refresh"}
          </button>
          {signedIn ? (
            <button type="button" className="btn btn-ghost" onClick={() => void logout()}>
              Sign out
            </button>
          ) : (
            <NavLink to="/wrapper" className="btn btn-ghost">
              Sign in
            </NavLink>
          )}
        </div>
      </div>
      <nav className="rail">
        <NavLink to="/" end className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Local
          <span className="rail-count">{presentCount}</span>
        </NavLink>
        <NavLink to="/chat" className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Chat
        </NavLink>
        <NavLink to="/wrapper" className={({ isActive }) => (isActive ? "rail-link active" : "rail-link")}>
          Wrapper
        </NavLink>
      </nav>
      <main className="desktop-main">
        {error ? <div className="banner banner-error">{error}</div> : null}
        <Routes>
          <Route path="/auth/callback" element={<AuthCallbackPage onAuthenticated={handleAuthenticated} />} />
          <Route path="/callback" element={<AuthCallbackPage onAuthenticated={handleAuthenticated} />} />
          <Route
            path="/"
            element={snapshot ? <HarnessesPage snapshot={snapshot} /> : <div className="empty">Loading local harnesses…</div>}
          />
          <Route
            path="/chat"
            element={
              snapshot && signedIn && config ? (
                <EntityChatPage snapshot={snapshot} token={token} config={config} />
              ) : (
                login
              )
            }
          />
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
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}
