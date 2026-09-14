import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  appendChatMessage,
  createChatThread,
  getChatThread,
  listChatThreads,
} from "../api/chat";
import { APIError } from "../api/client";
import { listComposition } from "../api/composition";
import type { ChatMessage, ChatThread } from "../api/types.chat";
import { formatTime } from "../utils/format";

interface Props {
  token: string;
  namespace: string;
  onViewNamespace?: (namespace: string) => void;
}

const THREAD_LIMIT = 100;

function isStubMetadata(metadata: unknown): boolean {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) {
    return false;
  }
  return Boolean((metadata as { stub?: unknown }).stub);
}

function stubEngine(metadata: unknown): string {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) {
    return "";
  }
  const engine = (metadata as { engine?: unknown }).engine;
  return typeof engine === "string" ? engine.trim() : "";
}

function messageKey(message: ChatMessage): string {
  return message.id || `${message.threadId}-${message.sequence}`;
}

export function ChatPage({ token, namespace: activeNamespace, onViewNamespace }: Props) {
  const params = useParams();
  const navigate = useNavigate();
  const routeNamespace = params.namespace?.trim() ?? "";
  const threadID = params.threadId?.trim() ?? "";
  const namespace = routeNamespace || activeNamespace;

  const [threads, setThreads] = useState<ChatThread[]>([]);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [selectedThread, setSelectedThread] = useState<ChatThread | null>(null);
  const [profiles, setProfiles] = useState<string[]>([]);
  const [profileFilter, setProfileFilter] = useState("");
  const [newProfile, setNewProfile] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [draft, setDraft] = useState("");
  const [listLoading, setListLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [sending, setSending] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  const messagesEndRef = useRef<HTMLDivElement | null>(null);
  const listRequestRef = useRef(0);
  const detailRequestRef = useRef(0);

  useEffect(() => {
    if (!routeNamespace) {
      return;
    }
    onViewNamespace?.(routeNamespace);
  }, [routeNamespace, onViewNamespace]);

  const loadThreads = useCallback(
    async (signal?: AbortSignal) => {
      const requestId = ++listRequestRef.current;
      if (!namespace) {
        setThreads([]);
        setListLoading(false);
        setListError("Select or add a namespace.");
        return;
      }
      setListLoading(true);
      setListError(null);
      try {
        const items = await listChatThreads(
          token,
          namespace,
          {
            limit: THREAD_LIMIT,
            mode: "persona",
            profileName: profileFilter || undefined,
          },
          signal,
        );
        if (requestId !== listRequestRef.current) {
          return;
        }
        setThreads(items);
      } catch (err) {
        if (signal?.aborted || requestId !== listRequestRef.current) {
          return;
        }
        setThreads([]);
        setListError(
          err instanceof APIError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err),
        );
      } finally {
        if (requestId === listRequestRef.current) {
          setListLoading(false);
        }
      }
    },
    [token, namespace, profileFilter],
  );

  useEffect(() => {
    const controller = new AbortController();
    void loadThreads(controller.signal);
    return () => {
      controller.abort();
      listRequestRef.current += 1;
    };
  }, [loadThreads]);

  useEffect(() => {
    if (!namespace || !token) {
      setProfiles([]);
      return;
    }
    const controller = new AbortController();
    void listComposition(token, namespace, "agent-run-profiles", 200, controller.signal)
      .then((docs) => {
        setProfiles(docs.map((doc) => doc.metadata.name).filter(Boolean).sort());
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setProfiles([]);
        }
      });
    return () => controller.abort();
  }, [token, namespace]);

  useEffect(() => {
    if (!newProfile && profiles.length > 0) {
      setNewProfile(profiles[0] ?? "");
    }
  }, [profiles, newProfile]);

  const loadDetail = useCallback(async () => {
    const requestId = ++detailRequestRef.current;
    if (!namespace || !threadID) {
      setSelectedThread(null);
      setMessages([]);
      setDetailError(null);
      setDetailLoading(false);
      return;
    }
    const controller = new AbortController();
    setDetailLoading(true);
    setDetailError(null);
    try {
      const detail = await getChatThread(token, namespace, threadID, controller.signal);
      if (requestId !== detailRequestRef.current) {
        return;
      }
      const { messages: detailMessages, ...thread } = detail;
      setSelectedThread(thread);
      setMessages(detailMessages ?? []);
    } catch (err) {
      if (controller.signal.aborted || requestId !== detailRequestRef.current) {
        return;
      }
      setSelectedThread(null);
      setMessages([]);
      setDetailError(
        err instanceof APIError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err),
      );
    } finally {
      if (requestId === detailRequestRef.current) {
        setDetailLoading(false);
      }
    }
  }, [token, namespace, threadID]);

  useEffect(() => {
    void loadDetail();
    return () => {
      detailRequestRef.current += 1;
    };
  }, [loadDetail]);

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages, threadID]);

  const openThread = useCallback(
    (thread: ChatThread) => {
      navigate(`/ns/${encodeURIComponent(thread.namespace)}/chat/${encodeURIComponent(thread.id)}`);
    },
    [navigate],
  );

  const handleCreate = useCallback(
    async (event: FormEvent) => {
      event.preventDefault();
      if (!namespace) {
        return;
      }
      const profileName = newProfile.trim();
      if (!profileName) {
        setListError("Choose an AgentRunProfile for the persona thread.");
        return;
      }
      setCreating(true);
      setListError(null);
      try {
        const thread = await createChatThread(token, namespace, {
          mode: "persona",
          profileName,
          title: newTitle.trim() || undefined,
        });
        setNewTitle("");
        await loadThreads();
        openThread(thread);
      } catch (err) {
        setListError(
          err instanceof APIError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err),
        );
      } finally {
        setCreating(false);
      }
    },
    [token, namespace, newProfile, newTitle, loadThreads, openThread],
  );

  const handleSend = useCallback(
    async (event?: FormEvent) => {
      event?.preventDefault();
      if (!namespace || !threadID) {
        return;
      }
      const content = draft.trim();
      if (!content || sending) {
        return;
      }
      setSending(true);
      setSendError(null);
      try {
        const result = await appendChatMessage(token, namespace, threadID, { content });
        setDraft("");
        setSelectedThread(result.thread);
        setMessages((prev) => [...prev, result.user, result.assistant]);
        setThreads((prev) => {
          const next = prev.filter((item) => item.id !== result.thread.id);
          return [result.thread, ...next];
        });
      } catch (err) {
        setSendError(
          err instanceof APIError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err),
        );
      } finally {
        setSending(false);
      }
    },
    [token, namespace, threadID, draft, sending],
  );

  const onComposerKeyDown = useCallback(
    (event: KeyboardEvent<HTMLTextAreaElement>) => {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        void handleSend();
      }
    },
    [handleSend],
  );

  const headerSubtitle = useMemo(() => {
    if (!namespace) {
      return "Select a namespace";
    }
    if (selectedThread) {
      const profile = selectedThread.profileName ? ` · ${selectedThread.profileName}` : "";
      return `${namespace}${profile} · persona`;
    }
    return `${namespace} · persona threads`;
  }, [namespace, selectedThread]);

  if (!namespace) {
    return (
      <div className="panel">
        <div className="panel-body empty">Select or add a namespace to open standing chat.</div>
      </div>
    );
  }

  return (
    <div className="chat-page">
      <div className="page-header">
        <div>
          <h1 className="page-title">Standing chat</h1>
          <p className="page-sub mono">{headerSubtitle}</p>
        </div>
        <div className="chip-row">
          <span className="chip">ChatGPT-style · AgentRunProfile</span>
          <span className="chip chip-mute">assistant replies may be stubs</span>
        </div>
      </div>

      <div className="banner banner-info">
        Threads persist in PostgreSQL for this namespace. LangGraph tools are not wired yet; the API may
        return echo stubs labeled <span className="mono">metadata.stub=true</span>.
      </div>

      <div className="chat-layout">
        <aside className="chat-sidebar panel">
          <div className="panel-header">
            <div>
              <h2 className="panel-title">Threads</h2>
              <div className="panel-subtitle">{listLoading ? "Loading…" : `${threads.length} shown`}</div>
            </div>
            <button type="button" className="btn btn-ghost" onClick={() => void loadThreads()} disabled={listLoading}>
              Refresh
            </button>
          </div>

          <div className="chat-sidebar-body">
            <label className="field">
              <span className="label">Filter profile</span>
              <select
                className="select"
                value={profileFilter}
                onChange={(event) => setProfileFilter(event.target.value)}
              >
                <option value="">All personas</option>
                {profiles.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>

            <form className="chat-create" onSubmit={handleCreate}>
              <label className="field">
                <span className="label">New thread profile</span>
                {profiles.length > 0 ? (
                  <select
                    className="select"
                    value={newProfile}
                    onChange={(event) => setNewProfile(event.target.value)}
                    required
                  >
                    {profiles.map((name) => (
                      <option key={name} value={name}>
                        {name}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    className="input"
                    value={newProfile}
                    onChange={(event) => setNewProfile(event.target.value)}
                    placeholder="AgentRunProfile name"
                    required
                    spellCheck={false}
                    autoComplete="off"
                  />
                )}
              </label>
              <label className="field">
                <span className="label">Title (optional)</span>
                <input
                  className="input"
                  value={newTitle}
                  onChange={(event) => setNewTitle(event.target.value)}
                  placeholder="New chat"
                  maxLength={200}
                />
              </label>
              <button
                type="submit"
                className="btn btn-primary"
                disabled={creating || !newProfile.trim()}
              >
                {creating ? "Creating…" : "New thread"}
              </button>
            </form>

            {listError ? <div className="banner banner-error">{listError}</div> : null}

            {listLoading && threads.length === 0 ? <div className="empty">Loading threads…</div> : null}

            {!listLoading && threads.length === 0 && !listError ? (
              <div className="empty">No threads yet. Create one with a persona profile.</div>
            ) : null}

            <ul className="chat-thread-list">
              {threads.map((thread) => {
                const active = thread.id === threadID;
                return (
                  <li key={thread.id}>
                    <button
                      type="button"
                      className={`chat-thread-item${active ? " active" : ""}`}
                      onClick={() => openThread(thread)}
                    >
                      <span className="chat-thread-title">{thread.title || "New chat"}</span>
                      <span className="chat-thread-meta mono">
                        {thread.profileName || "—"} · {formatTime(thread.updatedAt)}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        </aside>

        <section className="chat-main panel">
          {!threadID ? (
            <div className="panel-body empty chat-empty-detail">
              Select a thread or create one. Routes:{" "}
              <span className="mono">/chat</span> and{" "}
              <span className="mono">/ns/&lt;namespace&gt;/chat/&lt;threadId&gt;</span>.
            </div>
          ) : (
            <>
              <div className="panel-header">
                <div>
                  <h2 className="panel-title">{selectedThread?.title || "Thread"}</h2>
                  <div className="panel-subtitle mono">
                    {selectedThread?.profileName || "persona"}
                    {selectedThread?.id ? ` · ${selectedThread.id.slice(0, 8)}…` : ""}
                  </div>
                </div>
                <div className="chip-row">
                  <Link className="btn btn-ghost" to="/chat">
                    Close
                  </Link>
                  <button type="button" className="btn btn-ghost" onClick={() => void loadDetail()} disabled={detailLoading}>
                    Reload
                  </button>
                </div>
              </div>

              {detailError ? <div className="banner banner-error">{detailError}</div> : null}
              {detailLoading && messages.length === 0 ? (
                <div className="panel-body empty">Loading messages…</div>
              ) : null}

              <div className="chat-messages" aria-live="polite">
                {!detailLoading && messages.length === 0 && !detailError ? (
                  <div className="empty">No messages yet. Send the first one below.</div>
                ) : null}
                {messages.map((message) => {
                  const stub = isStubMetadata(message.metadata);
                  const engine = stubEngine(message.metadata);
                  return (
                    <article
                      key={messageKey(message)}
                      className={`chat-bubble chat-bubble-${message.role || "unknown"}`}
                    >
                      <header className="chat-bubble-header">
                        <span className="chat-bubble-role">{message.role}</span>
                        <span className="chat-bubble-time mono">{formatTime(message.createdAt)}</span>
                        {stub ? (
                          <span className="chip chip-amber" title="Assistant reply is a storage stub">
                            stub{engine ? ` · ${engine}` : ""}
                          </span>
                        ) : null}
                      </header>
                      <pre className="chat-bubble-body">{message.content}</pre>
                    </article>
                  );
                })}
                <div ref={messagesEndRef} />
              </div>

              <form className="chat-composer" onSubmit={handleSend}>
                {sendError ? <div className="banner banner-error">{sendError}</div> : null}
                <label className="field">
                  <span className="label">Message</span>
                  <textarea
                    className="textarea chat-composer-input"
                    rows={3}
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    onKeyDown={onComposerKeyDown}
                    placeholder="Write a message… (Enter to send, Shift+Enter for newline)"
                    disabled={sending || Boolean(detailError)}
                  />
                </label>
                <div className="chat-composer-actions">
                  <span className="chat-composer-hint">Stored as role=user; API appends an assistant reply.</span>
                  <button type="submit" className="btn btn-primary" disabled={sending || !draft.trim()}>
                    {sending ? "Sending…" : "Send"}
                  </button>
                </div>
              </form>
            </>
          )}
        </section>
      </div>
    </div>
  );
}
