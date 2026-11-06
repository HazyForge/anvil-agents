import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { COMPOSITION_KINDS } from "../api/types.composition";
import { Icon, iconForLeaf } from "./Icon";

export type TreeLeafId = "agent-runs" | string;

interface Props {
  namespaces: string[];
  activeNamespace: string;
  compositionRead: boolean;
  controlsRead: boolean;
  externalTriggersEnabled: boolean;
  chatEnabled: boolean;
  onSelectNamespace: (namespace: string) => void;
  onAddNamespace: (namespace: string) => void;
  onRemoveNamespace: (namespace: string) => void;
}

export function leafForPath(pathname: string): TreeLeafId {
  if (pathname === "/" || pathname.includes("/runs/")) {
    return "agent-runs";
  }
  if (pathname.startsWith("/controls")) {
    return "controls";
  }
  if (pathname.startsWith("/chat") || /\/ns\/[^/]+\/chat(\/|$)/.test(pathname)) {
    return "chat";
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
  if (leaf === "chat") {
    return "/chat";
  }
  return `/ns/${encodeURIComponent(namespace)}/${leaf}`;
}

/** CRD subfolders under each namespace folder. AgentRuns is always first. */
function crdLeaves(
  compositionRead: boolean,
  externalTriggersEnabled: boolean,
): Array<{ id: TreeLeafId; label: string; kind: string }> {
  const leaves: Array<{ id: TreeLeafId; label: string; kind: string }> = [
    { id: "agent-runs", label: "Agent runs", kind: "AgentRun" },
  ];
  if (!compositionRead) {
    return leaves;
  }
  for (const info of COMPOSITION_KINDS) {
    if (info.route === "external-triggers" && !externalTriggersEnabled) {
      continue;
    }
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
  externalTriggersEnabled,
  chatEnabled,
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
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Deep links: keep the lit destination visible in the scrolling tree.
  // Scrolls only the tree container, never the page.
  useEffect(() => {
    const container = scrollRef.current;
    const current = container?.querySelector<HTMLElement>(".ns-subfolder.active");
    if (!container || !current) {
      return;
    }
    const box = container.getBoundingClientRect();
    const item = current.getBoundingClientRect();
    if (item.bottom > box.bottom - 24) {
      container.scrollTop += item.bottom - box.bottom + 32;
    } else if (item.top < box.top) {
      container.scrollTop -= box.top - item.top + 8;
    }
  }, [activeLeaf, activeNamespace]);
  const leaves = useMemo(
    () => crdLeaves(compositionRead, externalTriggersEnabled),
    [compositionRead, externalTriggersEnabled],
  );

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

  // Operate first (runs, chat, gates), then composition, then the raw CRD folders.
  const [runsLeaf, ...kindLeaves] = leaves;
  const profileLeaf = kindLeaves.find((leaf) => leaf.id === "profiles");
  const otherCrdLeaves = kindLeaves.filter((leaf) => leaf.id !== "profiles");

  function leafButton(ns: string, isActiveNs: boolean, leaf: { id: TreeLeafId; label: string; kind: string }) {
    const selected = isActiveNs && activeLeaf === leaf.id;
    return (
      <li key={leaf.id}>
        <button
          type="button"
          className={`ns-subfolder${selected ? " active" : ""}`}
          onClick={() => openLeaf(ns, leaf.id)}
          title={leaf.kind}
          aria-current={selected ? "page" : undefined}
        >
          <Icon name={iconForLeaf(leaf.id)} className="ns-subfolder-glyph" />
          <span className="ns-subfolder-label">{leaf.label}</span>
          <span className="ns-subfolder-kind mono">{leaf.kind}</span>
        </button>
      </li>
    );
  }

  function leafLink(ns: string, isActiveNs: boolean, id: TreeLeafId, to: string, label: string, kind: string) {
    const selected = isActiveNs && activeLeaf === id;
    return (
      <li>
        <Link
          className={`ns-subfolder ns-subfolder-link${selected ? " active" : ""}`}
          to={to}
          onClick={() => onSelectNamespace(ns)}
          aria-current={selected ? "page" : undefined}
        >
          <Icon name={iconForLeaf(id)} className="ns-subfolder-glyph" />
          <span className="ns-subfolder-label">{label}</span>
          <span className="ns-subfolder-kind mono">{kind}</span>
        </Link>
      </li>
    );
  }

  return (
    <aside className="ns-tree" id="console-namespace-tree" aria-label="Namespaces and CRDs">
      <div className="ns-tree-header">
        <span className="ns-tree-heading">Namespaces</span>
        <span className="ns-tree-hint">folders · CRDs</span>
      </div>

      <div className="ns-tree-scroll" ref={scrollRef}>
        {namespaces.length === 0 ? (
          <div className="ns-tree-empty">No namespaces yet. Add one below.</div>
        ) : (
          <ul className="ns-tree-list">
            {namespaces.map((ns) => {
              const isOpen = Boolean(expanded[ns]);
              const isActiveNs = ns === activeNamespace;
              return (
                <li key={ns} className={`ns-folder${isActiveNs ? " is-active-ns" : ""}${isOpen ? " is-open" : ""}`}>
                  <div className="ns-folder-row">
                    <button
                      type="button"
                      className="ns-folder-toggle"
                      aria-expanded={isOpen}
                      aria-label={isOpen ? `Collapse ${ns}` : `Expand ${ns}`}
                      onClick={() => toggleExpanded(ns)}
                    >
                      <Icon name="chevron" className={`ns-chevron${isOpen ? " open" : ""}`} />
                    </button>
                    <button
                      type="button"
                      className={`ns-folder-label${isActiveNs ? " active" : ""}`}
                      onClick={() => openLeaf(ns, "agent-runs")}
                      title={`Open AgentRuns in ${ns}`}
                    >
                      <Icon name="folder" className={`ns-folder-glyph${isOpen ? " open" : ""}`} />
                      <span className="ns-folder-name mono">{ns}</span>
                      {isActiveNs ? <span className="ns-folder-live" aria-hidden="true" /> : null}
                    </button>
                    {namespaces.length > 1 ? (
                      <button
                        type="button"
                        className="ns-folder-remove"
                        title={`Remove ${ns}`}
                        aria-label={`Remove ${ns}`}
                        onClick={() => onRemoveNamespace(ns)}
                      >
                        <Icon name="close" />
                      </button>
                    ) : null}
                  </div>

                  {isOpen ? (
                    <ul className="ns-sublist" role="group" aria-label={`${ns} CRDs`}>
                      {runsLeaf ? leafButton(ns, isActiveNs, runsLeaf) : null}
                      {chatEnabled
                        ? leafLink(ns, isActiveNs, "chat", "/chat", "Standing chat", "threads")
                        : null}
                      {controlsRead
                        ? leafLink(ns, isActiveNs, "controls", "/controls", "Launch controls", "AgentRunControl")
                        : null}
                      {profileLeaf ? leafButton(ns, isActiveNs, profileLeaf) : null}
                      {compositionRead
                        ? leafLink(ns, isActiveNs, "library", "/library", "Library hub", "all CRDs")
                        : null}
                      {otherCrdLeaves.length > 0 ? (
                        <li className="ns-sublist-divider" aria-hidden="true">
                          CRDs
                        </li>
                      ) : null}
                      {otherCrdLeaves.map((leaf) => leafButton(ns, isActiveNs, leaf))}
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
            className="input mono"
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
