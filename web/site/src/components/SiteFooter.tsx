import { Link } from "react-router-dom";

const GITHUB = "https://github.com/HazyForge/anvil-agents";
const CONSOLE = "https://agents.anvil.hazyforge.io";

export default function SiteFooter() {
  return (
    <footer className="site-footer">
      <div className="container site-footer-inner">
        <div className="mono footer-meta">
          <span>© {new Date().getFullYear()} Hazy Forge</span>
          <span>anvil-agents.hazyforge.io</span>
        </div>
        <div className="footer-links mono">
          <Link to="/docs">Docs</Link>
          <a href={GITHUB}>GitHub</a>
          <a href={CONSOLE}>Console</a>
          <a href="https://hazyforge.io">Hazy Forge</a>
        </div>
      </div>
    </footer>
  );
}
