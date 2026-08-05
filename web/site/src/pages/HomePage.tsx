import { Link } from "react-router-dom";
import AgentCouncil from "../components/AgentCouncil";
import ClusterFieldBackground from "../components/ClusterFieldBackground";

const GITHUB = "https://github.com/HazyForge/anvil-agents";

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
    name: "Optional OIDC API",
    detail:
      "Optional read API and live SSE logs so operators can inspect runs with their own OIDC-protected deployment.",
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

export default function HomePage() {
  return (
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
              composable profiles, durable storage, and an observable run record.
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
              <Link className="btn btn-primary" to="/docs/getting-started">
                Read the docs
              </Link>
              <a className="btn btn-ghost" href={GITHUB}>
                View on GitHub
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
              <Link
                className="inline-link"
                to="/docs/blog/agents-as-cluster-jobs-not-chatbots"
              >
                Read the essay
              </Link>
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
          <div className="home-doc-images">
            <Link to="/docs/architecture" className="home-doc-image panel">
              <img
                src="/docs/architecture-overview.jpg"
                alt="Architecture overview diagram"
                loading="lazy"
              />
              <span className="mono">Architecture →</span>
            </Link>
            <Link to="/docs/composition" className="home-doc-image panel">
              <img
                src="/docs/composition-model.jpg"
                alt="Composition model diagram"
                loading="lazy"
              />
              <span className="mono">Composition →</span>
            </Link>
          </div>
        </div>
      </section>

      <section id="docs" className="section">
        <div className="container panel cta-panel">
          <div>
            <div className="eyebrow">Documentation</div>
            <h2 className="display section-title">
              Install, compose,
              <span className="soft"> operate</span>
            </h2>
            <p className="section-lead">
              Full operator docs live on this site — getting started, harnesses,
              security, streams, and more — with diagrams that match how the
              system actually runs.
            </p>
          </div>
          <div className="cta-actions">
            <Link className="btn btn-primary" to="/docs">
              Browse docs
            </Link>
            <Link className="btn btn-ghost" to="/docs/getting-started">
              Getting started
            </Link>
            <a className="btn btn-ghost" href={GITHUB}>
              View on GitHub
            </a>
          </div>
        </div>
      </section>
    </main>
  );
}
