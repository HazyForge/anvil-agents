import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import type { UIConfig } from "../auth/config";
import { loadNamespace } from "../state/namespace";
import { runCoordinatorCommand } from "../wrapper/coordinator";
import { formatTurnError } from "../wrapper/turn";

interface Props {
  token: string;
  config: UIConfig;
}

type ChatLine = {
  id: string;
  kind: "user" | "coordinator";
  content: string;
};

export function EntityChatPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents";
  const [namespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lines, setLines] = useState<ChatLine[]>([]);
  const endRef = useRef<HTMLDivElement | null>(null);

  const canSend = Boolean(token);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [lines]);

  async function onSubmit(event?: FormEvent) {
    event?.preventDefault();
    const text = draft.trim();
    if (!text || busy || !canSend) {
      return;
    }
    setBusy(true);
    setError(null);
    setDraft("");
    setLines((prev) => [...prev, { id: `user-${Date.now()}`, kind: "user", content: text }]);
    try {
      const result = await runCoordinatorCommand({
        token,
        namespace,
        createEnabled: Boolean(config.runs.createEnabled),
        text,
      });
      setLines((prev) => [
        ...prev,
        {
          id: `coord-${Date.now()}`,
          kind: "coordinator",
          content: result.text,
        },
      ]);
    } catch (err) {
      setError(formatTurnError(err));
    } finally {
      setBusy(false);
    }
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void onSubmit();
    }
  }

  return (
    <div className="human-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Chat</h1>
          <p className="page-sub">
            {canSend
              ? "Commands for the Desktop coordinator on Primaris. Not a local CLI."
              : "Sign in to send a command."}
          </p>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}

      <section className="panel entity-chat-main">
        <div className="chat-messages" aria-live="polite">
          {lines.length === 0 ? (
            <div className="empty">{canSend ? "status · start <agent> <work> · request peer for <run>" : null}</div>
          ) : null}
          {lines.map((line) => (
            <article
              key={line.id}
              className={`chat-bubble ${line.kind === "user" ? "chat-bubble-user" : "chat-bubble-run"}`}
            >
              <header className="chat-bubble-header">
                <span className="chat-bubble-role">{line.kind === "user" ? "You" : "Coordinator"}</span>
              </header>
              <pre className="chat-bubble-body">{line.content}</pre>
            </article>
          ))}
          <div ref={endRef} />
        </div>
        {canSend ? (
          <form className="chat-composer" onSubmit={(event) => void onSubmit(event)}>
            <label className="field">
              <span className="label">Command</span>
              <textarea
                className="textarea chat-composer-input"
                rows={3}
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={onComposerKeyDown}
                placeholder="status"
                disabled={busy}
                aria-label="Command"
              />
            </label>
            <div className="chat-composer-actions">
              <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim()}>
                {busy ? "Working…" : "Send"}
              </button>
            </div>
          </form>
        ) : (
          <p className="human-empty" style={{ padding: "0.75rem" }}>
            Sign in to send a command.
          </p>
        )}
      </section>
    </div>
  );
}
