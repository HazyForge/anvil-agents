import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  connectAnvilCouncil,
  getAnvilCouncil,
  turnAnvilCouncil,
  type CouncilMember,
  type CouncilMessage,
  type CouncilState,
} from "../api/council";
import { APIError } from "../api/client";
import type { Snapshot } from "../api/types";
import type { UIConfig } from "../auth/config";
import { loadNamespace, saveNamespace } from "../state/namespace";

interface Props {
  snapshot: Snapshot;
  token: string;
  config: UIConfig;
}

const SAMPLE_ANVIL_PROMPT =
  "Map agents-quickstart, then implement the mixed-harness proof. Do not let two members inventory the same namespace.";
const SAMPLE_MEMBER_PROMPT = "Tell Anvil you have the inventory claim, and tell Implementer to wait on your map.";

export function AnvilCouncilPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents-quickstart";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [to, setTo] = useState("anvil-agent");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [state, setState] = useState<CouncilState | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);

  const chatEnabled = Boolean(config.chat?.enabled);
  const writeEnabled = Boolean(config.composition.writeEnabled);
  const runsEnabled = Boolean(config.runs.createEnabled);
  const members = state?.members?.length ? state.members : defaultMembers();
  const addresseeName = memberLabel(to, "");

  function setNs(next: string) {
    setNamespace(next);
    saveNamespace(next);
  }

  const refresh = useCallback(async () => {
    if (!namespace || !chatEnabled) {
      return;
    }
    try {
      setState(await getAnvilCouncil(token, namespace));
      setError(null);
    } catch (err) {
      if (err instanceof APIError && (err.status === 404 || err.code === "chat_disabled")) {
        setState(null);
        return;
      }
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [chatEnabled, namespace, token]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [state?.messages, busy]);

  async function onConnect() {
    setBusy(true);
    setError(null);
    try {
      setState(await connectAnvilCouncil(token, namespace));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function runTurn(text: string, addressee = to) {
    const content = text.trim();
    if (!content || busy) {
      return;
    }
    setBusy(true);
    setError(null);
    setDraft("");
    const optimistic: CouncilMessage = {
      id: `local-${Date.now()}`,
      role: "user",
      content,
      createdAt: new Date().toISOString(),
      authorKind: "human",
      displayName: "You",
      addressedTo: addressee,
      kind: "utterance",
    };
    setState((current) =>
      current ? { ...current, messages: [...current.messages, optimistic] } : current,
    );
    try {
      if (!state?.connected) {
        await connectAnvilCouncil(token, namespace);
      }
      setState(await turnAnvilCouncil(token, namespace, content, addressee));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      try {
        setState(await getAnvilCouncil(token, namespace));
      } catch {
        // keep optimistic thread if refresh also fails
      }
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit(event?: FormEvent) {
    event?.preventDefault();
    await runTurn(draft, to);
  }

  function onComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void onSubmit();
    }
  }

  const messages = state?.messages ?? [];
  const runs = state?.delegatedRuns ?? [];
  const talkingToAnvil = to === "anvil-agent";

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Anvil council</h1>
          <p className="page-sub">
            Message <span className="mono">Anvil agent</span> and its LLM harness decides who confers and what to
            delegate. You can also talk to a member; they reply as themselves and may message Anvil or peers.
          </p>
        </div>
        <div className="chip-row">
          <span className={`chip ${state?.connected ? "chip-ok" : ""}`}>{state?.connected ? "connected" : "idle"}</span>
          <span className={`chip ${chatEnabled ? "chip-ok" : ""}`}>{chatEnabled ? "chat api" : "chat off"}</span>
          <span className={`chip ${runsEnabled ? "chip-ok" : ""}`}>{runsEnabled ? "runs create" : "runs off"}</span>
        </div>
      </div>

      <section className="panel" style={{ marginBottom: "0.75rem" }}>
        <div className="panel-body" style={{ flexDirection: "row", flexWrap: "wrap", alignItems: "end" }}>
          <label className="field" style={{ minWidth: "12rem", flex: 1 }}>
            <span className="label">Namespace</span>
            <input
              className="input"
              value={namespace}
              onChange={(event) => setNs(event.target.value)}
              placeholder={fallbackNs}
            />
          </label>
          <button type="button" className="btn btn-primary" disabled={busy || !writeEnabled} onClick={() => void onConnect()}>
            {busy ? "Working…" : "Connect council"}
          </button>
        </div>
      </section>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {!writeEnabled || !runsEnabled || !chatEnabled ? (
        <div className="banner banner-info">
          Kind API must enable chat, composition write, and append-only AgentRun create before the council can prove
          itself.
        </div>
      ) : null}

      <div className="council-room">
        <section className="thread-shell" aria-label="Council room">
          <header className="thread-header">
            <span className="thread-avatar" aria-hidden="true" />
            <div>
              <h2 className="thread-title">{addresseeName}</h2>
              <p className="thread-sub">
                {talkingToAnvil
                  ? "Controller · send a message and the council-llm harness decides conferral and delegation"
                  : `Member · a real conversation with ${addresseeName}, who can message Anvil or peers`}
              </p>
            </div>
          </header>
          <div className="thread-messages" aria-live="polite">
            {messages.length === 0 ? (
              <div className="empty">
                Connect, then send to Anvil agent or pick a member. Conferral is not a button — it comes from the
                harness.
              </div>
            ) : null}
            {messages.map((message, index) => {
              const name = displayName(message);
              const outgoing = message.role === "user" || message.authorKind === "human";
              const showFrom =
                !outgoing &&
                (index === 0 ||
                  displayName(messages[index - 1]) !== name ||
                  messages[index - 1].addressedTo !== message.addressedTo ||
                  messages[index - 1].role === "user");
              return (
                <div key={message.id} className={outgoing ? "thread-row thread-row-out" : "thread-row"}>
                  {outgoing ? (
                    <p className="thread-from">You → {memberLabel(message.addressedTo || "anvil-agent", "")}</p>
                  ) : showFrom ? (
                    <p className="thread-from">
                      Message from {name}
                      {message.addressedTo ? ` → ${addressLabel(message.addressedTo)}` : ""}
                      {message.waitingOn ? ` · waiting on ${waitingLabel(message.waitingOn)}` : ""}
                    </p>
                  ) : null}
                  <article className={`thread-bubble ${bubbleClass(message)}`}>
                    {message.kind === "interrupt" ? <span className="chip chip-warn">interrupt</span> : null}
                    <pre className="chat-bubble-body">{message.content}</pre>
                  </article>
                </div>
              );
            })}
            {busy ? (
              <p className="thread-thinking">
                {addresseeName} is running its harness…
              </p>
            ) : null}
            <div ref={endRef} />
          </div>
          <form className="thread-composer" onSubmit={(event) => void onSubmit(event)}>
            <div className="recipient-row" role="group" aria-label="Message recipient">
              {members.map((member) => (
                <button
                  key={member.profileName}
                  type="button"
                  className={`recipient-chip ${to === member.profileName ? "recipient-chip-active" : ""}`}
                  disabled={busy}
                  onClick={() => setTo(member.profileName)}
                >
                  {memberLabel(member.profileName, member.role)}
                </button>
              ))}
            </div>
            <label className="field">
              <span className="label">{talkingToAnvil ? `Ask ${addresseeName}` : `Message ${addresseeName}`}</span>
              <textarea
                className="textarea chat-composer-input"
                rows={3}
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={onComposerKeyDown}
                placeholder={talkingToAnvil ? SAMPLE_ANVIL_PROMPT : SAMPLE_MEMBER_PROMPT}
                disabled={busy || !writeEnabled}
              />
            </label>
            <div className="chat-composer-actions">
              <button
                type="button"
                className="btn btn-ghost btn-optional"
                disabled={busy || !writeEnabled}
                onClick={() => setDraft(talkingToAnvil ? SAMPLE_ANVIL_PROMPT : SAMPLE_MEMBER_PROMPT)}
              >
                Sample prompt
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim() || !writeEnabled}>
                {busy ? "Working…" : "Send"}
              </button>
            </div>
          </form>
        </section>

        <aside className="thread-rail">
          <div className="panel">
            <div className="panel-header">
              <h2 className="panel-title">In the room</h2>
              <span className="muted">{members.length}</span>
            </div>
            <div className="panel-body">
              <ul className="entity-list">
                {members.map((member) => (
                  <li key={member.profileName} className="entity-item">
                    <span>{memberLabel(member.profileName, member.role)}</span>
                    <span className="muted">
                      {member.role}
                      {member.harness ? ` · ${member.harness}` : ""}
                      {member.backend ? ` · ${member.backend}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
          <div className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Knowledge</h2>
            </div>
            <div className="panel-body">
              {(state?.knowledge ?? []).length === 0 ? (
                <p className="muted">Auto-attached when the council connects.</p>
              ) : (
                <ul className="entity-list">
                  {(state?.knowledge ?? []).map((entry) => (
                    <li key={entry.id} className="entity-item">
                      <span>{entry.title}</span>
                      <span className="muted">{entry.body}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
          <div className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Shared memory</h2>
            </div>
            <div className="panel-body">
              {(state?.memory ?? []).length === 0 ? (
                <p className="muted">Unified memory is empty until connect/turn.</p>
              ) : (
                <ul className="entity-list">
                  {(state?.memory ?? []).map((entry) => (
                    <li key={entry.id} className="entity-item">
                      <span className="mono">{entry.key}</span>
                      <span>{entry.value}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
          <div className="panel">
            <div className="panel-header">
              <h2 className="panel-title">Runs</h2>
              <span className="muted">{runs.length}</span>
            </div>
            <div className="panel-body">
              {runs.length === 0 ? (
                <p className="muted">Send to Anvil agent. Its harness starts a conversation run and may delegate work on mixed harnesses.</p>
              ) : (
                <ul className="entity-list">
                  {runs.map((run) => (
                    <li key={run.name} className="entity-item run-card">
                      <span className="mono">{run.name}</span>
                      <span>
                        {run.kind ? `${run.kind} · ` : ""}
                        {run.role} · {run.profileName}
                      </span>
                      <span className="muted">
                        harness {run.harnessProfileName} · backend {run.backend}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function defaultMembers(): CouncilMember[] {
  return [
    { role: "controller", profileName: "anvil-agent" },
    { role: "researcher", profileName: "council-researcher" },
    { role: "implementer", profileName: "council-implementer" },
  ];
}

function displayName(message: CouncilMessage): string {
  if (message.displayName) {
    return message.displayName;
  }
  if (message.role === "user" || message.authorKind === "human") {
    return "You";
  }
  return memberLabel(message.authorProfile || "", message.authorRole || message.role);
}

function memberLabel(profile: string, role: string): string {
  switch (profile) {
    case "anvil-agent":
      return "Anvil agent";
    case "council-researcher":
      return "Council Researcher";
    case "council-implementer":
      return "Council Implementer";
    case "user":
      return "You";
    default:
      return profile || role || "member";
  }
}

function addressLabel(profile: string): string {
  return memberLabel(profile, "");
}

function waitingLabel(profile: string): string {
  return memberLabel(profile, "");
}

function bubbleClass(message: CouncilMessage): string {
  if (message.kind === "interrupt") {
    return "thread-bubble-interrupt";
  }
  if (message.role === "user" || message.authorKind === "human") {
    return "thread-bubble-user";
  }
  if (message.authorProfile === "anvil-agent" || message.authorRole === "controller") {
    return "thread-bubble-anvil";
  }
  return "thread-bubble-member";
}
