import { Link } from "react-router-dom";
import { DOC_SECTIONS, DOCS, docsInSection } from "../docs/catalog";

export default function DocsIndexPage() {
  return (
    <main className="docs-shell">
      <div className="container docs-index">
        <header className="docs-index-hero">
          <div className="eyebrow">Documentation</div>
          <h1 className="display section-title">Anvil Agents docs</h1>
          <p className="section-lead">
            Operator documentation for the standalone Kubernetes agent operator:
            install paths, composition, harnesses, streams, security, and
            day-two operations. Diagrams are first-class.
          </p>
          <div className="hero-cta">
            <Link className="btn btn-primary" to="/docs/getting-started">
              Getting started
            </Link>
            <Link className="btn btn-ghost" to="/docs/architecture">
              Architecture
            </Link>
          </div>
        </header>

        <div className="docs-featured">
          {DOCS.filter((d) => d.image)
            .slice(0, 4)
            .map((doc) => (
              <Link
                key={doc.slug}
                to={`/docs/${doc.slug}`}
                className="docs-featured-card panel"
              >
                <img src={doc.image} alt="" loading="lazy" />
                <div>
                  <h2 className="display">{doc.title}</h2>
                  <p>{doc.summary}</p>
                </div>
              </Link>
            ))}
        </div>

        {DOC_SECTIONS.map((section) => {
          const entries = docsInSection(section.id);
          if (entries.length === 0) return null;
          return (
            <section key={section.id} className="docs-section-block">
              <h2 className="mono docs-section-label">{section.label}</h2>
              <div className="docs-card-grid">
                {entries.map((doc) => (
                  <Link
                    key={doc.slug}
                    to={`/docs/${doc.slug}`}
                    className="panel docs-card"
                  >
                    {doc.image ? (
                      <img
                        className="docs-card-thumb"
                        src={doc.image}
                        alt=""
                        loading="lazy"
                      />
                    ) : null}
                    <h3 className="display">{doc.title}</h3>
                    <p>{doc.summary}</p>
                  </Link>
                ))}
              </div>
            </section>
          );
        })}
      </div>
    </main>
  );
}
