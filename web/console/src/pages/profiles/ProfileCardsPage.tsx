import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { APIError } from "../../api/client";
import { listComposition } from "../../api/composition";
import type { CompositionDocument } from "../../api/types.composition";
import { formatTime } from "../../utils/format";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

function refNames(spec: Record<string, unknown>, key: "skillSets" | "toolSets"): string[] {
  const block = spec[key];
  if (typeof block !== "object" || !block) {
    return [];
  }
  return ((block as { refs?: { name?: string }[] }).refs ?? [])
    .map((ref) => String(ref.name ?? "").trim())
    .filter(Boolean);
}

function profileSummary(doc: CompositionDocument): string {
  const spec = doc.spec ?? {};
  const harness =
    typeof spec.harnessProfileRef === "object" && spec.harnessProfileRef
      ? String((spec.harnessProfileRef as { name?: string }).name ?? "")
      : "";
  const skillSets = refNames(spec, "skillSets");
  const toolSets = refNames(spec, "toolSets");
  const intent =
    typeof spec.harness === "object" && spec.harness
      ? String((spec.harness as { intent?: string }).intent ?? "")
      : "";
  const bits = [
    harness ? `harness:${harness}` : null,
    skillSets.length ? `skills:${skillSets.join(",")}` : null,
    toolSets.length ? `tools:${toolSets.join(",")}` : null,
    intent ? `intent:${intent}` : null,
  ].filter(Boolean);
  return bits.join(" · ") || "profile";
}

export function ProfileCardsPage({ token, namespace, writeEnabled }: Props) {
  const navigate = useNavigate();
  const [items, setItems] = useState<CompositionDocument[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");

  useEffect(() => {
    if (!namespace) {
      setItems([]);
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void listComposition(token, namespace, "agent-run-profiles", 200, controller.signal)
      .then((docs) => {
        setItems(docs);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setLoading(false);
        setError(err instanceof APIError ? err.message : String(err));
      });
    return () => controller.abort();
  }, [token, namespace]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) {
      return items;
    }
    return items.filter((doc) => {
      const description = String(doc.spec?.description ?? "").toLowerCase();
      return (
        doc.metadata.name.toLowerCase().includes(q) ||
        description.includes(q) ||
        profileSummary(doc).toLowerCase().includes(q) ||
        (doc.management.managedBy ?? "").toLowerCase().includes(q)
      );
    });
  }, [items, query]);

  if (!namespace) {
    return (
      <div className="panel">
        <div className="panel-body empty">Select a namespace to browse AgentRunProfiles.</div>
      </div>
    );
  }

  return (
    <div className="profiles-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Profiles</h1>
          <p className="page-sub">
            AgentRunProfiles compose harness, skill sets, tool sets, and policy for namespace{" "}
            <span className="mono">{namespace}</span>
          </p>
        </div>
        <div className="chip-row">
          {writeEnabled ? (
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => navigate("/profiles/new")}
            >
              New profile
            </button>
          ) : (
            <span className="chip chip-mute">create disabled</span>
          )}
        </div>
      </div>

      <div className="banner banner-info">
        Profiles are the cluster composition objects. GitOps-owned profiles are read-only here;
        console-created profiles are labeled{" "}
        <span className="mono">control.anvil.hazyforge.io/managed-by=anvil-agents-console</span>.
      </div>

      <div className="filters-bar">
        <label className="field" style={{ flex: 1, minWidth: "14rem" }}>
          <span className="label">Search</span>
          <input
            className="input"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="name, description, harness…"
          />
        </label>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {loading ? <div className="empty">Loading profiles…</div> : null}

      {!loading && !error && filtered.length === 0 ? (
        <div className="panel">
          <div className="panel-body empty">No AgentRunProfiles in this namespace.</div>
        </div>
      ) : null}

      {!loading && filtered.length > 0 ? (
        <div className="card-grid">
          {filtered.map((doc) => {
            const description = String(doc.spec?.description ?? "").trim();
            return (
              <article
                key={doc.metadata.uid || doc.metadata.name}
                className="agent-card"
                role="button"
                tabIndex={0}
                onClick={() =>
                  navigate(
                    `/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(doc.metadata.name)}`,
                  )
                }
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    navigate(
                      `/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(doc.metadata.name)}`,
                    );
                  }
                }}
              >
                <header className="agent-card-header">
                  <h2 className="agent-card-title mono">{doc.metadata.name}</h2>
                  <div className="chip-row">
                    {doc.management.writable ? (
                      <span className="chip chip-ok">console</span>
                    ) : doc.management.reason === "gitops_protected" ? (
                      <span className="chip chip-lock">
                        gitops · {doc.management.managedBy || "protected"}
                      </span>
                    ) : (
                      <span className="chip chip-mute">locked</span>
                    )}
                  </div>
                </header>
                <p className="agent-card-desc">{description || "No description."}</p>
                <div className="agent-card-meta mono">{profileSummary(doc)}</div>
                <div className="agent-card-meta">
                  gen {doc.metadata.generation ?? "—"} · {formatTime(doc.metadata.creationTimestamp)}
                </div>
                <footer className="agent-card-actions">
                  <Link
                    className="btn btn-primary"
                    to={`/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(doc.metadata.name)}`}
                    onClick={(event) => event.stopPropagation()}
                  >
                    Open
                  </Link>
                  <Link
                    className="btn"
                    to={`/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(doc.metadata.name)}`}
                    onClick={(event) => event.stopPropagation()}
                  >
                    {doc.management.writable && writeEnabled ? "Edit" : "View"}
                  </Link>
                </footer>
              </article>
            );
          })}
        </div>
      ) : null}

      <p className="page-sub" style={{ marginTop: "1rem" }}>
        Other composition kinds (harness, skills, tools, volumes) live under{" "}
        <Link to="/library">Library</Link>.
      </p>
    </div>
  );
}
