import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { listChatMessages, listChatThreads } from "../api/chat";
import { listRunProfiles, type CompositionDocument } from "../api/client";
import type { UIConfig } from "../auth/config";
import { personaLabel } from "../names";
import { loadNamespace, saveNamespace } from "../state/namespace";
import { dnsLabel, parseWrapperIntent, WRAPPER_PROFILE_NAME } from "../wrapper/intent";
import { formatTurnError, runWrapperTurn, visibleFromChat, type EntityLine, type VisibleMessage } from "../wrapper/turn";

interface Props {
  token: string;
  config: UIConfig;
}

const DEMO_PROMPT = "Create two agents named scout and cartographer, then have them greet each other.";

export function EntityChatPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [spawnName, setSpawnName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [messages, setMessages] = useState<VisibleMessage[]>([]);
  const [room, setRoom] = useState<EntityLine[]>([]);
  const [wrapperThreadId, setWrapperThreadId] = useState<string | null>(null);
  const [chatLive, setChatLive] = useState(Boolean(config.chat?.enabled));
  const [selected, setSelected] = useState<string | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);

  const writeEnabled = Boolean(config.composition.writeEnabled);
  const chatEnabled = Boolean(config.chat?.enabled) && chatLive;
  const entityProfiles = useMemo(
    () => profiles.filter((doc) => doc.metadata.name !== WRAPPER_PROFILE_NAME),
    [profiles],
  );

  function setNs(next: string) {
    setNamespace(next);
    saveNamespace(next);
  }

  const refresh = useCallback(async () => {
    if (!namespace) {
      return;
    }
    setError(null);
    try {
      if (config.composition.readEnabled) {
        setProfiles(await listRunProfiles(token, namespace));
      }
      if (config.chat?.enabled) {
        const threads = await listChatThreads(token, namespace, { mode: "persona", limit: 100 });
        setChatLive(true);
        const wrapper = threads.find((thread) => thread.profileName === WRAPPER_PROFILE_NAME);
        if (wrapper) {
          setWrapperThreadId(wrapper.id);
          const items = await listChatMessages(token, namespace, wrapper.id);
          setMessages(visibleFromChat(items));
        }
      }
    } catch (err) {
      const text = formatTurnError(err);
      if (text.includes("chat_disabled") || text.includes("not_found")) {
        setChatLive(false);
      } else {
        setError(text);
      }
    }
  }, [config.chat?.enabled, config.composition.readEnabled, namespace, token]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [messages, room]);

  async function runTurn(userText: string) {
    const text = userText.trim();
    if (!text || busy) {
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
        chatEnabled,
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
      } else {
        setProfiles((prev) => mergeProfiles(prev, result.spawned));
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

  async function onSpawnNamed() {
    const name = dnsLabel(spawnName);
    if (!name) {
      setError("spawn name must be a DNS label (lowercase, digits, hyphens)");
      return;
    }
    await runTurn(`Create an agent named ${name}.`);
    setSpawnName("");
  }

  async function onHaveThemTalk() {
    if (entityProfiles.length < 2) {
      setError("spawn at least two agents before they can talk");
      return;
    }
    await runTurn(`Have ${entityProfiles[0].metadata.name} and ${entityProfiles[1].metadata.name} greet each other.`);
  }

  const intentPreview = parseWrapperIntent(
    draft,
    profiles.map((doc) => doc.metadata.name),
  );

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Agents</h1>
          <p className="page-sub">
            Talk to named Primaris agents over the OIDC API. The wrapper can POST AgentRunProfiles and
            have them speak on standing-chat threads. This is not kubectl and not a local CLI.
          </p>
        </div>
        <div className="chip-row">
          <span className={`chip ${writeEnabled ? "chip-ok" : ""}`}>
            {writeEnabled ? "composition write" : "write off"}
          </span>
          <span className={`chip ${chatEnabled ? "chip-ok" : ""}`}>
            {chatEnabled ? "standing chat" : "chat local/off"}
          </span>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {!writeEnabled ? (
        <div className="banner banner-info">
          composition.writeEnabled is false. The API must grant composition write before Desktop can
          create agents.
        </div>
      ) : null}

      <div className="entity-layout">
        <aside className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Agents</h2>
            <span className="muted">{entityProfiles.length}</span>
          </div>
          <div className="panel-body">
            {entityProfiles.length === 0 ? (
              <p className="muted">No AgentRunProfiles in this namespace yet. Spawn one on the right, or ask the wrapper.</p>
            ) : (
              <ul className="entity-list">
                {entityProfiles.map((doc) => {
                  const name = doc.metadata.name;
                  const active = selected === name;
                  return (
                    <li key={name}>
                      <button
                        type="button"
                        className={`entity-item ${active ? "selected" : ""}`}
                        onClick={() => setSelected(name)}
                      >
                        <span>{personaLabel(name)}</span>
                        <span className="chip chip-ok">online</span>
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
            <h2 className="panel-title">Conversation</h2>
            <span className="chip mono">{WRAPPER_PROFILE_NAME}</span>
          </div>
          <div className="chat-messages" aria-live="polite">
            {messages.length === 0 ? (
              <div className="empty">Ask the wrapper to create or talk to named agents.</div>
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
                placeholder={DEMO_PROMPT}
                disabled={busy || !writeEnabled}
              />
            </label>
            {draft.trim() && intentPreview.spawn ? (
              <p className="muted">
                Will spawn {intentPreview.names.map(personaLabel).join(", ") || "—"}
                {intentPreview.talk ? " and have them talk" : ""}.
              </p>
            ) : null}
            <div className="chat-composer-actions">
              <button
                type="button"
                className="btn"
                disabled={busy || !writeEnabled}
                onClick={() => void runTurn(DEMO_PROMPT)}
              >
                Spawn two and let them talk
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim() || !writeEnabled}>
                {busy ? "Working…" : "Send"}
              </button>
            </div>
          </form>
        </section>

        <aside className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Namespace</h2>
          </div>
          <div className="panel-body">
            <label className="field">
              <span className="label">Namespace</span>
              <input
                className="input"
                value={namespace}
                onChange={(event) => setNs(event.target.value)}
                placeholder={fallbackNs}
              />
            </label>
            <p className="muted">
              From ui-config: {config.defaultNamespaces.join(", ") || "none"}. This is not a kube
              context picker.
            </p>
            <label className="field">
              <span className="label">Spawn agent</span>
              <input
                className="input"
                value={spawnName}
                onChange={(event) => setSpawnName(event.target.value)}
                placeholder="scout"
                spellCheck={false}
              />
            </label>
            <div className="btn-row">
              <button type="button" className="btn btn-primary" disabled={busy || !writeEnabled} onClick={() => void onSpawnNamed()}>
                Spawn agent
              </button>
              <button type="button" className="btn" disabled={busy || !writeEnabled} onClick={() => void onHaveThemTalk()}>
                Have them talk
              </button>
            </div>
            <p className="muted">No Kubernetes context. No kubectl. On-machine CLIs live on Local.</p>
          </div>
        </aside>
      </div>
    </div>
  );
}

function mergeProfiles(prev: CompositionDocument[], spawned: CompositionDocument[]): CompositionDocument[] {
  const byName = new Map(prev.map((doc) => [doc.metadata.name, doc]));
  for (const doc of spawned) {
    byName.set(doc.metadata.name, doc);
  }
  return [...byName.values()];
}
