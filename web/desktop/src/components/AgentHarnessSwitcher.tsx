import { useEffect, useState } from "react";
import {
  APIError,
  backendKindFromComposition,
  harnessModelFromComposition,
  harnessOptionLabel,
  harnessRefFromRunProfile,
  patchRunProfileHarness,
  type CompositionDocument,
} from "../api/client";

interface Props {
  token: string;
  namespace: string;
  profileName: string;
  profile?: CompositionDocument;
  harnesses: CompositionDocument[];
  writeEnabled: boolean;
  disabled?: boolean;
  /** Thread-level harness pin for the open conversation, if any. */
  threadHarness?: string;
  onUpdated?: (doc: CompositionDocument) => void;
}

function managementNote(profile?: CompositionDocument): string | null {
  if (!profile) return null;
  const management = profile.management;
  if (management?.writable) return null;
  if (management?.reason === "gitops_protected") {
    return `GitOps source of truth${management.managedBy ? ` (${management.managedBy})` : ""} — edit the Git repository instead of the live object.`;
  }
  return "Read-only — only console-managed agents can switch harnesses here.";
}

export function AgentHarnessSwitcher({
  token,
  namespace,
  profileName,
  profile,
  harnesses,
  writeEnabled,
  disabled,
  threadHarness,
  onUpdated,
}: Props) {
  const current = harnessRefFromRunProfile(profile);
  const [selected, setSelected] = useState(current);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");

  useEffect(() => {
    setSelected(current);
    setSaved("");
    setError("");
  }, [current, profileName, namespace]);

  const currentDoc = harnesses.find((h) => h.metadata.name === current);
  const currentBackend = backendKindFromComposition(currentDoc) || backendKindFromComposition(profile);
  const currentModel = harnessModelFromComposition(currentDoc);
  const writable = Boolean(profile?.management?.writable);
  const blocked = managementNote(profile);
  const unchanged = selected === current;
  const canSave =
    Boolean(profileName) && Boolean(profile) && writeEnabled && writable && !unchanged && !saving && !disabled;

  async function onSave() {
    if (!canSave || !profile) return;
    setSaving(true);
    setError("");
    setSaved("");
    try {
      const updated = await patchRunProfileHarness(token, namespace, profileName, selected);
      setSaved(
        selected
          ? `Agent migrated to ${selected}. The next chat turn uses it with a fresh home on the new harness.`
          : "Harness binding cleared — the agent falls back to its inline harness.",
      );
      onUpdated?.(updated);
    } catch (err) {
      setError(err instanceof APIError ? err.message : err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  if (!profileName) return null;

  return (
    <div className="agent-harness-switcher">
      <div className="field">
        <span className="label">Cluster harness</span>
        <p className="remote-chat-caption">
          {current ? (
            <>
              Current: <strong>{current}</strong>
              {currentBackend ? ` (${currentBackend}${currentModel ? ` · ${currentModel}` : ""})` : ""}
            </>
          ) : (
            "No harness profile selected — the agent uses its inline harness."
          )}
        </p>
        <select
          className="input"
          aria-label="Cluster harness"
          value={selected}
          disabled={saving || disabled || !harnesses.length}
          onChange={(e) => {
            setSelected(e.target.value);
            setSaved("");
            setError("");
          }}
        >
          <option value="">Agent default (inline harness)</option>
          {harnesses.map((h) => (
            <option key={h.metadata.name} value={h.metadata.name}>
              {harnessOptionLabel(h)}
            </option>
          ))}
        </select>
      </div>
      {!writeEnabled ? (
        <p className="remote-chat-caption">Composition write is disabled on this API — harness migration is unavailable.</p>
      ) : blocked ? (
        <p className="remote-chat-caption">{blocked}</p>
      ) : null}
      {threadHarness ? (
        <p className="remote-chat-caption">
          This conversation pins <strong>{threadHarness}</strong>. Migrating below rebinds the agent
          default used by standing conversations and new chats.
        </p>
      ) : (
        <p className="remote-chat-caption">
          Migrates <code>{profileName}</code> to a new harness profile binding without deleting it.
          Skills, tools, and scope are preserved; the next chat turn resolves the new harness.
          Harness-local durable homes and internal harness memory (Codex home, Grok home, Pi home, …)
          are not migrated — the agent starts with a fresh home on the target harness.
        </p>
      )}
      {error ? (
        <div className="banner banner-error" role="alert">
          {error}
        </div>
      ) : null}
      {saved ? <p className="remote-chat-caption">{saved}</p> : null}
      <div className="btn-row">
        <button type="button" className="btn btn-primary" disabled={!canSave} onClick={() => void onSave()}>
          {saving ? "Migrating…" : "Migrate harness"}
        </button>
      </div>
    </div>
  );
}
