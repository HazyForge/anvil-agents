import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { Link } from "react-router-dom";
import { listChatMessages, listChatThreads } from "../api/chat";
import { listRunProfiles, type CompositionDocument } from "../api/client";
import type { UIConfig } from "../auth/config";
import { personaLabel } from "../names";
import { loadNamespace } from "../state/namespace";
import { WRAPPER_PROFILE_NAME } from "../wrapper/intent";
import { formatTurnError, runWrapperTurn, visibleFromChat, type EntityLine, type VisibleMessage } from "../wrapper/turn";

interface Props {
  token: string;
  config: UIConfig;
}

/** Live Primaris still origin_denied for Origin http://127.0.0.1:1738 until anvil-agents#178. */
function desktopChatCanReply(chatConfigured: boolean, chatLive: boolean): boolean {
  if (!chatConfigured || !chatLive) {
    return false;
  }
  const host = window.location.hostname;
  if (host === "127.0.0.1" || host === "localhost" || host === "[::1]") {
    return false;
  }
  return true;
}

export function EntityChatPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents";
  const [namespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [messages, setMessages] = useState<VisibleMessage[]>([]);
  const [room, setRoom] = useState<EntityLine[]>([]);
  const [wrapperThreadId, setWrapperThreadId] = useState<string | null>(null);
  const [chatLive, setChatLive] = useState(Boolean(config.chat?.enabled));
  const [selected, setSelected] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const endRef = useRef<HTMLDivElement | null>(null);

  const writeEnabled = Boolean(config.composition.writeEnabled);
  const chatConfigured = Boolean(config.chat?.enabled);
  const canReply = desktopChatCanReply(chatConfigured, chatLive);
  const entityProfiles = useMemo(
    () => profiles.filter((doc) => doc.metadata.name !== WRAPPER_PROFILE_NAME),
    [profiles],
  );

  const refresh = useCallback(async () => {
    if (!namespace) {
      return;
    }
    setError(null);
    try {
      if (config.composition.readEnabled) {
        setProfiles(await listRunProfiles(token, namespace));
      }
      if (canReply && chatConfigured) {
        const threads = await listChatThreads(token, namespace, { mode: "persona", limit: 100 });
        setChatLive(true);
        const wrapper = threads.find((thread) => thread.profileName === WRAPPER_PROFILE_NAME);
        if (wrapper) {
          setWrapperThreadId(wrapper.id);
          const items = await listChatMessages(token, namespace, wrapper.id);
          setMessages(visibleFromChat(items));
        }
      } else if (!chatConfigured) {
        setChatLive(false);
      }
    } catch (err) {
      const text = formatTurnError(err);
      if (text.includes("chat_disabled") || text.includes("not_found")) {
        setChatLive(false);
      } else {
        setError(text);
      }
    } finally {
      setLoaded(true);
    }
  }, [canReply, chatConfigured, config.composition.readEnabled, namespace, token]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [messages, room]);

  async function runTurn(userText: string) {
    const text = userText.trim();
    if (!text || busy || !canReply) {
      return;
    }
    setBusy(true);
    setError(null);
    const localUser: VisibleMessage = {
      id: `local-user-${Date.now()}`,
      author: "user",
      content: text,
      createdAt: new Date().toISOString(),
    };
    setMessages((prev) => [...prev, localUser]);
    setDraft("");
    try {
      const result = await runWrapperTurn({
        token,
        namespace,
        userText: text,
        chatEnabled: canReply,
        writeEnabled,
        wrapperThreadId,
        existingProfileNames: profiles.map((doc) => doc.metadata.name),
      });
      if (result.wrapperThread) {
        setWrapperThreadId(result.wrapperThread.id);
      }
      setRoom((prev) => [...prev, ...result.room]);
      setMessages((prev) => [...prev, ...result.history]);
      if (config.composition.readEnabled) {
        setProfiles(await listRunProfiles(token, namespace));
      }
    } catch (err) {
      setError(formatTurnError(err));
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit(event?: FormEvent) {
    event?.preventDefault();
    await runTurn(draft);
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void onSubmit();
    }
  }

  const selectedName = selected || entityProfiles[0]?.metadata.name || "";

  return (
    <div className="human-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Chat</h1>
          <p className="page-sub">
            {canReply
              ? "Talk with a named cluster agent."
              : "Named agents on the cluster. This window cannot send a message yet."}
          </p>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}

      {!canReply ? (
        <div className="roster-layout">
          <section className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Cluster agents</h2>
              <span className="muted">{loaded ? entityProfiles.length : "…"}</span>
            </div>
            <div className="panel-body">
              {!loaded ? <p className="muted">Loading names…</p> : null}
              {loaded && entityProfiles.length === 0 ? (
                <p className="muted">No named agents in this workspace yet. Start work from Runs.</p>
              ) : null}
              {entityProfiles.length > 0 ? (
                <ul className="entity-list">
                  {entityProfiles.map((doc) => {
                    const name = doc.metadata.name;
                    return (
                      <li key={name}>
                        <div className="entity-item entity-item-static">
                          <span>{personaLabel(name)}</span>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              ) : null}
            </div>
          </section>
          <section className="panel human-next">
            <div className="panel-body">
              <p className="human-empty">
                Chat cannot reply from this desktop yet. Standing chat is off, and sending from here is
                blocked.
              </p>
              <Link to="/wrapper" className="btn btn-primary">
                Open Runs
              </Link>
            </div>
          </section>
        </div>
      ) : (
        <div className="roster-chat-layout">
          <aside className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Agents</h2>
              <span className="muted">{entityProfiles.length}</span>
            </div>
            <div className="panel-body">
              {entityProfiles.length === 0 ? (
                <p className="muted">No named agents yet. Start work from Runs.</p>
              ) : (
                <ul className="entity-list">
                  {entityProfiles.map((doc) => {
                    const name = doc.metadata.name;
                    const active = selectedName === name;
                    return (
                      <li key={name}>
                        <button
                          type="button"
                          className={`entity-item ${active ? "selected" : ""}`}
                          onClick={() => setSelected(name)}
                        >
                          <span>{personaLabel(name)}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>
          </aside>
          <section className="panel entity-chat-main">
            <div className="panel-header">
              <h2 className="panel-title">{selectedName ? personaLabel(selectedName) : "Conversation"}</h2>
            </div>
            <div className="chat-messages" aria-live="polite">
              {messages.length === 0 && room.length === 0 ? (
                <div className="empty">No messages yet.</div>
              ) : null}
              {messages.map((message) => (
                <article key={message.id} className={`chat-bubble chat-bubble-${message.author}`}>
                  <header className="chat-bubble-header">
                    <span className="chat-bubble-role">{personaLabel(message.profile || message.author)}</span>
                  </header>
                  <pre className="chat-bubble-body">{message.content}</pre>
                </article>
              ))}
              {room.map((line, index) => (
                <article key={`room-${line.author}-${index}`} className="chat-bubble chat-bubble-entity">
                  <header className="chat-bubble-header">
                    <span className="chat-bubble-role">{personaLabel(line.author)}</span>
                  </header>
                  <pre className="chat-bubble-body">{line.content}</pre>
                </article>
              ))}
              <div ref={endRef} />
            </div>
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
                />
              </label>
              <div className="chat-composer-actions">
                <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim()}>
                  {busy ? "Sending…" : "Send"}
                </button>
              </div>
            </form>
          </section>
        </div>
      )}
    </div>
  );
}
