import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { APIError } from "../../api/client";
import { listComposition } from "../../api/composition";
import { createAgentRun } from "../../api/runs";
import { emptyCardDraft, type AgentCardDraft } from "../../cards/types";
import { getCard, recordCardRun, upsertCard } from "../../cards/store";

interface Props {
  token: string;
  namespace: string;
  compositionReadEnabled: boolean;
  runsCreateEnabled: boolean;
}

function splitCSV(value: string): string[] {
  return value
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

export function CardEditorPage({
  token,
  namespace: activeNamespace,
  compositionReadEnabled,
  runsCreateEnabled,
}: Props) {
  const { id = "new" } = useParams();
  const navigate = useNavigate();
  const isCreate = id === "new";

  const [draft, setDraft] = useState<AgentCardDraft>(() => emptyCardDraft(activeNamespace));
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState(false);
  const [profiles, setProfiles] = useState<string[]>([]);
  const [harnesses, setHarnesses] = useState<string[]>([]);
  const [skillSets, setSkillSets] = useState<string[]>([]);
  const [toolSets, setToolSets] = useState<string[]>([]);

  useEffect(() => {
    if (isCreate) {
      setDraft(emptyCardDraft(activeNamespace));
      return;
    }
    const existing = getCard(id);
    if (!existing) {
      setError("Card not found in this browser");
      return;
    }
    setDraft({ ...existing });
  }, [id, isCreate, activeNamespace]);

  useEffect(() => {
    if (!compositionReadEnabled || !token || !draft.namespace) {
      return;
    }
    const controller = new AbortController();
    void Promise.all([
      listComposition(token, draft.namespace, "agent-run-profiles", 200, controller.signal),
      listComposition(token, draft.namespace, "agent-harness-profiles", 200, controller.signal),
      listComposition(token, draft.namespace, "agent-skill-sets", 200, controller.signal),
      listComposition(token, draft.namespace, "agent-tool-sets", 200, controller.signal),
    ])
      .then(([p, h, s, t]) => {
        setProfiles(p.map((item) => item.metadata.name));
        setHarnesses(h.map((item) => item.metadata.name));
        setSkillSets(s.map((item) => item.metadata.name));
        setToolSets(t.map((item) => item.metadata.name));
      })
      .catch(() => {
        // Pickers stay free-text when composition read fails.
      });
    return () => controller.abort();
  }, [compositionReadEnabled, token, draft.namespace]);

  const skillCSV = useMemo(() => draft.skillSetNames.join(", "), [draft.skillSetNames]);
  const toolCSV = useMemo(() => draft.toolSetNames.join(", "), [draft.toolSetNames]);
  const tagsCSV = useMemo(() => draft.tags.join(", "), [draft.tags]);

  function update<K extends keyof AgentCardDraft>(key: K, value: AgentCardDraft[K]) {
    setDraft((prev) => ({ ...prev, [key]: value }));
  }

  function onSave() {
    setError(null);
    setInfo(null);
    setSaving(true);
    try {
      const saved = upsertCard(draft);
      setInfo("Card saved in this browser");
      if (isCreate) {
        navigate(`/cards/${encodeURIComponent(saved.id)}`, { replace: true });
      } else {
        setDraft({ ...saved });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function onRun() {
    setError(null);
    setInfo(null);
    if (!runsCreateEnabled) {
      setError("Run create is disabled on this API (enable runs.createEnabled + anvil-agents:runs:create).");
      return;
    }
    let saved;
    try {
      saved = upsertCard(draft);
      setDraft({ ...saved });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return;
    }
    setRunning(true);
    try {
      const run = await createAgentRun(token, saved.namespace, {
        generateName: `card-${slugify(saved.title)}-`,
        prompt: saved.prompt,
        profileName: saved.profileName,
        harnessProfileName: saved.harnessProfileName,
        skillSetNames: saved.skillSetNames,
        toolSetNames: saved.toolSetNames,
        intent: saved.intent || undefined,
        purpose: saved.purpose || "manual",
        sourceKind: "ConsoleCard",
        sourceName: saved.id,
      });
      recordCardRun(saved.id, run.name);
      setInfo(`Started run ${run.name}`);
      navigate(`/ns/${encodeURIComponent(run.namespace)}/runs/${encodeURIComponent(run.name)}`);
    } catch (err) {
      setError(err instanceof APIError ? err.message : String(err));
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="card-editor">
      <div className="page-header">
        <div>
          <div className="breadcrumb">
            <Link to="/cards">Cards</Link>
            <span>/</span>
            <span>{isCreate ? "new" : draft.title || id}</span>
          </div>
          <h1 className="page-title">{isCreate ? "New agent card" : draft.title || "Edit card"}</h1>
          <p className="page-sub">Frontend recipe · not a cluster CR</p>
        </div>
        <div className="chip-row">
          <button type="button" className="btn" disabled={saving || running} onClick={onSave}>
            {saving ? "Saving…" : "Save card"}
          </button>
          <button
            type="button"
            className="btn btn-primary"
            disabled={saving || running}
            onClick={() => void onRun()}
          >
            {running ? "Starting…" : "Save & run"}
          </button>
        </div>
      </div>

      {error ? <div className="banner banner-error">{error}</div> : null}
      {info ? <div className="banner banner-ok">{info}</div> : null}

      <div className="panel">
        <div className="panel-header">
          <h2 className="panel-title">Card identity</h2>
        </div>
        <div className="panel-body editor-form">
          <label className="field">
            <span className="label">Title</span>
            <input
              className="input"
              value={draft.title}
              onChange={(event) => update("title", event.target.value)}
              placeholder="Production auditor (quick)"
            />
          </label>
          <label className="field">
            <span className="label">Description</span>
            <textarea
              className="textarea"
              value={draft.description}
              onChange={(event) => update("description", event.target.value)}
              placeholder="What this card is for"
            />
          </label>
          <label className="field">
            <span className="label">Namespace</span>
            <input
              className="input mono"
              value={draft.namespace}
              onChange={(event) => update("namespace", event.target.value)}
            />
          </label>
          <label className="field">
            <span className="label">Tags (comma-separated)</span>
            <input
              className="input"
              value={tagsCSV}
              onChange={(event) => update("tags", splitCSV(event.target.value))}
              placeholder="audit, prod, grok"
            />
          </label>
        </div>
      </div>

      <div className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">Composition assembly</h2>
        </div>
        <div className="panel-body editor-form">
          <label className="field">
            <span className="label">Profile (required)</span>
            <input
              className="input mono"
              list="card-profiles"
              value={draft.profileName}
              onChange={(event) => update("profileName", event.target.value)}
              placeholder="hazy-trade-production-auditor"
            />
            <datalist id="card-profiles">
              {profiles.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          </label>
          <label className="field">
            <span className="label">Harness profile (optional override)</span>
            <input
              className="input mono"
              list="card-harnesses"
              value={draft.harnessProfileName ?? ""}
              onChange={(event) => update("harnessProfileName", event.target.value)}
              placeholder="leave empty to use profile default"
            />
            <datalist id="card-harnesses">
              {harnesses.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          </label>
          <label className="field">
            <span className="label">Skill sets (comma-separated, ordered)</span>
            <input
              className="input mono"
              list="card-skills"
              value={skillCSV}
              onChange={(event) => update("skillSetNames", splitCSV(event.target.value))}
              placeholder="repo-review, org-knowledge"
            />
            <datalist id="card-skills">
              {skillSets.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          </label>
          <label className="field">
            <span className="label">Tool sets (comma-separated, ordered)</span>
            <input
              className="input mono"
              list="card-tools"
              value={toolCSV}
              onChange={(event) => update("toolSetNames", splitCSV(event.target.value))}
              placeholder="knowledge-http"
            />
            <datalist id="card-tools">
              {toolSets.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          </label>
          <div className="field-row">
            <label className="field">
              <span className="label">Intent</span>
              <select
                className="select"
                value={draft.intent ?? ""}
                onChange={(event) =>
                  update("intent", event.target.value as AgentCardDraft["intent"])
                }
              >
                <option value="">(profile default)</option>
                <option value="observe">observe</option>
                <option value="fixTransient">fixTransient</option>
                <option value="proposeChange">proposeChange</option>
                <option value="cleanup">cleanup</option>
              </select>
            </label>
            <label className="field">
              <span className="label">Purpose</span>
              <select
                className="select"
                value={draft.purpose ?? "manual"}
                onChange={(event) =>
                  update("purpose", event.target.value as AgentCardDraft["purpose"])
                }
              >
                <option value="manual">manual</option>
                <option value="adverseSituation">adverseSituation</option>
                <option value="scheduledHealthCheck">scheduledHealthCheck</option>
              </select>
            </label>
          </div>
          {!compositionReadEnabled ? (
            <div className="banner banner-warn" style={{ marginBottom: 0 }}>
              Composition library read is disabled — type resource names manually. Enable
              composition.readEnabled to get pickers from the cluster.
            </div>
          ) : null}
        </div>
      </div>

      <div className="panel" style={{ marginTop: "0.75rem" }}>
        <div className="panel-header">
          <h2 className="panel-title">Prompt</h2>
        </div>
        <div className="panel-body">
          <textarea
            className="textarea textarea-spec"
            value={draft.prompt}
            onChange={(event) => update("prompt", event.target.value)}
            placeholder="What should this run do now?"
          />
        </div>
      </div>
    </div>
  );
}

function slugify(value: string): string {
  const slug = value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
  return slug || "agent";
}
