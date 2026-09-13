import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  AUDITOR_GROK_PROFILE,
  GROK_BACKEND,
  createAgentRun,
} from "../api/client";
import type { UIConfig } from "../auth/config";
import { personaLabel } from "../names";
import { loadNamespace } from "../state/namespace";
import { formatTurnError } from "../wrapper/turn";

interface Props {
  token: string;
  config: UIConfig;
}

type ChatLine = {
  id: string;
  kind: "user" | "run";
  content: string;
  runName?: string;
};

export function EntityChatPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents";
  const [namespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lines, setLines] = useState<ChatLine[]>([]);
  const endRef = useRef<HTMLDivElement | null>(null);

  const canSend = Boolean(token) && Boolean(config.runs.createEnabled);

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
    const userLine: ChatLine = {
      id: `user-${Date.now()}`,
      kind: "user",
      content: text,
    };
    setLines((prev) => [...prev, userLine]);
    try {
      const created = await createAgentRun(token, namespace, {
        generateName: "desktop-chat-",
        prompt: text,
        profileName: AUDITOR_GROK_PROFILE,
        backend: GROK_BACKEND,
      });
      const name = created.name?.trim() || "";
      setLines((prev) => [
        ...prev,
        {
          id: `run-${name || Date.now()}`,
          kind: "run",
          content: name
            ? `Started run ${name}. The cluster harness is taking this command.`
            : "Started a run. The cluster harness is taking this command.",
          runName: name || undefined,
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
              ? `Each message starts a ${personaLabel(AUDITOR_GROK_PROFILE)} run on the cluster.`
              : !token
                ? "Sign in to send a message."
                : "This server does not allow starting runs from Chat."}
          </p>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}

      <section className="panel entity-chat-main">
        <div className="chat-messages" aria-live="polite">
          {lines.length === 0 ? (
            <div className="empty">
              {canSend ? "Write what the agent should do." : null}
            </div>
          ) : null}
          {lines.map((line) => (
            <article
              key={line.id}
              className={`chat-bubble ${line.kind === "user" ? "chat-bubble-user" : "chat-bubble-run"}`}
            >
              <header className="chat-bubble-header">
                <span className="chat-bubble-role">{line.kind === "user" ? "You" : "Run"}</span>
              </header>
              <pre className="chat-bubble-body">{line.content}</pre>
            </article>
          ))}
          <div ref={endRef} />
        </div>
        {canSend ? (
          <form className="chat-composer" onSubmit={(event) => void onSubmit(event)}>
            <label className="field">
              <span className="label">Message</span>
              <textarea
                className="textarea chat-composer-input"
                rows={3}
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={onComposerKeyDown}
                placeholder="Write a message"
                disabled={busy}
                aria-label="Message"
              />
            </label>
            <div className="chat-composer-actions">
              <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim()}>
                {busy ? "Sending…" : "Send"}
              </button>
            </div>
          </form>
        ) : (
          <p className="human-empty" style={{ padding: "0.75rem" }}>
            {!token ? "Sign in to send a message." : "Starting a run is turned off on this server."}
          </p>
        )}
      </section>
    </div>
  );
}
