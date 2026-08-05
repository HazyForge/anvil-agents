import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { COMPOSITION_KINDS } from "../api/types.composition";

export type TreeLeafId = "agent-runs" | string;

interface Props {
  namespaces: string[];
  activeNamespace: string;
  compositionRead: boolean;
  controlsRead: boolean;
  onSelectNamespace: (namespace: string) => void;
  onAddNamespace: (namespace: string) => void;
  onRemoveNamespace: (namespace: string) => void;
}

function leafForPath(pathname: string): TreeLeafId {
  if (pathname === "/" || pathname.includes("/runs/")) {
    return "agent-runs";
  }
  if (pathname.startsWith("/controls")) {
    return "controls";
  }
  if (pathname.startsWith("/profiles") || /\/ns\/[^/]+\/profiles(\/|$)/.test(pathname)) {
    return "profiles";
  }
  if (pathname.startsWith("/library")) {
    return "library";
  }
  const match = pathname.match(/\/ns\/[^/]+\/([^/]+)/);
  if (match?.[1] && match[1] !== "runs") {
    return match[1];
  }
  return "agent-runs";
}

function pathForLeaf(namespace: string, leaf: TreeLeafId): string {
  if (leaf === "agent-runs") {
    return "/";
  }
  if (leaf === "library") {
    return "/library";
  }
  if (leaf === "profiles") {
    return "/profiles";
  }
  return `/ns/${encodeURIComponent(namespace)}/${leaf}`;
}

/** CRD subfolders under each namespace folder. AgentRuns is always first. */
function crdLeaves(compositionRead: boolean): Array<{ id: TreeLeafId; label: string; kind: string }> {
  const leaves: Array<{ id: TreeLeafId; label: string; kind: string }> = [
    { id: "agent-runs", label: "Agent runs", kind: "AgentRun" },
  ];
  if (!compositionRead) {
    return leaves;
  }
  for (const info of COMPOSITION_KINDS) {
    leaves.push({
      id: info.route,
      label: info.title,
      kind: info.kind,
    });
  }
  return leaves;
}

export function NamespaceTree({
  namespaces,
  activeNamespace,
  compositionRead,
  controlsRead,
  onSelectNamespace,
  onAddNamespace,
  onRemoveNamespace,
}: Props) {
  const location = useLocation();
  const navigate = useNavigate();
  const [draft, setDraft] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {};
    if (activeNamespace) {
      initial[activeNamespace] = true;
    }
    return initial;
  });

  const activeLeaf = useMemo(() => leafForPath(location.pathname), [location.pathname]);
  const leaves = useMemo(() => crdLeaves(compositionRead), [compositionRead]);

  useEffect(() => {
    if (!activeNamespace) {
      return;
    }
    setExpanded((prev) => {
      if (prev[activeNamespace]) {
        return prev;
      }
      return { ...prev, [activeNamespace]: true };
    });
  }, [activeNamespace]);

  function toggleExpanded(ns: string) {
    setExpanded((prev) => ({ ...prev, [ns]: !prev[ns] }));
  }

  function openLeaf(ns: string, leaf: TreeLeafId) {
    onSelectNamespace(ns);
    setExpanded((prev) => ({ ...prev, [ns]: true }));
    navigate(pathForLeaf(ns, leaf));
  }

  function handleAdd(event: FormEvent) {
    event.preventDefault();
    const value = draft.trim();
    if (!value) {
      return;
    }
    onAddNamespace(value);
    setDraft("");
    setExpanded((prev) => ({ ...prev, [value]: true }));
    navigate("/");
  }

  return (
    <aside className="ns-tree" aria-label="Namespaces and CRDs">
      <div className="ns-tree-header">
        <span className="ns-tree-heading">Namespaces</span>
        <span className="ns-tree-hint">folders · CRDs</span>
      </div>

      <div className="ns-tree-scroll">
        {namespaces.length === 0 ? (
          <div className="ns-tree-empty">No namespaces yet. Add one below.</div>
        ) : (
          <ul className="ns-tree-list">
            {namespaces.map((ns) => {
              const isOpen = Boolean(expanded[ns]);
              const isActiveNs = ns === activeNamespace;
              return (
                <li key={ns} className={`ns-folder${isActiveNs ? " is-active-ns" : ""}`}>
                  <div className="ns-folder-row">
                    <button
                      type="button"
                      className="ns-folder-toggle"
                      aria-expanded={isOpen}
                      aria-label={isOpen ? `Collapse ${ns}` : `Expand ${ns}`}
                      onClick={() => toggleExpanded(ns)}
                    >
                      <span className={`ns-chevron${isOpen ? " open" : ""}`} aria-hidden>
                        ▸
                      </span>
                    </button>
                    <button
                      type="button"
                      className={`ns-folder-label${isActiveNs ? " active" : ""}`}
                      onClick={() => openLeaf(ns, "agent-runs")}
                      title={`Open AgentRuns in ${ns}`}
                    >
                      <span className={`ns-folder-glyph${isOpen ? " open" : ""}`} aria-hidden />
                      <span className="ns-folder-name mono">{ns}</span>
                    </button>
                    {namespaces.length > 1 ? (
                      <button
                        type="button"
                        className="ns-folder-remove"
                        title={`Remove ${ns}`}
                        aria-label={`Remove ${ns}`}
                        onClick={() => onRemoveNamespace(ns)}
                      >
                        ×
                      </button>
                    ) : null}
                  </div>

                  {isOpen ? (
                    <ul className="ns-sublist" role="group" aria-label={`${ns} CRDs`}>
                      {leaves.map((leaf) => {
                        const selected = isActiveNs && activeLeaf === leaf.id;
                        return (
                          <li key={leaf.id}>
                            <button
                              type="button"
                              className={`ns-subfolder${selected ? " active" : ""}`}
                              onClick={() => openLeaf(ns, leaf.id)}
                              title={leaf.kind}
                            >
                              <span className="ns-subfolder-glyph" aria-hidden />
                              <span className="ns-subfolder-label">{leaf.label}</span>
                              <span className="ns-subfolder-kind mono">{leaf.kind}</span>
                            </button>
                          </li>
                        );
                      })}
                      {compositionRead ? (
                        <li>
                          <Link
                            className={`ns-subfolder ns-subfolder-link${
                              isActiveNs && activeLeaf === "library" ? " active" : ""
                            }`}
                            to="/library"
                            onClick={() => onSelectNamespace(ns)}
                          >
                            <span className="ns-subfolder-glyph" aria-hidden />
                            <span className="ns-subfolder-label">Library hub</span>
                            <span className="ns-subfolder-kind mono">all CRDs</span>
                          </Link>
                        </li>
                      ) : null}
                      {controlsRead ? (
                        <li>
                          <Link
                            className={`ns-subfolder ns-subfolder-link${
                              isActiveNs && activeLeaf === "controls" ? " active" : ""
                            }`}
                            to="/controls"
                            onClick={() => onSelectNamespace(ns)}
                          >
                            <span className="ns-subfolder-glyph" aria-hidden />
                            <span className="ns-subfolder-label">Launch controls</span>
                            <span className="ns-subfolder-kind mono">AgentRunControl</span>
                          </Link>
                        </li>
                      ) : null}
                    </ul>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <form className="ns-tree-add" onSubmit={handleAdd}>
        <label className="label" htmlFor="ns-tree-add-input">
          Add namespace
        </label>
        <div className="ns-tree-add-row">
          <input
            id="ns-tree-add-input"
            className="input"
            placeholder="namespace"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            aria-label="New namespace name"
            autoComplete="off"
            spellCheck={false}
          />
          <button type="submit" className="btn btn-primary" disabled={!draft.trim()}>
            Add new
          </button>
        </div>
      </form>
    </aside>
  );
}
