import { useEffect, useState } from "react";
import { beginLogin } from "../auth/oidc";
import { saveStubSession } from "../auth/session";
import { DEFAULT_API_ORIGIN, PRODUCT_TITLE } from "../product";
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
  issuer,
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
  const redirect = `${window.location.origin}/auth/callback`;

  return (
    <div className="panel token-gate">
      <h1>Sign in to {PRODUCT_TITLE}</h1>
      <p>
        Authorization Code + PKCE to the anvil-agents OIDC API (Anvil Primaris). This process talks
        to cluster agents — there is no kubeconfig, kubectl, or cluster context picker. Access
        tokens stay in <code>sessionStorage</code> and are never placed in query strings.
      </p>
      <label className="field">
        <span className="label">OIDC API origin</span>
        <input
          className="input"
          placeholder={DEFAULT_API_ORIGIN}
          value={originDraft}
          onChange={(event) => setOriginDraft(event.target.value)}
          autoComplete="off"
        />
      </label>
      <div className={`banner ${apiReachable ? "banner-ok" : "banner-info"}`}>
        {apiMessage || "Save the Primaris API origin, then sign in. The host reverse-proxies /api and /ui-config.json."}
      </div>
      {issuer ? (
        <p className="muted">
          Issuer from ui-config: <span className="mono">{issuer}</span>. Native redirect{" "}
          <span className="mono">{redirect}</span>.
        </p>
      ) : null}
      {oidcClientSource === "native" && oidcClientId ? (
        <p className="muted">
          Native PKCE client <span className="mono">{oidcClientId}</span>.
        </p>
      ) : null}
      {oidcClientSource === "console" && oidcClientId ? (
        <div className="banner banner-warn">
          ui-config has no <span className="mono">desktop.oidcClientId</span>. Signing in with the
          Console User-Agent client <span className="mono">{oidcClientId}</span>, not Native. Register{" "}
          <span className="mono">{redirect}</span> on the Native app when GitOps publishes that id.
          Cluster agents stay the product.
        </div>
      ) : null}
      {error ? <div className="banner banner-error">{error}</div> : null}
      {signInError ? <div className="banner banner-error">{signInError}</div> : null}
      <div className="btn-row">
        <button type="button" className="btn" disabled={busy} onClick={() => void persistDraft()}>
          {busy ? "Saving" : "Save API origin"}
        </button>
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => void handleSignIn()}
          disabled={signInBusy || !(originDraft.trim() || apiOrigin)}
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
