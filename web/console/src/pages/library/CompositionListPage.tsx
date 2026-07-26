import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { APIError } from "../../api/client";
import { listComposition } from "../../api/composition";
import {
  compositionKindByRoute,
  type CompositionDocument,
} from "../../api/types.composition";
import { formatTime } from "../../utils/format";

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
      return (
        item.metadata.name.toLowerCase().includes(q) ||
        description.includes(q) ||
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

      <div className="filters-bar">
        <label className="field" style={{ minWidth: "16rem", flex: 1 }}>
          <span className="label">Search</span>
          <input
            className="input"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="name, description, managed-by"
          />
        </label>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {loading ? <div className="empty">Loading {kind.plural}…</div> : null}

      {!loading && !error ? (
        <div className="panel">
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Description</th>
                  <th>Management</th>
                  <th>Generation</th>
                  <th>Created</th>
                </tr>
              </thead>
              <tbody>
                {filtered.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="empty">
                      No {kind.plural} in this namespace.
                    </td>
                  </tr>
                ) : (
                  filtered.map((item) => (
                    <tr
                      key={item.metadata.uid || item.metadata.name}
                      className="click-row"
                      onClick={() =>
                        navigate(
                          `/ns/${encodeURIComponent(namespace)}/${kind.route}/${encodeURIComponent(item.metadata.name)}`,
                        )
                      }
                    >
                      <td className="mono">{item.metadata.name}</td>
                      <td>{String(item.spec?.description ?? "—")}</td>
                      <td>
                        <ManagementChip doc={item} />
                      </td>
                      <td className="mono">{item.metadata.generation ?? "—"}</td>
                      <td className="mono">
                        {formatTime(item.metadata.creationTimestamp)}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function ManagementChip({ doc }: { doc: CompositionDocument }) {
  if (doc.management.writable) {
    return <span className="chip chip-ok">console</span>;
  }
  if (doc.management.reason === "gitops_protected") {
    return (
      <span className="chip chip-lock" title="GitOps source of truth">
        gitops · {doc.management.managedBy || "protected"}
      </span>
    );
  }
  return (
    <span className="chip chip-mute" title="Not console-managed">
      locked · {doc.management.managedBy || "unmanaged"}
    </span>
  );
}
