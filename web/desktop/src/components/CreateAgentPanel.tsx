import { useState, type FormEvent } from "react";
import type { ChatChip } from "../api/types.chat";
import {
  CREATE_AGENT_SKILL_DESCRIPTION,
  CREATE_AGENT_TOOL,
  createAgentChips,
  executeCreateAgent,
  formatCreateAgentReceipt,
  isCreateAgentPrincipal,
  requestedCreateChips,
  type CreateAgentRequest,
  type CreateAgentResult,
} from "../wrapper/createAgent";

interface Props {
  token: string;
  namespace: string;
  principal: string;
  writeEnabled: boolean;
  chatEnabled: boolean;
  threadId?: string;
  pendingRequests?: CreateAgentRequest[];
  onCreated?: (result: CreateAgentResult) => void;
}

export function CreateAgentPanel({
  token,
  namespace,
  principal,
  writeEnabled,
  chatEnabled,
  threadId,
  pendingRequests = [],
  onCreated,
}: Props) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [receipt, setReceipt] = useState<string | null>(null);
  const [chips, setChips] = useState<ChatChip[]>([]);
  const allowed = isCreateAgentPrincipal(principal);

  async function onSubmit(event?: FormEvent) {
    event?.preventDefault();
    if (!name.trim() || busy) {
      return;
    }
    setBusy(true);
    try {
      const result = await executeCreateAgent({
        token,
        namespace,
        principal,
        input: { name: name.trim(), description: description.trim(), systemPrompt: systemPrompt.trim() },
        chatEnabled,
        writeEnabled,
        spawnedFromThreadId: threadId,
      });
      setChips(createAgentChips(result));
      setReceipt(formatCreateAgentReceipt(result));
      if (result.ok) {
        setName("");
        setDescription("");
        setSystemPrompt("");
      }
      onCreated?.(result);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel create-agent-panel">
      <div className="panel-header">
        <h2 className="panel-title">{CREATE_AGENT_TOOL}</h2>
        <span className={`chip ${allowed ? "chip-ok" : "chip-fail"}`}>
          {allowed ? `skill · ${principal}` : "peers request"}
        </span>
      </div>
      <div className="panel-body">
        <p className="muted">{CREATE_AGENT_SKILL_DESCRIPTION}</p>
        {pendingRequests.length > 0 ? (
          <div className="chip-row" aria-label="Peer create-agent requests">
            {pendingRequests.flatMap((request) =>
              requestedCreateChips(request).map((chip) => (
                <span key={`${request.name}-${chip.id}`} className={`chip chat-chip chat-chip-${chip.type}`}>
                  {chip.label}
                </span>
              )),
            )}
          </div>
        ) : null}
        {!allowed ? (
          <p className="muted">This principal cannot create teammates. Request create-agent from Wrapper or manager.</p>
        ) : !writeEnabled ? (
          <p className="muted">Composition write is disabled on this API — create-agent cannot POST AgentRunProfiles.</p>
        ) : (
          <form className="create-agent-form" onSubmit={(event) => void onSubmit(event)}>
            <label className="field">
              <span className="label">Name</span>
              <input
                className="input"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="scout"
                disabled={busy}
                aria-label="Agent name"
              />
            </label>
            <label className="field">
              <span className="label">Description</span>
              <input
                className="input"
                value={description}
                onChange={(event) => setDescription(event.target.value)}
                placeholder="Reviews pull requests"
                disabled={busy}
                aria-label="Agent description"
              />
            </label>
            <label className="field">
              <span className="label">System prompt (optional)</span>
              <textarea
                className="textarea"
                rows={2}
                value={systemPrompt}
                onChange={(event) => setSystemPrompt(event.target.value)}
                placeholder="Standing instructions"
                disabled={busy}
                aria-label="System prompt"
              />
            </label>
            <div className="btn-row">
              <button type="submit" className="btn btn-primary" disabled={busy || !name.trim()}>
                {busy ? "Creating…" : "Create agent"}
              </button>
            </div>
          </form>
        )}
        {chips.length > 0 ? (
          <div className="chip-row" aria-label="create-agent receipt">
            {chips.map((chip) => (
              <span key={chip.id} className={`chip chat-chip chat-chip-${chip.type} ${chip.status === "failed" ? "chat-chip-failed" : ""}`}>
                {chip.label}
              </span>
            ))}
          </div>
        ) : null}
        {receipt ? <p className={chips.some((c) => c.status === "failed") ? "banner banner-error" : "muted"}>{receipt}</p> : null}
      </div>
    </section>
  );
}
