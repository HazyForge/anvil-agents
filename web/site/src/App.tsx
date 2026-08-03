import AgentCouncil from "./components/AgentCouncil";
import ClusterFieldBackground from "./components/ClusterFieldBackground";

const GITHUB = "https://github.com/HazyForge/anvil-agents";
const DOCS =
  "https://github.com/HazyForge/anvil-agents/blob/master/docs/getting-started.md";
const CONSOLE = "https://agents.anvil.hazyforge.io";
const BLOG =
  "https://github.com/HazyForge/anvil-agents/blob/master/docs/blog/2026-08-02-agents-as-cluster-jobs-not-chatbots.md";

const COMPOSITION = [
  {
    name: "AgentRun",
    detail:
      "Append-only execution record. One run, one Job, one durable result. A retry is a new run.",
  },
  {
    name: "AgentRunProfile",
    detail:
      "Reusable role: scope, policy, standing prompt, and composition for a worker without embedding pod details.",
  },
  {
    name: "AgentHarnessProfile",
    detail:
      "Execution envelope: backend, image, identity, credentials, storage, limits, placement, timeout.",
  },
  {
    name: "Skill & Tool Sets",
    detail:
      "Instructions stay separate from executables. Skills teach when; tools install and prove clients.",
  },
  {
    name: "AgentSchedule",
    detail:
      "Cadence and launch policy for append-only runs — suspension, concurrency, named templates, backoff.",
  },
  {
    name: "OIDC Console",
    detail:
      "Optional API, live SSE logs, and a console SPA so operators inspect runs without raw cluster access.",
  },
];

const HIGHLIGHTS = [
  {
    label: "Kubernetes Jobs",
    detail:
      "Heavy agent work becomes schedulable Jobs with isolation, placement, and concurrency controls.",
  },
  {
    label: "Multi-harness",
    detail:
      "Codex, OpenCode, Hermes, OpenClaw, Grok Build, Pi, or a custom harness behind one run contract.",
  },
  {
    label: "Composable",
    detail:
      "Change a skill, tool, harness, profile, or schedule independently — keep change local.",
  },
  {
    label: "Standalone",
    detail:
      "No Anvil Primaris or control-plane lock-in required. Apache-2.0 operator you can run anywhere.",
  },
];

export default function App() {
  return (
    <div className="shell">
      <header className="site-header">
        <div className="container site-header-inner">
          <a className="brand" href="/" aria-label="Anvil Agents home">
            <span className="brand-mark" aria-hidden="true" />
            <span className="brand-text">
              <span className="mono brand-kicker">Hazy Forge</span>
              <span className="display brand-name">Anvil Agents</span>
            </span>
          </a>
          <nav className="nav mono" aria-label="Primary">
            <a href="#why">Why</a>
            <a href="#composition">Composition</a>
            <a href="#open-source">Open source</a>
            <a href={CONSOLE}>Console</a>
          </nav>
          <div className="header-actions">
            <a className="btn btn-ghost" href={DOCS}>
              Docs
            </a>
            <a className="btn btn-primary" href={GITHUB}>
              GitHub
            </a>
          </div>
        </div>
      </header>

      <main>
        <section className="hero">
          <ClusterFieldBackground />
          <div className="hero-scrim" aria-hidden="true" />
          <div className="container hero-grid">
            <div className="hero-copy">
              <div className="eyebrow">Open source · Kubernetes operator</div>
              <h1 className="display hero-title">
                <span>Anvil</span>
                <span className="hero-title-accent">Agents</span>
              </h1>
              <p className="hero-tagline">
                Agents as cluster <em>jobs</em>, not chatbots
              </p>
              <p className="hero-lead">
                Durable multi-harness agent loops on Kubernetes. Turn builds,
                suites, scans, indexes, and long research into isolated Jobs with
                composable profiles, durable storage, and an observable run
                record.
              </p>
              <div className="hero-chips">
                <span className="chip">
                  <span className="chip-dot" />
                  v0.1 live
                </span>
                <span className="chip">Persistent</span>
                <span className="chip">Observable</span>
                <span className="chip">Restartable</span>
              </div>
              <div className="hero-cta">
                <a className="btn btn-primary" href={GITHUB}>
                  View on GitHub
                </a>
                <a className="btn btn-ghost" href={DOCS}>
                  Getting started
                </a>
                <a className="btn btn-ghost" href={CONSOLE}>
                  Open console
                </a>
              </div>
            </div>
            <div className="hero-visual">
              <AgentCouncil />
            </div>
          </div>
        </section>

        <section id="why" className="section">
          <div className="container">
            <div className="section-head">
              <div className="eyebrow">Why we built it</div>
              <h2 className="display section-title">
                Leave the laptop
                <span className="soft"> behind</span>
              </h2>
              <p className="section-lead">
                A chatbot answers a tenant question in seconds. Platform work —
                multi-file changes, test suites, security analysis, recurring
                maintenance — needs identity, capacity, isolation, and a result
                that remains useful after the conversation ends.
              </p>
            </div>
            <div className="highlight-grid">
              {HIGHLIGHTS.map((item, index) => (
                <article key={item.label} className="panel highlight-card">
                  <div className="mono highlight-index">
                    {String(index + 1).padStart(2, "0")}
                  </div>
                  <h3 className="display highlight-title">{item.label}</h3>
                  <p>{item.detail}</p>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section id="composition" className="section section-alt">
          <div className="container">
            <div className="section-head">
              <div className="eyebrow">Composition model</div>
              <h2 className="display section-title">
                Small objects.
                <span className="soft"> Local change.</span>
              </h2>
              <p className="section-lead">
                The important design choice is not the model provider. It is the
                set of objects that describe what the work is, how it runs, and
                when it should start.{" "}
                <a className="inline-link" href={BLOG}>
                  Read the essay
                </a>
                .
              </p>
            </div>
            <div className="composition-grid">
              {COMPOSITION.map((item) => (
                <article key={item.name} className="panel composition-card">
                  <h3 className="display composition-title">{item.name}</h3>
                  <p>{item.detail}</p>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section id="open-source" className="section">
          <div className="container panel cta-panel">
            <div>
              <div className="eyebrow">Ship agents on the cluster</div>
              <h2 className="display section-title">
                Production-shaped
                <span className="soft"> open source</span>
              </h2>
              <p className="section-lead">
                We open-source the operator work we run in production. Helm
                chart, CRDs, multi-harness runners, archive, and console — under
                Apache-2.0.
              </p>
            </div>
            <div className="cta-actions">
              <a className="btn btn-primary" href={GITHUB}>
                Star on GitHub
              </a>
              <a className="btn btn-ghost" href={DOCS}>
                Install guide
              </a>
              <a className="btn btn-ghost" href={CONSOLE}>
                Live console
              </a>
            </div>
          </div>
        </section>
      </main>

      <footer className="site-footer">
        <div className="container site-footer-inner">
          <div className="mono footer-meta">
            <span>© {new Date().getFullYear()} Hazy Forge</span>
            <span>anvil-agents.hazyforge.io</span>
          </div>
          <div className="footer-links mono">
            <a href={GITHUB}>GitHub</a>
            <a href={DOCS}>Docs</a>
            <a href={CONSOLE}>Console</a>
            <a href="https://hazyforge.io">Hazy Forge</a>
          </div>
        </div>
      </footer>

      <style>{`
        .site-header {
          position: sticky;
          top: 0;
          z-index: 40;
          border-bottom: 1px solid rgb(232 255 244 / 0.06);
          background: rgb(3 8 6 / 0.72);
          backdrop-filter: blur(16px);
        }
        .site-header-inner {
          display: flex;
          align-items: center;
          justify-content: space-between;
          gap: 1rem;
          min-height: 4.1rem;
        }
        .brand {
          display: inline-flex;
          align-items: center;
          gap: 0.75rem;
        }
        .brand-mark {
          width: 0.72rem;
          height: 0.72rem;
          border-radius: 999px;
          background: var(--emerald);
          box-shadow: 0 0 18px rgb(var(--emerald-rgb) / 0.9);
        }
        .brand-text {
          display: grid;
          gap: 0.1rem;
        }
        .brand-kicker {
          color: rgb(var(--ice-rgb) / 0.38);
          font-size: 0.58rem;
        }
        .brand-name {
          font-size: 0.95rem;
          letter-spacing: 0.04em;
        }
        .nav {
          display: none;
          gap: 1.35rem;
          color: rgb(var(--ice-rgb) / 0.48);
          font-size: 0.68rem;
        }
        .nav a:hover {
          color: var(--ice);
        }
        .header-actions {
          display: flex;
          gap: 0.55rem;
        }
        .hero {
          position: relative;
          isolation: isolate;
          min-height: calc(100svh - 4.1rem);
          padding: 2.5rem 0 4rem;
          overflow: hidden;
        }
        .hero-scrim {
          position: absolute;
          inset: 0;
          z-index: 1;
          pointer-events: none;
          background:
            linear-gradient(90deg, rgb(3 8 6 / 0.78) 0%, rgb(3 8 6 / 0.28) 48%, rgb(3 8 6 / 0.55) 100%),
            linear-gradient(180deg, rgb(3 8 6 / 0.2) 0%, transparent 40%, rgb(3 8 6 / 0.72) 100%);
        }
        .hero-grid {
          position: relative;
          z-index: 2;
          display: grid;
          gap: 2.5rem;
          align-items: center;
        }
        .hero-title {
          margin: 1rem 0 0;
          font-size: clamp(3.4rem, 10vw, 7.2rem);
          display: grid;
        }
        .hero-title-accent {
          color: var(--emerald);
        }
        .hero-tagline {
          margin: 1.1rem 0 0;
          max-width: 28rem;
          color: rgb(var(--ice-rgb) / 0.78);
          font-size: clamp(1.15rem, 2.4vw, 1.55rem);
          font-weight: 600;
          line-height: 1.35;
        }
        .hero-tagline em {
          font-style: normal;
          color: var(--emerald);
          border-bottom: 1px solid rgb(var(--emerald-rgb) / 0.45);
        }
        .hero-lead {
          margin: 1.15rem 0 0;
          max-width: 34rem;
          color: var(--muted);
          font-size: 1rem;
          font-weight: 500;
          line-height: 1.7;
        }
        .hero-chips {
          display: flex;
          flex-wrap: wrap;
          gap: 0.5rem;
          margin-top: 1.35rem;
        }
        .hero-cta {
          display: flex;
          flex-wrap: wrap;
          gap: 0.65rem;
          margin-top: 1.6rem;
        }
        .section-head {
          margin-bottom: 2rem;
        }
        .soft {
          color: rgb(var(--ice-rgb) / 0.34);
        }
        .highlight-grid,
        .composition-grid {
          display: grid;
          gap: 1rem;
        }
        .highlight-card,
        .composition-card {
          padding: 1.25rem 1.3rem 1.35rem;
        }
        .highlight-index {
          color: var(--teal);
          font-size: 0.65rem;
          margin-bottom: 0.7rem;
        }
        .highlight-title,
        .composition-title {
          margin: 0;
          font-size: 1.45rem;
        }
        .highlight-card p,
        .composition-card p {
          margin: 0.7rem 0 0;
          color: var(--muted);
          font-size: 0.94rem;
          line-height: 1.65;
        }
        .section-alt {
          background: linear-gradient(180deg, transparent, rgb(var(--emerald-rgb) / 0.03), transparent);
        }
        .inline-link {
          color: var(--emerald);
          border-bottom: 1px solid rgb(var(--emerald-rgb) / 0.35);
        }
        .cta-panel {
          display: grid;
          gap: 1.5rem;
          padding: 2rem 1.4rem;
        }
        .cta-actions {
          display: flex;
          flex-wrap: wrap;
          gap: 0.65rem;
        }
        .site-footer {
          border-top: 1px solid rgb(232 255 244 / 0.06);
          padding: 1.4rem 0 2rem;
        }
        .site-footer-inner {
          display: flex;
          flex-wrap: wrap;
          gap: 1rem;
          align-items: center;
          justify-content: space-between;
        }
        .footer-meta,
        .footer-links {
          display: flex;
          flex-wrap: wrap;
          gap: 0.85rem 1.2rem;
          color: rgb(var(--ice-rgb) / 0.34);
          font-size: 0.62rem;
        }
        .footer-links a:hover {
          color: var(--ice);
        }
        @media (min-width: 760px) {
          .nav { display: flex; }
          .highlight-grid { grid-template-columns: 1fr 1fr; }
          .composition-grid { grid-template-columns: 1fr 1fr 1fr; }
          .cta-panel {
            grid-template-columns: 1.4fr 1fr;
            align-items: end;
            padding: 2.4rem 2rem;
          }
        }
        @media (min-width: 1024px) {
          .hero-grid {
            grid-template-columns: minmax(0, 1.05fr) minmax(320px, 0.95fr);
            gap: 2.75rem;
            min-height: calc(100svh - 7rem);
          }
        }
      `}</style>
    </div>
  );
}
