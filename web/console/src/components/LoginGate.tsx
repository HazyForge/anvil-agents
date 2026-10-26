import { useEffect, useState } from "react";
import { loadUIConfig } from "../auth/config";
import { beginLogin } from "../auth/oidc";
import { BrandMark } from "./BrandMark";
import { Icon } from "./Icon";

interface Props {
  error?: string | null;
}

export function LoginGate({ error = null }: Props) {
  const [busy, setBusy] = useState(false);
  const [title, setTitle] = useState("Anvil Agents Console");
  const [configError, setConfigError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void loadUIConfig()
      .then((config) => {
        if (!cancelled) {
          setTitle(config.productTitle);
          setConfigError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setConfigError(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleSignIn() {
    setBusy(true);
    try {
      await beginLogin(window.location.pathname + window.location.search);
    } catch (err) {
      setConfigError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  }

  return (
    <div className="login-shell">
      <main className="login-card plate">
        <header className="login-brand">
          <BrandMark className="login-mark" />
          <div className="brand">
            <div className="brand-title">{title}</div>
            <div className="brand-sub">Read-only observer</div>
          </div>
        </header>
        <div className="token-gate">
          <h1>Sign in</h1>
          <p>
            Use your Hazy Forge account via OIDC Authorization Code + PKCE. Access tokens stay in{" "}
            <code>sessionStorage</code> for this tab and are never placed in query strings after
            login.
          </p>
          {error ? <div className="banner banner-error">{error}</div> : null}
          {configError ? <div className="banner banner-error">{configError}</div> : null}
          <button
            type="button"
            className="btn btn-primary login-button"
            onClick={() => void handleSignIn()}
            disabled={busy}
          >
            {busy ? "Redirecting…" : "Sign in with Hazy Forge"}
          </button>
          <div className="trust-line" aria-hidden="true">
            <span>
              <Icon name="key" /> OIDC · PKCE
            </span>
            <span>
              <Icon name="lock" /> Tab-scoped session
            </span>
            <span>
              <Icon name="globe" /> No tokens in URLs
            </span>
          </div>
        </div>
      </main>
    </div>
  );
}
