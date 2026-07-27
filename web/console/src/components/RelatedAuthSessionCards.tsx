import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { listComposition } from "../api/composition";
import type { CompositionDocument } from "../api/types.composition";
import {
  authSessionIsActive,
  authSessionVolumeName,
  compositionCardSummary,
} from "../utils/compositionSummary";
import { formatTime } from "../utils/format";

interface Props {
  token: string;
  namespace: string;
  /** When set, only sessions for these data volume names. Empty = none shown. */
  dataVolumeNames: string[];
  title?: string;
}

/**
 * Cards for AgentAuthSession CRs that affect selected durable homes.
 * Controllers keep AgentRuns Pending while a session is active on a mounted volume.
 */
export function RelatedAuthSessionCards({
  token,
  namespace,
  dataVolumeNames,
  title = "Auth sessions on selected volumes",
}: Props) {
  const [items, setItems] = useState<CompositionDocument[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const volumeKey = dataVolumeNames.slice().sort().join("|");

  useEffect(() => {
    if (!namespace || !token || dataVolumeNames.length === 0) {
      setItems([]);
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void listComposition(token, namespace, "agent-auth-sessions", 200, controller.signal)
      .then((docs) => {
        setItems(docs);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setLoading(false);
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [token, namespace, volumeKey]);

  const related = useMemo(() => {
    const wanted = new Set(dataVolumeNames);
    return items
      .filter((doc) => wanted.has(authSessionVolumeName(doc)))
      .sort((a, b) => {
        // Active first, then name.
        const aActive = authSessionIsActive(a) ? 0 : 1;
        const bActive = authSessionIsActive(b) ? 0 : 1;
        if (aActive !== bActive) {
          return aActive - bActive;
        }
        return a.metadata.name.localeCompare(b.metadata.name);
      });
  }, [items, dataVolumeNames]);

  if (dataVolumeNames.length === 0) {
    return null;
  }

  return (
    <section className="related-auth-sessions">
      <h3 className="form-section-title">{title}</h3>
      <p className="field-help">
        Durable OAuth/login maintenance is an append-only <span className="mono">AgentAuthSession</span>{" "}
        CR. While a session is non-terminal on a volume, AgentRuns that mount it stay{" "}
        <span className="mono">Pending</span> with reason <span className="mono">AuthSessionActive</span>.
        Create reauth/logout with <span className="mono">anvil-agentctl auth</span>, not GitOps.
      </p>
      {loading ? <div className="empty">Loading auth sessions…</div> : null}
      {error ? <div className="banner banner-error">{error}</div> : null}
      {!loading && !error && related.length === 0 ? (
        <div className="ordered-ref-empty">
          No auth sessions for {dataVolumeNames.map((n) => n).join(", ")}. That is normal when login is
          healthy.
        </div>
      ) : null}
      {related.length > 0 ? (
        <div className="card-grid card-grid-library">
          {related.map((doc) => {
            const active = authSessionIsActive(doc);
            const phase = String(doc.status?.phase ?? "Pending");
            const href = `/ns/${encodeURIComponent(namespace)}/auth-sessions/${encodeURIComponent(doc.metadata.name)}`;
            return (
              <article
                key={doc.metadata.uid || doc.metadata.name}
                className={`agent-card agent-card-library${active ? " auth-session-card-active" : ""}`}
              >
                <header className="agent-card-header">
                  <div className="chip-row">
                    <span className="chip chip-mute">AgentAuthSession</span>
                    <span className={`chip ${active ? "chip-warn" : "chip-mute"}`}>{phase}</span>
                    <span className="chip mono">{String(doc.spec?.provider ?? "")}</span>
                    <span className="chip mono">{String(doc.spec?.action ?? "")}</span>
                  </div>
                  <h2 className="agent-card-title mono">
                    <Link to={href}>{doc.metadata.name}</Link>
                  </h2>
                </header>
                <p className="agent-card-desc mono">{compositionCardSummary(doc)}</p>
                <div className="agent-card-meta">
                  vol {authSessionVolumeName(doc) || "—"} ·{" "}
                  {formatTime(doc.metadata.creationTimestamp)}
                  {active ? " · blocks new runs on this volume" : ""}
                </div>
                <footer className="agent-card-actions">
                  <Link className="btn btn-primary" to={href}>
                    Open
                  </Link>
                </footer>
              </article>
            );
          })}
        </div>
      ) : null}
    </section>
  );
}
