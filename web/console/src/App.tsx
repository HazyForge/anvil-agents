import { useCallback, useMemo, useState } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { NamespaceSwitcher } from "./components/NamespaceSwitcher";
import { TokenGate } from "./components/TokenGate";
import { BoardPage } from "./pages/BoardPage";
import { RunPage } from "./pages/RunPage";
import { clearToken, loadToken, saveToken } from "./auth/token";
import {
  loadActiveNamespace,
  loadNamespaces,
  saveActiveNamespace,
  saveNamespaces,
  uniqueNamespaces,
} from "./state/namespaces";

export default function App() {
  const [token, setToken] = useState(() => loadToken());
  const [namespaces, setNamespaces] = useState(() => loadNamespaces());
  const [activeNamespace, setActiveNamespace] = useState(() =>
    loadActiveNamespace(loadNamespaces()),
  );

  const handleToken = useCallback((value: string) => {
    saveToken(value);
    setToken(value.trim());
  }, []);

  const handleLogout = useCallback(() => {
    clearToken();
    setToken("");
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

  if (!authed) {
    return <TokenGate initialToken={token} onSubmit={handleToken} />;
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-title">Anvil Agents Console</div>
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
            Clear token
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
  );
}
