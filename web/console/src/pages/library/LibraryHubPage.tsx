import { Link } from "react-router-dom";
import { COMPOSITION_KINDS } from "../../api/types.composition";
import { Icon, iconForLeaf } from "../../components/Icon";
import { CRD_AS_CARD_HELP, CRD_AS_CARD_MANTRA } from "../../design/mantra";

interface Props {
  namespace: string;
  writeEnabled: boolean;
  externalTriggersEnabled: boolean;
}

export function LibraryHubPage({ namespace, writeEnabled, externalTriggersEnabled }: Props) {
  if (!namespace) {
    return (
      <div className="empty-state plate">
        <Icon name="library" />
        <p className="empty">Select a namespace to browse composition objects.</p>
      </div>
    );
  }

  const kinds = COMPOSITION_KINDS.filter((kind) => {
    if (kind.route === "profiles") {
      return false;
    }
    if (kind.route === "external-triggers" && !externalTriggersEnabled) {
      return false;
    }
    return true;
  });

  return (
    <div className="library-hub">
      <header className="page-header">
        <div>
          <span className="page-kicker">Composition CRDs</span>
          <h1 className="page-title">Composition library</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {" · "}
            every composition CRD is a card
          </p>
        </div>
        <div className="page-actions">
          <span className={`chip ${writeEnabled ? "chip-ok" : "chip-mute"}`}>
            {writeEnabled ? "write enabled" : "read only"}
          </span>
          <Link className="btn btn-primary" to="/profiles">
            <Icon name="profiles" />
            Profile cards
          </Link>
        </div>
      </header>

      <div className="banner banner-info">
        <span>
          <strong>{CRD_AS_CARD_MANTRA}</strong> {CRD_AS_CARD_HELP}{" "}
          <Link to="/profiles">AgentRunProfiles</Link> and the kinds below are all CRDs. GitOps-owned
          objects stay locked; only resources labeled{" "}
          <span className="mono">control.anvil.hazyforge.io/managed-by=anvil-agents-console</span> can
          be created or edited here.
        </span>
      </div>

      <div className="library-grid">
        {kinds.map((kind) => (
          <Link
            key={kind.segment}
            className={`library-card card${kind.danger ? " library-card-danger warn" : ""}`}
            to={`/ns/${encodeURIComponent(namespace)}/${kind.route}`}
          >
            <span className="kind-tile" aria-hidden="true">
              <Icon name={iconForLeaf(kind.route)} />
            </span>
            <div className="library-card-title">{kind.title}</div>
            <div className="library-card-kind mono">{kind.kind}</div>
            <div className="library-card-body">{kind.description}</div>
            {kind.danger ? (
              <div className="library-card-warn">
                <Icon name="alert" />
                Elevated authority
              </div>
            ) : null}
            <Icon name="chevron" className="library-card-go" />
          </Link>
        ))}
      </div>
    </div>
  );
}
