import { useEffect, useState } from "react";
import { beginLogin } from "../auth/oidc";
import { saveStubSession } from "../auth/session";
import { PRODUCT_TITLE } from "../product";

interface Props {
  apiOrigin: string;
  apiMessage?: string;
  apiReachable?: boolean;
  issuer?: string;
  stubSession?: boolean;
  error?: string | null;
  busy?: boolean;
  onSaveOrigin: (origin: string) => Promise<void> | void;
  onAuthenticated?: (accessToken: string) => void;
}

function isLoopbackHost(): boolean {
  const host = window.location.hostname;
  return host === "127.0.0.1" || host === "localhost" || host === "[::1]";
}

export function LoginGate({
  apiOrigin,
  apiMessage,
  apiReachable = false,
  issuer,
  stubSession = false,
  error = null,
  busy = false,
  onSaveOrigin,
  onAuthenticated,
}: Props) {
  const [originDraft, setOriginDraft] = useState(apiOrigin);
  const [signInBusy, setSignInBusy] = useState(false);
  const [signInError, setSignInError] = useState<string | null>(null);

  useEffect(() => {
    setOriginDraft(apiOrigin);
  }, [apiOrigin]);

  async function handleSignIn() {
    setSignInBusy(true);
    try {
      await beginLogin(window.location.pathname + window.location.search);
    } catch (err) {
      setSignInError(err instanceof Error ? err.message : String(err));
      setSignInBusy(false);
    }
  }

  function handleStubSession() {
    const session = saveStubSession();
    onAuthenticated?.(session.accessToken);
  }

  const allowStub = stubSession && isLoopbackHost();

  return (
    <div className="panel token-gate">
      <h1>Sign in to {PRODUCT_TITLE}</h1>
      <p>
        This app talks only to the anvil-agents OIDC API — the same AgentRun API the browser console
        uses. There is no kubeconfig, kubectl, or cluster context picker. Sign in with Authorization
        Code + PKCE. Access tokens stay in <code>sessionStorage</code> for this window and are never
        placed in query strings after login.
      </p>
      <label className="field">
        <span className="label">OIDC API origin</span>
        <input
          className="input"
          placeholder="https://agents.example.com"
          value={originDraft}
          onChange={(event) => setOriginDraft(event.target.value)}
          autoComplete="off"
        />
      </label>
      <div className={`banner ${apiReachable ? "banner-ok" : "banner-info"}`}>
        {apiMessage || "Save an API origin. The host reverse-proxies /api and /ui-config.json to that origin."}
      </div>
      {issuer ? (
        <p className="muted">
          Issuer from ui-config: <span className="mono">{issuer}</span>. Register redirect{" "}
          <span className="mono">{window.location.origin}/auth/callback</span> on that OIDC client.
        </p>
      ) : null}
      {error ? <div className="banner banner-error">{error}</div> : null}
      {signInError ? <div className="banner banner-error">{signInError}</div> : null}
      <div className="btn-row">
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => void onSaveOrigin(originDraft)}
        >
          {busy ? "Saving" : "Save API origin"}
        </button>
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => void handleSignIn()}
          disabled={signInBusy || !apiOrigin}
        >
          {signInBusy ? "Redirecting…" : "Sign in with OIDC"}
        </button>
        {allowStub ? (
          <button type="button" className="btn" onClick={handleStubSession}>
            Open stub session
          </button>
        ) : null}
      </div>
      {allowStub ? (
        <p className="muted">
          Loopback stub API only. Kind and Zitadel still use Sign in with OIDC. The stub token is not an
          IdP credential.
        </p>
      ) : null}
    </div>
  );
}
