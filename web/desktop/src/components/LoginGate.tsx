import { useEffect, useState } from "react";
import { beginLogin } from "../auth/oidc";
import { saveStubSession } from "../auth/session";
import { DEFAULT_API_ORIGIN } from "../product";
import type { OIDCClientSource } from "../auth/config";

interface Props {
  apiOrigin: string;
  apiMessage?: string;
  apiReachable?: boolean;
  issuer?: string;
  oidcClientId?: string;
  oidcClientSource?: OIDCClientSource;
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
  oidcClientId,
  oidcClientSource,
  stubSession = false,
  error = null,
  busy = false,
  onSaveOrigin,
  onAuthenticated,
}: Props) {
  const [originDraft, setOriginDraft] = useState(apiOrigin || DEFAULT_API_ORIGIN);
  const [signInBusy, setSignInBusy] = useState(false);
  const [signInError, setSignInError] = useState<string | null>(null);
  const [showOrigin, setShowOrigin] = useState(!apiOrigin);

  useEffect(() => {
    setOriginDraft(apiOrigin || DEFAULT_API_ORIGIN);
  }, [apiOrigin]);

  async function persistDraft(): Promise<string> {
    const origin = originDraft.trim() || DEFAULT_API_ORIGIN;
    if (origin !== apiOrigin) {
      await onSaveOrigin(origin);
    }
    return origin;
  }

  async function handleSignIn() {
    setSignInBusy(true);
    setSignInError(null);
    try {
      await persistDraft();
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
      <h1>Sign in</h1>
      <p>Use your Hazy Forge account to see cluster agents and runs.</p>
      {oidcClientSource === "console" && oidcClientId ? (
        <div className="banner banner-warn">
          This build is still using the Console sign-in app, not the Desktop one. Sign-in may fail on
          loopback until Desktop is published.
        </div>
      ) : null}
      {error ? <div className="banner banner-error">{error}</div> : null}
      {signInError ? <div className="banner banner-error">{signInError}</div> : null}
      {!apiReachable && apiMessage ? <div className="banner banner-info">{apiMessage}</div> : null}
      <div className="btn-row">
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => void handleSignIn()}
          disabled={signInBusy || busy || !(originDraft.trim() || apiOrigin)}
        >
          {signInBusy ? "Redirecting…" : "Sign in"}
        </button>
        {allowStub ? (
          <button type="button" className="btn" onClick={handleStubSession}>
            Open stub session
          </button>
        ) : null}
      </div>
      <button type="button" className="btn btn-ghost" onClick={() => setShowOrigin((open) => !open)}>
        {showOrigin ? "Hide server" : "Change server"}
      </button>
      {showOrigin ? (
        <label className="field">
          <span className="label">Server</span>
          <input
            className="input"
            placeholder={DEFAULT_API_ORIGIN}
            value={originDraft}
            onChange={(event) => setOriginDraft(event.target.value)}
            autoComplete="off"
          />
        </label>
      ) : null}
      {allowStub ? (
        <p className="muted">Stub session is local only. It is not a real account.</p>
      ) : null}
    </div>
  );
}
