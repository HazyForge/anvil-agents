import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import type { UIConfig } from "../auth/config";
import { loadNamespace } from "../state/namespace";
import { loadManagerHarnessHistory, streamDesktopChat } from "../wrapper/harnessChat";
import { formatTurnError } from "../wrapper/turn";

interface Props {
  token: string;
  config: UIConfig;
}

type ChatLine = {
  id: string;
  kind: "user" | "harness" | "honest";
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

  useEffect(() => {
    if (!token) {
      return;
    }
    const controller = new AbortController();
    void loadManagerHarnessHistory({ token, namespace, signal: controller.signal })
      .then((history) => {
        if (!controller.signal.aborted && history.lines.length > 0) {
          setLines(history.lines);
        }
      })
      .catch(() => {
        /* standing chat missing is the empty composer, not a fake reply */
      });
    return () => controller.abort();
  }, [token, namespace]);

  async function onSubmit(event?: FormEvent) {
    event?.preventDefault();
    const text = draft.trim();
    if (!text || busy || !canSend) {
      return;
    }
    setBusy(true);
    setError(null);
    setDraft("");
    const replyId = `reply-${Date.now()}`;
    setLines((prev) => [...prev, { id: `user-${Date.now()}`, kind: "user", content: text }]);
    try {
      const result = await streamDesktopChat({
        token,
        namespace,
        text,
        onDelta: (chunk) => {
          setLines((prev) => {
            const existing = prev.find((line) => line.id === replyId);
            if (!existing) {
              return [...prev, { id: replyId, kind: "harness", content: chunk }];
            }
            return prev.map((line) =>
              line.id === replyId ? { ...line, kind: "harness", content: line.content + chunk } : line,
            );
          });
        },
      });
      setLines((prev) => {
        const existing = prev.find((line) => line.id === replyId);
        if (existing) {
          return prev.map((line) =>
            line.id === replyId
              ? { ...line, kind: result.source === "harness" ? "harness" : "honest", content: result.text }
              : line,
          );
        }
        return [
          ...prev,
          {
            id: replyId,
            kind: result.source === "harness" ? "harness" : "honest",
            content: result.text,
          },
        ];
      });
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
              ? "Talk to the always-on manager harness. Hello is a message, not a run."
              : "Sign in to chat with the manager harness."}
          </p>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}

      <section className="panel entity-chat-main">
        <div className="chat-messages" aria-live="polite">
          {lines.length === 0 ? (
            <div className="empty">{canSend ? "Message the manager harness." : null}</div>
          ) : null}
          {lines.map((line) => (
            <article
              key={line.id}
              className={`chat-bubble ${line.kind === "user" ? "chat-bubble-user" : "chat-bubble-run"}`}
            >
              <header className="chat-bubble-header">
                <span className="chat-bubble-role">
                  {line.kind === "user" ? "You" : line.kind === "harness" ? "Manager" : "Desktop"}
                </span>
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
                placeholder="hello"
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
            Sign in to chat with the manager harness.
          </p>
        )}
      </section>
    </div>
  );
}
