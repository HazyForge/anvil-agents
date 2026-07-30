import { Link } from "react-router-dom";
import { COMPOSITION_KINDS } from "../../api/types.composition";
import { CRD_AS_CARD_HELP, CRD_AS_CARD_MANTRA } from "../../design/mantra";

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
  const sections = [
    { id: "atomic", title: "Atomic capabilities", description: "Select skills, executable tools, and MCP servers independently." },
    { id: "collection", title: "Collections", description: "Reusable ordered sets of one atomic capability kind." },
    { id: "runtime", title: "Runtime", description: "Harness identity, storage, placement, and auth maintenance." },
  ] as const;

  return (
    <div className="library-hub">
      <div className="page-header">
        <div>
          <h1 className="page-title">Composition library</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {" · "}
            every composition CRD is a card
          </p>
        </div>
        <div className="chip-row">
          <span className="chip">{writeEnabled ? "write enabled" : "read only"}</span>
          <Link className="btn btn-primary" to="/profiles">
            Profile cards
          </Link>
        </div>
      </div>

      <div className="banner banner-info">
        <strong>{CRD_AS_CARD_MANTRA}</strong> {CRD_AS_CARD_HELP}{" "}
        <Link to="/profiles">AgentRunProfiles</Link> and the kinds below are all CRDs. GitOps-owned
        objects stay locked; only resources labeled{" "}
        <span className="mono">control.anvil.hazyforge.io/managed-by=anvil-agents-console</span> can
        be created or edited here.
      </div>

      {sections.map((section) => (
        <section key={section.id} className="library-section">
          <h2 className="panel-title">{section.title}</h2>
          <p className="page-sub">{section.description}</p>
          <div className="library-grid">
            {kinds.filter((kind) => kind.category === section.id).map((kind) => (
              <Link
                key={kind.segment}
                className={`library-card${kind.danger ? " library-card-danger" : ""}`}
                to={`/ns/${encodeURIComponent(namespace)}/${kind.route}`}
              >
                <div className="library-card-title">{kind.title}</div>
                <div className="library-card-kind mono">{kind.kind}</div>
                <div className="library-card-body">{kind.description}</div>
                {kind.danger ? <div className="library-card-warn">Elevated authority</div> : null}
              </Link>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
