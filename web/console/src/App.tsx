import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { NamespaceSwitcher } from "./components/NamespaceSwitcher";
import { LoginGate } from "./components/LoginGate";
import { BoardPage } from "./pages/BoardPage";
import { RunPage } from "./pages/RunPage";
import { AuthCallbackPage } from "./pages/AuthCallbackPage";
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
                  <div className="brand-sub">Observer · no mutations</div>
                </div>
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
