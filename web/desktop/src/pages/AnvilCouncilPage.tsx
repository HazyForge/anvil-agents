import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  connectAnvilCouncil,
  getAnvilCouncil,
  turnAnvilCouncil,
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

const DEMO_PROMPT =
  "Both of you start by inventorying the same namespace, then implement the mixed-harness proof in parallel.";

export function AnvilCouncilPage({ token, config }: Props) {
  const fallbackNs = config.defaultNamespaces[0] || "agents-quickstart";
  const [namespace, setNamespace] = useState(() => loadNamespace(fallbackNs));
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [state, setState] = useState<CouncilState | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);

  const chatEnabled = Boolean(config.chat?.enabled);
  const writeEnabled = Boolean(config.composition.writeEnabled);
  const runsEnabled = Boolean(config.runs.createEnabled);

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
  }, [state?.messages]);

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

  async function runTurn(text: string) {
    const content = text.trim();
    if (!content || busy) {
      return;
    }
    setBusy(true);
    setError(null);
    setDraft("");
    try {
      if (!state?.connected) {
        await connectAnvilCouncil(token, namespace);
      }
      setState(await turnAnvilCouncil(token, namespace, content));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
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

  const messages = state?.messages ?? [];
  const interrupts = state?.interrupts ?? [];
  const runs = state?.delegatedRuns ?? [];

  return (
    <div>
      <div className="page-header">
        <div>
          <h1 className="page-title">Anvil council</h1>
          <p className="page-sub">
            <span className="mono">anvil-agent</span> is in charge. Members share one knowledge base and unified
            memory, confer as distinct voices, interrupt duplicate work, and delegate in parallel on mixed harnesses.
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

      <div className="council-layout">
        <aside className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Roster</h2>
            <span className="muted">{state?.members.length ?? 0}</span>
          </div>
          <div className="panel-body">
            {(state?.members ?? []).length === 0 ? (
              <p className="muted">Connect to attach Anvil agent, researcher, and implementer.</p>
            ) : (
              <ul className="entity-list">
                {(state?.members ?? []).map((member) => (
                  <li key={member.profileName} className="entity-item">
                    <span className="mono">{member.profileName}</span>
                    <span className="muted">
                      {member.role}
                      {member.harness ? ` · ${member.harness}` : ""}
                      {member.backend ? ` · ${member.backend}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
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
                    <span className="mono">{entry.title}</span>
                    <span>{entry.body}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
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
        </aside>

        <section className="panel entity-chat-main">
          <div className="panel-header">
            <h2 className="panel-title">Anvil agent</h2>
            <span className="chip mono">{state?.controller || "anvil-agent"}</span>
          </div>
          <div className="chat-messages" aria-live="polite">
            {messages.length === 0 ? (
              <div className="empty">Connect, then send the demo prompt so members confer and delegate.</div>
            ) : null}
            {messages.map((message) => (
              <article key={message.id} className={`chat-bubble ${bubbleClass(message)}`}>
                <header className="chat-bubble-header">
                  <span className="chat-bubble-role">{message.authorProfile || message.authorKind || message.role}</span>
                  {message.kind === "interrupt" ? <span className="chip chip-warn">interrupt</span> : null}
                </header>
                <pre className="chat-bubble-body">{message.content}</pre>
              </article>
            ))}
            <div ref={endRef} />
          </div>
          <form className="chat-composer" onSubmit={(event) => void onSubmit(event)}>
            <label className="field">
              <span className="label">Message to Anvil agent</span>
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
            <div className="chat-composer-actions">
              <button type="button" className="btn" disabled={busy || !writeEnabled} onClick={() => void runTurn(DEMO_PROMPT)}>
                Confer and delegate
              </button>
              <button type="submit" className="btn btn-primary" disabled={busy || !draft.trim() || !writeEnabled}>
                {busy ? "Working…" : "Send"}
              </button>
            </div>
          </form>
        </section>

        <aside className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Interrupts</h2>
            <span className="muted">{interrupts.length}</span>
          </div>
          <div className="panel-body">
            {interrupts.length === 0 ? (
              <p className="muted">If two members claim the same work, Anvil agent interrupts the duplicate.</p>
            ) : (
              <ul className="entity-list">
                {interrupts.map((line) => (
                  <li key={line.id} className="entity-item">
                    <span className="chip chip-warn">interrupt</span>
                    <span className="mono">{line.authorProfile}</span>
                    <span>{line.content}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="panel-header">
            <h2 className="panel-title">Parallel runs</h2>
            <span className="muted">{runs.length}</span>
          </div>
          <div className="panel-body">
            {runs.length === 0 ? (
              <p className="muted">A turn creates two append-only AgentRuns on different harnesses.</p>
            ) : (
              <ul className="entity-list">
                {runs.map((run) => (
                  <li key={run.name} className="entity-item run-card">
                    <span className="mono">{run.name}</span>
                    <span>
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
        </aside>
      </div>
    </div>
  );
}

function bubbleClass(message: CouncilMessage): string {
  if (message.kind === "interrupt") {
    return "chat-bubble-interrupt";
  }
  if (message.authorProfile === "anvil-agent" || message.authorRole === "controller") {
    return "chat-bubble-wrapper";
  }
  if (message.role === "user") {
    return "chat-bubble-user";
  }
  return "chat-bubble-entity";
}
