import { Link, useParams } from "react-router-dom";
import DocMarkdown from "../components/DocMarkdown";
import { DOC_SECTIONS, DOCS, docBySlug } from "../docs/catalog";
import { loadDocMarkdown } from "../docs/loadDocs";
import { rewriteDocMarkdown } from "../docs/markdown";

export default function DocPage() {
  const params = useParams();
  const slug = params["*"] || params.slug || "";
  const entry = docBySlug(slug);
  const raw = entry ? loadDocMarkdown(entry) : null;

  if (!entry || raw === null) {
    return (
      <main className="docs-shell">
        <div className="container docs-missing panel">
          <h1 className="display">Doc not found</h1>
          <p className="section-lead">
            No published page for <code>{slug || "(empty)"}</code>.
          </p>
          <Link className="btn btn-primary" to="/docs">
            Back to docs
          </Link>
        </div>
      </main>
    );
  }

  const markdown = rewriteDocMarkdown(raw);
  const sectionLabel =
    DOC_SECTIONS.find((s) => s.id === entry.section)?.label ?? entry.section;
  const related = DOCS.filter(
    (d) => d.section === entry.section && d.slug !== entry.slug,
  ).slice(0, 4);

  return (
    <main className="docs-shell">
      <div className="container docs-layout">
        <aside className="docs-sidebar panel" aria-label="Docs navigation">
          <Link className="mono docs-sidebar-home" to="/docs">
            ← All docs
          </Link>
          {DOC_SECTIONS.map((section) => {
            const items = DOCS.filter((d) => d.section === section.id);
            if (items.length === 0) return null;
            return (
              <div key={section.id} className="docs-sidebar-group">
                <div className="mono docs-sidebar-label">{section.label}</div>
                <ul>
                  {items.map((doc) => (
                    <li key={doc.slug}>
                      <Link
                        to={`/docs/${doc.slug}`}
                        className={
                          doc.slug === entry.slug ? "is-active" : undefined
                        }
                      >
                        {doc.title}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
        </aside>

        <article className="docs-article panel">
          <div className="mono docs-kicker">{sectionLabel}</div>
          {entry.image ? (
            <div className="docs-hero-image">
              <img src={entry.image} alt="" />
            </div>
          ) : null}
          <DocMarkdown markdown={markdown} />
          {related.length > 0 ? (
            <footer className="docs-related">
              <div className="mono docs-sidebar-label">Related</div>
              <div className="docs-related-grid">
                {related.map((doc) => (
                  <Link key={doc.slug} to={`/docs/${doc.slug}`}>
                    {doc.title}
                  </Link>
                ))}
              </div>
            </footer>
          ) : null}
        </article>
      </div>
    </main>
  );
}
