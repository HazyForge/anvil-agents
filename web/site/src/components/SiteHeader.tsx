import { Link, NavLink } from "react-router-dom";

const GITHUB = "https://github.com/HazyForge/anvil-agents";

export default function SiteHeader() {
  return (
    <header className="site-header">
      <div className="container site-header-inner">
        <Link className="brand" to="/" aria-label="Anvil Agents home">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-text">
            <span className="mono brand-kicker">Hazy Forge</span>
            <span className="display brand-name">Anvil Agents</span>
          </span>
        </Link>
        <nav className="nav mono" aria-label="Primary">
          <NavLink to="/" end>
            Home
          </NavLink>
          <NavLink to="/docs">Docs</NavLink>
          <NavLink to="/docs/blog/agents-as-cluster-jobs-not-chatbots">
            Blog
          </NavLink>
          <a href={GITHUB}>GitHub</a>
        </nav>
        <div className="header-actions">
          <Link className="btn btn-ghost" to="/docs/getting-started">
            Docs
          </Link>
          <a className="btn btn-primary" href={GITHUB}>
            GitHub
          </a>
        </div>
      </div>
    </header>
  );
}
