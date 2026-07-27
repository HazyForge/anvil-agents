import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { APIError } from "../../api/client";
import { listComposition } from "../../api/composition";
import type { CompositionDocument } from "../../api/types.composition";
import { CompositionResourceCard } from "../../components/CompositionResourceCard";
import { CRD_AS_CARD_HELP, CRD_AS_CARD_MANTRA } from "../../design/mantra";
import { compositionCardSummary } from "../../utils/compositionSummary";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
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
        compositionCardSummary(doc).toLowerCase().includes(q) ||
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
            AgentRunProfile CRDs for namespace <span className="mono">{namespace}</span>
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
        <strong>{CRD_AS_CARD_MANTRA}</strong> {CRD_AS_CARD_HELP} Assign avatars via{" "}
        <span className="mono">ui.anvil.hazyforge.io/icon</span>. GitOps-owned profiles stay
        read-only.
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
        <div className="card-grid card-grid-profiles">
          {filtered.map((doc) => (
            <CompositionResourceCard
              key={doc.metadata.uid || doc.metadata.name}
              doc={doc}
              kindRoute="profiles"
              namespace={namespace}
              size="lg"
              primaryLabel="Open"
              secondaryLabel={doc.management.writable && writeEnabled ? "Edit" : "View"}
              onOpen={() =>
                navigate(
                  `/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(doc.metadata.name)}`,
                )
              }
            />
          ))}
        </div>
      ) : null}

      <p className="page-sub" style={{ marginTop: "1rem" }}>
        Supporting CRDs (harness, skills, tools, volumes) are also cards under{" "}
        <Link to="/library">Library</Link>.
      </p>
    </div>
  );
}
