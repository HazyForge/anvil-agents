import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { BrandLockup } from "./components/BrandMark";
import { DockNav } from "./components/DockNav";
import { Icon } from "./components/Icon";
import { NamespaceTree } from "./components/NamespaceTree";
import { LoginGate } from "./components/LoginGate";
import { BoardPage } from "./pages/BoardPage";
import { RunPage } from "./pages/RunPage";
import { AuthCallbackPage } from "./pages/AuthCallbackPage";
import { LibraryHubPage } from "./pages/library/LibraryHubPage";
import { CompositionListPage } from "./pages/library/CompositionListPage";
import { CompositionEditorPage } from "./pages/library/CompositionEditorPage";
import { ControlsPage } from "./pages/ControlsPage";
import { ProfileCardsPage } from "./pages/profiles/ProfileCardsPage";
import { ProfileEditorPage } from "./pages/profiles/ProfileEditorPage";
import { ChatPage } from "./pages/ChatPage";
import { loadUIConfig } from "./auth/config";
import { ensureAccessToken, logout } from "./auth/oidc";
import { clearLegacyToken, loadSession } from "./auth/session";
import {
  loadActiveNamespace,
  loadNamespaces,
  saveActiveNamespace,
  saveNamespaces,
  uniqueNamespaces,
} from "./state/namespaces";

export default function App() {
  const [token, setToken] = useState(() => loadSession()?.accessToken ?? "");
  const [namespaces, setNamespaces] = useState(() => loadNamespaces());
  const [activeNamespace, setActiveNamespace] = useState(() =>
    loadActiveNamespace(loadNamespaces()),
  );
  const [productTitle, setProductTitle] = useState("Anvil Agents Console");
  const [authError, setAuthError] = useState<string | null>(null);
  const [compositionRead, setCompositionRead] = useState(false);
  const [compositionWrite, setCompositionWrite] = useState(false);
  const [controlsRead, setControlsRead] = useState(false);
  const [controlsWrite, setControlsWrite] = useState(false);
  const [externalTriggersEnabled, setExternalTriggersEnabled] = useState(false);
  const [chatEnabled, setChatEnabled] = useState(false);
  /** Avoid catch-all redirect until /api/v1/ui-config finishes — deep links like /harness-profiles/new must not bounce to /. */
  const [configReady, setConfigReady] = useState(false);
  /** Narrow layouts: the namespace tree opens as a drawer over the page. */
  const [navOpen, setNavOpen] = useState(false);
  const location = useLocation();

  useEffect(() => {
    setNavOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!navOpen) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setNavOpen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [navOpen]);

  useEffect(() => {
    clearLegacyToken();
    let cancelled = false;
    void (async () => {
      try {
        const config = await loadUIConfig();
        if (cancelled) {
          return;
        }
        setProductTitle(config.productTitle);
        setCompositionRead(config.composition.readEnabled);
        setCompositionWrite(config.composition.writeEnabled);
        setControlsRead(config.controls?.readEnabled ?? false);
        setControlsWrite(config.controls?.writeEnabled ?? false);
        setExternalTriggersEnabled(config.externalTriggers?.enabled ?? false);
        setChatEnabled(config.chat?.enabled ?? false);
        if (config.defaultNamespaces.length > 0) {
          setNamespaces((prev) => {
            const next = uniqueNamespaces([...config.defaultNamespaces, ...prev]);
            saveNamespaces(next);
            return next;
          });
        }
        const access = await ensureAccessToken();
        if (!cancelled && access) {
          setToken(access);
        } else if (!cancelled && !access) {
          setToken("");
        }
      } catch (err) {
        if (!cancelled) {
          setAuthError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (!cancelled) {
          setConfigReady(true);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

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

  const handleAuthenticated = useCallback((accessToken: string) => {
    setToken(accessToken);
    setAuthError(null);
  }, []);

  const handleLogout = useCallback(() => {
    void logout();
  }, []);

  const onSelectNamespace = useCallback((ns: string) => {
    setActiveNamespace(ns);
    saveActiveNamespace(ns);
  }, []);

  const onAddNamespace = useCallback((ns: string) => {
    setNamespaces((prev) => {
      const next = uniqueNamespaces([...prev, ns]);
      saveNamespaces(next);
      return next;
    });
    setActiveNamespace(ns);
    saveActiveNamespace(ns);
  }, []);

  const onRemoveNamespace = useCallback((ns: string) => {
    setNamespaces((prev) => {
      const next = prev.filter((item) => item !== ns);
      saveNamespaces(next);
      setActiveNamespace((active) => {
        if (active !== ns && next.includes(active)) {
          return active;
        }
        const replacement = next[0] ?? "";
        saveActiveNamespace(replacement);
        return replacement;
      });
      return next;
    });
  }, []);

  const onViewNamespace = useCallback((ns: string) => {
    setNamespaces((prev) => {
      const next = uniqueNamespaces([...prev, ns]);
      saveNamespaces(next);
      return next;
    });
    setActiveNamespace(ns);
    saveActiveNamespace(ns);
  }, []);

  const authed = useMemo(() => Boolean(token.trim()), [token]);

  return (
    <Routes>
      <Route path="/auth/callback" element={<AuthCallbackPage onAuthenticated={handleAuthenticated} />} />
      <Route
        path="*"
        element={
          !authed ? (
            <LoginGate error={authError} />
          ) : (
            <div className={`app-shell${navOpen ? " nav-open" : ""}`}>
              <a className="skip-link" href="#console-main">
                Skip to content
              </a>
              <div className="rail">
                <header className="rail-brand">
                  <BrandLockup title={productTitle} />
                </header>
                <button
                  type="button"
                  className="rail-ns-toggle"
                  aria-expanded={navOpen}
                  aria-controls="console-namespace-tree"
                  onClick={() => setNavOpen((open) => !open)}
                >
                  <Icon name="folder" />
                  <span className="rail-ns-copy">
                    <small>Namespace</small>
                    <span className="mono">{activeNamespace || "none"}</span>
                  </span>
                  <Icon name="chevron" className="rail-ns-chevron" />
                </button>
                <NamespaceTree
                  namespaces={namespaces}
                  activeNamespace={activeNamespace}
                  compositionRead={compositionRead}
                  controlsRead={controlsRead}
                  externalTriggersEnabled={externalTriggersEnabled}
                  chatEnabled={chatEnabled}
                  onSelectNamespace={onSelectNamespace}
                  onAddNamespace={onAddNamespace}
                  onRemoveNamespace={onRemoveNamespace}
                />
                <DockNav
                  compositionRead={compositionRead}
                  controlsRead={controlsRead}
                  chatEnabled={chatEnabled}
                />
                <div className="rail-foot">
                  <div className="identity">
                    <span className="identity-tile" aria-hidden="true">
                      <Icon name="user" />
                    </span>
                    <div className="identity-copy brand-sub">
                      <strong>
                        {compositionWrite
                          ? "Operator · profiles"
                          : compositionRead
                            ? "Observer · profiles + library"
                            : "Observer · runs only"}
                      </strong>
                      {activeNamespace ? <span className="mono">{activeNamespace}</span> : null}
                    </div>
                  </div>
                  <button type="button" className="signout" onClick={handleLogout}>
                    <Icon name="logout" />
                    <span className="signout-label">Sign out</span>
                  </button>
                </div>
              </div>
              {navOpen ? (
                <button
                  type="button"
                  className="rail-scrim"
                  aria-label="Close namespaces"
                  onClick={() => setNavOpen(false)}
                />
              ) : null}
              <div className="workspace">
                <main className="main" id="console-main">
                  <Routes>
                    <Route path="/" element={<BoardPage token={token} namespace={activeNamespace} />} />
                    <Route
                      path="/ns/:namespace/runs/:name"
                      element={<RunPage token={token} onViewNamespace={onViewNamespace} />}
                    />
                    {chatEnabled ? (
                      <>
                        <Route
                          path="/chat"
                          element={
                            <ChatPage
                              token={token}
                              namespace={activeNamespace}
                              onViewNamespace={onViewNamespace}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/chat"
                          element={
                            <ChatPage
                              token={token}
                              namespace={activeNamespace}
                              onViewNamespace={onViewNamespace}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/chat/:threadId"
                          element={
                            <ChatPage
                              token={token}
                              namespace={activeNamespace}
                              onViewNamespace={onViewNamespace}
                            />
                          }
                        />
                      </>
                    ) : null}
                    {controlsRead ? (
                      <Route
                        path="/controls"
                        element={<ControlsPage token={token} writeEnabled={controlsWrite} />}
                      />
                    ) : null}
                    {compositionRead ? (
                      <>
                        <Route
                          path="/profiles"
                          element={
                            <ProfileCardsPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                        <Route
                          path="/profiles/new"
                          element={
                            <ProfileEditorPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/profiles"
                          element={
                            <ProfileCardsPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/profiles/:name"
                          element={
                            <ProfileEditorPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                        <Route
                          path="/library"
                          element={
                            <LibraryHubPage
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                              externalTriggersEnabled={externalTriggersEnabled}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/:kind"
                          element={
                            <CompositionListPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                        <Route
                          path="/ns/:namespace/:kind/:name"
                          element={
                            <CompositionEditorPage
                              token={token}
                              namespace={activeNamespace}
                              writeEnabled={compositionWrite}
                            />
                          }
                        />
                      </>
                    ) : null}
                    <Route path="/cards/*" element={<Navigate to="/profiles" replace />} />
                    {!configReady ? (
                      <Route path="*" element={<div className="empty empty-loading">Loading console…</div>} />
                    ) : (
                      <Route path="*" element={<Navigate to="/" replace />} />
                    )}
                  </Routes>
                </main>
              </div>
            </div>
          )
        }
      />
    </Routes>
  );
}
