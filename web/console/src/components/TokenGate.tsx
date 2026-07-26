import { useState, type FormEvent } from "react";

interface Props {
  initialToken?: string;
  onSubmit: (token: string) => void;
}

export function TokenGate({ initialToken = "", onSubmit }: Props) {
  const [token, setToken] = useState(initialToken);

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const value = token.trim();
    if (!value) {
      return;
    }
    onSubmit(value);
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-title">Anvil Agents Console</div>
          <div className="brand-sub">Read-only observer</div>
        </div>
      </header>
      <main className="main">
        <form className="panel token-gate" onSubmit={handleSubmit}>
          <h1>Bearer access token</h1>
          <p>
            Paste a JWT access token with <code>anvil-agents:runs:read</code> (and stream)
            for the namespaces you need. The token is stored in{" "}
            <code>sessionStorage</code> for this tab only — never in query strings.
          </p>
          <label className="field">
            <span className="label">Authorization bearer</span>
            <textarea
              className="textarea"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="eyJ..."
              autoComplete="off"
              spellCheck={false}
              required
            />
          </label>
          <div style={{ marginTop: "0.85rem" }}>
            <button type="submit" className="btn btn-primary">
              Enter console
            </button>
          </div>
        </form>
      </main>
    </div>
  );
}
