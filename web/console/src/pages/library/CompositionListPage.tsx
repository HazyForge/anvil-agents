import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { APIError } from "../../api/client";
import { listComposition } from "../../api/composition";
import { compositionKindByRoute } from "../../api/types.composition";
import { CompositionResourceCard } from "../../components/CompositionResourceCard";
import { CRD_AS_CARD_HELP, CRD_AS_CARD_MANTRA } from "../../design/mantra";
import { compositionCardSummary } from "../../utils/compositionSummary";
import type { CompositionDocument } from "../../api/types.composition";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

export function CompositionListPage({ token, namespace: activeNamespace, writeEnabled }: Props) {
  const { kind: kindRoute = "", namespace: routeNamespace = "" } = useParams();
  const namespace = routeNamespace || activeNamespace;
  const navigate = useNavigate();
  const kind = compositionKindByRoute(kindRoute);
  const [items, setItems] = useState<CompositionDocument[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");

  useEffect(() => {
    if (!kind || !namespace) {
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void listComposition(token, namespace, kind.segment, 200, controller.signal)
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
  }, [token, namespace, kind]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) {
      return items;
    }
    return items.filter((item) => {
      const description = String(item.spec?.description ?? "").toLowerCase();
      const summary = compositionCardSummary(item).toLowerCase();
      return (
        item.metadata.name.toLowerCase().includes(q) ||
        description.includes(q) ||
        summary.includes(q) ||
        item.management.managedBy?.toLowerCase().includes(q) ||
        item.management.reason.toLowerCase().includes(q)
      );
    });
  }, [items, query]);

  if (!kind) {
    return (
      <div className="panel">
        <div className="panel-body empty">Unknown composition kind.</div>
      </div>
    );
  }

  if (!namespace) {
    return (
      <div className="panel">
        <div className="panel-body empty">Select a namespace first.</div>
      </div>
    );
  }

  return (
    <div className="library-list">
      <div className="page-header">
        <div>
          <div className="breadcrumb">
            <Link to="/library">Library</Link>
            <span>/</span>
            <span>{kind.title}</span>
          </div>
          <h1 className="page-title">{kind.title}</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {" · "}
            {kind.kind} CRDs as cards
          </p>
        </div>
        <div className="chip-row">
          {writeEnabled ? (
            <button
              type="button"
              className="btn btn-primary"
              onClick={() =>
                navigate(`/ns/${encodeURIComponent(namespace)}/${kind.route}/new`)
              }
            >
              Create
            </button>
          ) : null}
        </div>
      </div>

      <div className="banner banner-info">
        <strong>{CRD_AS_CARD_MANTRA}</strong> {CRD_AS_CARD_HELP}
        {kind.route === "harness-profiles" ? (
          <>
            {" "}
            Harness profiles are the <em>runtime machine</em> (backend, SA, secrets, volumes) —
            role/skills stay on <Link to="/profiles">run profiles</Link>.
          </>
        ) : null}
      </div>

      <div className="filters-bar">
        <label className="field" style={{ minWidth: "16rem", flex: 1 }}>
          <span className="label">Search</span>
          <input
            className="input"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="name, description, summary, managed-by"
          />
        </label>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {loading ? <div className="empty">Loading {kind.plural}…</div> : null}

      {!loading && !error && filtered.length === 0 ? (
        <div className="panel">
          <div className="panel-body empty">No {kind.plural} in this namespace.</div>
        </div>
      ) : null}

      {!loading && !error && filtered.length > 0 ? (
        <div className="card-grid card-grid-library">
          {filtered.map((item) => (
            <CompositionResourceCard
              key={item.metadata.uid || item.metadata.name}
              doc={item}
              kindRoute={kind.route}
              namespace={namespace}
              onOpen={() =>
                navigate(
                  `/ns/${encodeURIComponent(namespace)}/${kind.route}/${encodeURIComponent(item.metadata.name)}`,
                )
              }
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}
