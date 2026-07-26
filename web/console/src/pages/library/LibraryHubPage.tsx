import { Link } from "react-router-dom";
import { COMPOSITION_KINDS } from "../../api/types.composition";

interface Props {
  namespace: string;
  writeEnabled: boolean;
}

export function LibraryHubPage({ namespace, writeEnabled }: Props) {
  if (!namespace) {
    return (
      <div className="panel">
        <div className="panel-body empty">Select a namespace to browse composition objects.</div>
      </div>
    );
  }

  const kinds = COMPOSITION_KINDS.filter((kind) => kind.route !== "profiles");

  return (
    <div className="library-hub">
      <div className="page-header">
        <div>
          <h1 className="page-title">Composition library</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {" · "}
            GitOps remains source of truth for cluster-owned objects
          </p>
        </div>
        <div className="chip-row">
          <span className="chip">{writeEnabled ? "write enabled" : "read only"}</span>
          <Link className="btn btn-primary" to="/profiles">
            Profiles as cards
          </Link>
        </div>
      </div>

      <div className="banner banner-info">
        AgentRunProfiles are shown as cards on the{" "}
        <Link to="/profiles">Profiles</Link> page (the composition CRD). Other kinds below are
        supporting building blocks. GitOps-owned objects stay locked; only resources labeled{" "}
        <span className="mono">control.anvil.hazyforge.io/managed-by=anvil-agents-console</span> can
        be created or edited here.
      </div>

      <div className="library-grid">
        {kinds.map((kind) => (
          <Link
            key={kind.segment}
            className={`library-card${kind.danger ? " library-card-danger" : ""}`}
            to={`/ns/${encodeURIComponent(namespace)}/${kind.route}`}
          >
            <div className="library-card-title">{kind.title}</div>
            <div className="library-card-body">{kind.description}</div>
            {kind.danger ? <div className="library-card-warn">Elevated authority</div> : null}
          </Link>
        ))}
      </div>
    </div>
  );
}
