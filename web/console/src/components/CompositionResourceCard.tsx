import { Link } from "react-router-dom";
import type { CompositionDocument } from "../api/types.composition";
import {
  authSessionIsActive,
  compositionCardSummary,
} from "../utils/compositionSummary";
import { formatTime } from "../utils/format";
import { getIconUrl, getScreenshotUrl, resolveIconSrc } from "../utils/icons";
import { CompositionAvatar } from "./IconPicker";

interface Props {
  doc: CompositionDocument;
  /** e.g. profiles, harness-profiles */
  kindRoute: string;
  namespace: string;
  size?: "md" | "lg";
  primaryLabel?: string;
  secondaryLabel?: string;
  onOpen: () => void;
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

/** Shared browse card for any composition CRD. */
export function CompositionResourceCard({
  doc,
  kindRoute,
  namespace,
  size = "md",
  primaryLabel = "Open",
  secondaryLabel,
  onOpen,
}: Props) {
  const description = String(doc.spec?.description ?? "").trim();
  const icon = getIconUrl(doc.metadata.annotations);
  const screenshot = resolveIconSrc(getScreenshotUrl(doc.metadata.annotations));
  const href = `/ns/${encodeURIComponent(namespace)}/${kindRoute}/${encodeURIComponent(doc.metadata.name)}`;
  const sizeClass = size === "lg" ? "agent-card-lg" : "agent-card-library";
  const activeAuth = doc.kind === "AgentAuthSession" && authSessionIsActive(doc);
  const phase =
    doc.kind === "AgentAuthSession" ? String(doc.status?.phase ?? "Pending") : "";

  return (
    <article
      className={`agent-card ${sizeClass}${activeAuth ? " auth-session-card-active" : ""}`}
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
    >
      {screenshot ? (
        <div className="agent-card-banner">
          <img src={screenshot} alt="" className="agent-card-banner-img" />
        </div>
      ) : null}
      <header className="agent-card-header agent-card-header-with-avatar">
        <CompositionAvatar icon={icon} name={doc.metadata.name} size={size === "lg" ? "lg" : "md"} />
        <div className="agent-card-heading">
          <div className="chip-row" style={{ marginBottom: "0.15rem" }}>
            <span className="chip chip-mute">{doc.kind}</span>
            {phase ? (
              <span className={`chip ${activeAuth ? "chip-warn" : "chip-mute"}`}>{phase}</span>
            ) : null}
            <ManagementChip doc={doc} />
          </div>
          <h2 className="agent-card-title mono">{doc.metadata.name}</h2>
        </div>
      </header>
      <p className="agent-card-desc">
        {description ||
          (doc.kind === "AgentAuthSession"
            ? compositionCardSummary(doc)
            : "No description.")}
      </p>
      <div className="agent-card-meta mono">{compositionCardSummary(doc)}</div>
      <div className="agent-card-meta">
        gen {doc.metadata.generation ?? "—"} · {formatTime(doc.metadata.creationTimestamp)}
      </div>
      <footer className="agent-card-actions">
        <Link className="btn btn-primary" to={href} onClick={(event) => event.stopPropagation()}>
          {primaryLabel}
        </Link>
        {secondaryLabel ? (
          <Link className="btn" to={href} onClick={(event) => event.stopPropagation()}>
            {secondaryLabel}
          </Link>
        ) : null}
      </footer>
    </article>
  );
}
