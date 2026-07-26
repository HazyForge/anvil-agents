import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { NamespaceSwitcher } from "./components/NamespaceSwitcher";
import { LoginGate } from "./components/LoginGate";
import { BoardPage } from "./pages/BoardPage";
import { RunPage } from "./pages/RunPage";
import { AuthCallbackPage } from "./pages/AuthCallbackPage";
import { LibraryHubPage } from "./pages/library/LibraryHubPage";
import { CompositionListPage } from "./pages/library/CompositionListPage";
import { CompositionEditorPage } from "./pages/library/CompositionEditorPage";
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
  const location = useLocation();

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
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Quiet refresh while the console is open.
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
            <div className="app-shell">
              <header className="topbar">
                <div className="brand">
                  <div className="brand-title">{productTitle}</div>
                  <div className="brand-sub">
                    {compositionWrite
                      ? "Operator · composition (GitOps protected)"
                      : compositionRead
                        ? "Observer · library read"
                        : "Observer · runs only"}
                  </div>
                </div>
                <nav className="top-nav" aria-label="Primary">
                  <Link
                    className={`top-nav-link${location.pathname === "/" || location.pathname.includes("/runs/") ? " active" : ""}`}
                    to="/"
                  >
                    Runs
                  </Link>
                  {compositionRead ? (
                    <Link
                      className={`top-nav-link${location.pathname.startsWith("/library") || location.pathname.includes("/profiles") || location.pathname.includes("/skill-sets") || location.pathname.includes("/tool-sets") || location.pathname.includes("/volume") || location.pathname.includes("/harness") || location.pathname.includes("/data-volumes") ? " active" : ""}`}
                      to="/library"
                    >
                      Library
                    </Link>
                  ) : null}
                </nav>
                <NamespaceSwitcher
                  namespaces={namespaces}
                  active={activeNamespace}
                  onSelect={onSelectNamespace}
                  onAdd={onAddNamespace}
                  onRemove={onRemoveNamespace}
                />
                <div className="topbar-actions">
                  <button type="button" className="btn btn-danger" onClick={handleLogout}>
                    Sign out
                  </button>
                </div>
              </header>
              <main className="main">
                <Routes>
                  <Route path="/" element={<BoardPage token={token} namespace={activeNamespace} />} />
                  <Route
                    path="/ns/:namespace/runs/:name"
                    element={<RunPage token={token} onViewNamespace={onViewNamespace} />}
                  />
                  {compositionRead ? (
                    <>
                      <Route
                        path="/library"
                        element={
                          <LibraryHubPage
                            namespace={activeNamespace}
                            writeEnabled={compositionWrite}
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
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </main>
            </div>
          )
        }
      />
    </Routes>
  );
}
