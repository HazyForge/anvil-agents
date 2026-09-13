import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { completeLoginFromCallback, takeReturnPath } from "../auth/oidc";

interface Props {
  onAuthenticated: (accessToken: string) => void;
}

export function AuthCallbackPage({ onAuthenticated }: Props) {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const session = await completeLoginFromCallback(window.location.search);
        if (cancelled) {
          return;
        }
        onAuthenticated(session.accessToken);
        const returnTo = takeReturnPath();
        navigate(returnTo, { replace: true });
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [navigate, onAuthenticated]);

  if (error) {
    return (
      <div className="panel token-gate">
        <h1>Sign-in failed</h1>
        <div className="banner banner-error">{error}</div>
        <button type="button" className="btn btn-primary" onClick={() => navigate("/", { replace: true })}>
          Back to desktop
        </button>
      </div>
    );
  }

  return (
    <div className="panel token-gate">
      <h1>Completing sign-in…</h1>
      <p>Exchanging authorization code for an access token. The code is then stripped from the address bar.</p>
    </div>
  );
}
