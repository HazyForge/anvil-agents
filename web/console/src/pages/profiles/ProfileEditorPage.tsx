import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { APIError } from "../../api/client";
import {
  createComposition,
  deleteComposition,
  getComposition,
  listComposition,
  updateComposition,
} from "../../api/composition";
import type { CompositionDocument } from "../../api/types.composition";
import {
  CompositionCardPicker,
  optionsFromDocs,
} from "../../components/CompositionCardPicker";
import { CapabilitySelectionPicker, type CapabilitySelection } from "../../components/CapabilitySelectionPicker";
import { IconPicker } from "../../components/IconPicker";
import {
  getIconUrl,
  getScreenshotUrl,
  mergePresentationAnnotations,
} from "../../utils/icons";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

function arrayLen(spec: Record<string, unknown>, key: string): number {
  const value = spec[key];
  return Array.isArray(value) ? value.length : 0;
}

function harnessMeta(doc: CompositionDocument): string | undefined {
  const backend = doc.spec?.backend;
  if (typeof backend === "object" && backend && "kind" in backend) {
    const kind = String((backend as { kind?: string }).kind ?? "").trim();
    return kind ? `backend: ${kind}` : undefined;
  }
  return undefined;
}

function skillSetMeta(doc: CompositionDocument): string | undefined {
  const skills = arrayLen(doc.spec ?? {}, "skills");
  const tools = arrayLen(doc.spec ?? {}, "tools");
  const bits = [
    skills ? `${skills} skill${skills === 1 ? "" : "s"}` : null,
    tools ? `${tools} embedded tool${tools === 1 ? "" : "s"}` : null,
  ].filter(Boolean);
  return bits.length ? bits.join(" · ") : undefined;
}

function toolSetMeta(doc: CompositionDocument): string | undefined {
  const tools = arrayLen(doc.spec ?? {}, "tools");
  return tools ? `${tools} tool${tools === 1 ? "" : "s"}` : undefined;
}

interface ProfileForm {
  name: string;
  description: string;
  icon: string;
  screenshot: string;
  harnessProfileName: string;
  skillSetNames: string[];
  toolSetNames: string[];
  skillMode: string;
  skillSelections: CapabilitySelection[];
  skillOverrides: unknown[];
  toolMode: string;
  toolSelections: CapabilitySelection[];
  mcpMode: string;
  mcpSelections: CapabilitySelection[];
  intent: string;
  systemPrompt: string;
  applicationName: string;
}

function emptyForm(): ProfileForm {
  return {
    name: "",
    description: "",
    icon: "",
    screenshot: "",
    harnessProfileName: "",
    skillSetNames: [],
    toolSetNames: [],
    skillMode: "",
    skillSelections: [],
    skillOverrides: [],
    toolMode: "",
    toolSelections: [],
    mcpMode: "",
    mcpSelections: [],
    intent: "",
    systemPrompt: "",
    applicationName: "",
  };
}

function uniqueNames(names: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of names) {
    const name = raw.trim();
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    out.push(name);
  }
  return out;
}

function formFromDoc(doc: CompositionDocument): ProfileForm {
  const spec = doc.spec ?? {};
  const harness =
    typeof spec.harness === "object" && spec.harness
      ? (spec.harness as { intent?: string; systemPrompt?: string })
      : {};
  const harnessRef =
    typeof spec.harnessProfileRef === "object" && spec.harnessProfileRef
      ? String((spec.harnessProfileRef as { name?: string }).name ?? "")
      : "";
  const skillRefs =
    typeof spec.skillSets === "object" && spec.skillSets
      ? ((spec.skillSets as { refs?: { name?: string }[] }).refs ?? [])
          .map((ref) => String(ref.name ?? "").trim())
          .filter(Boolean)
      : [];
  const toolRefs =
    typeof spec.toolSets === "object" && spec.toolSets
      ? ((spec.toolSets as { refs?: { name?: string }[] }).refs ?? [])
          .map((ref) => String(ref.name ?? "").trim())
          .filter(Boolean)
      : [];
  const scope =
    typeof spec.scope === "object" && spec.scope
      ? (spec.scope as { applicationRef?: { name?: string } })
      : {};
  const capabilities = typeof spec.capabilities === "object" && spec.capabilities ? spec.capabilities as Record<string, unknown> : {};
  const skills = typeof capabilities.skills === "object" && capabilities.skills ? capabilities.skills as Record<string, unknown> : {};
  const tools = typeof capabilities.tools === "object" && capabilities.tools ? capabilities.tools as Record<string, unknown> : {};
  const mcps = typeof capabilities.mcpServers === "object" && capabilities.mcpServers ? capabilities.mcpServers as Record<string, unknown> : {};
  const selections = (value: unknown, atomicKey: string, setKey: string): CapabilitySelection[] => {
    const result: CapabilitySelection[] = [];
    if (!Array.isArray(value)) return result;
    for (const raw of value) {
      const item = typeof raw === "object" && raw ? raw as Record<string, unknown> : {};
      const atomic = typeof item[atomicKey] === "object" && item[atomicKey] ? String((item[atomicKey] as {name?: string}).name ?? "") : "";
      const set = typeof item[setKey] === "object" && item[setKey] ? String((item[setKey] as {name?: string}).name ?? "") : "";
      if (atomic) result.push({ type: "atomic", name: atomic });
      else if (set) result.push({ type: "set", name: set });
    }
    return result;
  };
  return {
    name: doc.metadata.name,
    description: String(spec.description ?? ""),
    icon: getIconUrl(doc.metadata.annotations),
    screenshot: getScreenshotUrl(doc.metadata.annotations),
    harnessProfileName: harnessRef,
    skillSetNames: uniqueNames(skillRefs),
    toolSetNames: uniqueNames(toolRefs),
    skillMode: String(skills.mode ?? ""),
    skillSelections: selections(skills.selections, "skillRef", "skillSetRef"),
    skillOverrides: Array.isArray(skills.overrides) ? skills.overrides : [],
    toolMode: String(tools.mode ?? ""),
    toolSelections: selections(tools.selections, "toolRef", "toolSetRef"),
    mcpMode: String(mcps.mode ?? ""),
    mcpSelections: selections(mcps.selections, "serverRef", "mcpSetRef"),
    intent: String(harness.intent ?? ""),
    systemPrompt: String(harness.systemPrompt ?? ""),
    applicationName: String(scope.applicationRef?.name ?? ""),
  };
}

function buildSpec(form: ProfileForm): Record<string, unknown> {
  const skillNames = uniqueNames(form.skillSetNames);
  const toolNames = uniqueNames(form.toolSetNames);
  const harness: Record<string, unknown> = {};
  if (form.intent) {
    harness.intent = form.intent;
  }
  if (form.systemPrompt.trim()) {
    harness.systemPrompt = form.systemPrompt;
  }
  const spec: Record<string, unknown> = {};
  if (form.description.trim()) {
    spec.description = form.description.trim();
  }
  if (form.harnessProfileName.trim()) {
    spec.harnessProfileRef = { name: form.harnessProfileName.trim() };
  }
  if (skillNames.length > 0) {
    spec.skillSets = {
      refs: skillNames.map((name) => ({ name })),
    };
  }
  if (toolNames.length > 0) {
    spec.toolSets = {
      refs: toolNames.map((name) => ({ name })),
    };
  }
  const capabilities: Record<string, unknown> = {};
  const buildSelections = (items: CapabilitySelection[], atomicKey: string, setKey: string) => items.map((item) => ({ [item.type === "atomic" ? atomicKey : setKey]: { name: item.name } }));
  if (form.skillMode || form.skillSelections.length || form.skillOverrides.length) capabilities.skills = { ...(form.skillMode ? { mode: form.skillMode } : {}), ...(form.skillSelections.length ? { selections: buildSelections(form.skillSelections, "skillRef", "skillSetRef") } : {}), ...(form.skillOverrides.length ? { overrides: form.skillOverrides } : {}) };
  if (form.toolMode || form.toolSelections.length) capabilities.tools = { ...(form.toolMode ? { mode: form.toolMode } : {}), ...(form.toolSelections.length ? { selections: buildSelections(form.toolSelections, "toolRef", "toolSetRef") } : {}) };
  if (form.mcpMode || form.mcpSelections.length) capabilities.mcpServers = { ...(form.mcpMode ? { mode: form.mcpMode } : {}), ...(form.mcpSelections.length ? { selections: buildSelections(form.mcpSelections, "serverRef", "mcpSetRef") } : {}) };
  if (Object.keys(capabilities).length) spec.capabilities = capabilities;
  if (Object.keys(harness).length > 0) {
    spec.harness = harness;
  }
  if (form.applicationName.trim()) {
    spec.scope = {
      applicationRef: { name: form.applicationName.trim() },
    };
  }
  return spec;
}

export function ProfileEditorPage({ token, namespace: activeNamespace, writeEnabled }: Props) {
  const { name: nameParam = "new", namespace: routeNamespace = "" } = useParams();
  const namespace = routeNamespace || activeNamespace;
  const navigate = useNavigate();
  const isCreate = nameParam === "new" || !nameParam;

  const [doc, setDoc] = useState<CompositionDocument | null>(null);
  const [form, setForm] = useState<ProfileForm>(emptyForm);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [loading, setLoading] = useState(!isCreate);
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [harnessDocs, setHarnessDocs] = useState<CompositionDocument[]>([]);
  const [skillSetDocs, setSkillSetDocs] = useState<CompositionDocument[]>([]);
  const [toolSetDocs, setToolSetDocs] = useState<CompositionDocument[]>([]);
  const [skillDocs, setSkillDocs] = useState<CompositionDocument[]>([]);
  const [toolDocs, setToolDocs] = useState<CompositionDocument[]>([]);
  const [mcpServerDocs, setMCPServerDocs] = useState<CompositionDocument[]>([]);
  const [mcpSetDocs, setMCPSetDocs] = useState<CompositionDocument[]>([]);

  useEffect(() => {
    if (!namespace || !token) {
      return;
    }
    const controller = new AbortController();
    void Promise.all([
      listComposition(token, namespace, "agent-harness-profiles", 200, controller.signal),
      listComposition(token, namespace, "agent-skill-sets", 200, controller.signal),
      listComposition(token, namespace, "agent-tool-sets", 200, controller.signal),
      listComposition(token, namespace, "agent-skills", 200, controller.signal),
      listComposition(token, namespace, "agent-tools", 200, controller.signal),
      listComposition(token, namespace, "agent-mcp-servers", 200, controller.signal),
      listComposition(token, namespace, "agent-mcp-sets", 200, controller.signal),
    ])
      .then(([h, s, t, skills, tools, mcpServers, mcpSets]) => {
        setHarnessDocs(h);
        setSkillSetDocs(s);
        setToolSetDocs(t);
        setSkillDocs(skills); setToolDocs(tools); setMCPServerDocs(mcpServers); setMCPSetDocs(mcpSets);
      })
      .catch(() => {
        // card grid will show empty; selected names still render as orphan cards
      });
    return () => controller.abort();
  }, [token, namespace]);

  const harnessOptions = useMemo(
    () => optionsFromDocs(harnessDocs, harnessMeta),
    [harnessDocs],
  );
  const skillSetOptions = useMemo(
    () => optionsFromDocs(skillSetDocs, skillSetMeta),
    [skillSetDocs],
  );
  const toolSetOptions = useMemo(
    () => optionsFromDocs(toolSetDocs, toolSetMeta),
    [toolSetDocs],
  );

  useEffect(() => {
    if (isCreate) {
      setForm(emptyForm());
      setDoc(null);
      setLoading(false);
      return;
    }
    if (!namespace) {
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void getComposition(token, namespace, "agent-run-profiles", nameParam, controller.signal)
      .then((loaded) => {
        setDoc(loaded);
        setForm(formFromDoc(loaded));
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setLoading(false);
        setError(err instanceof APIError ? err.message : String(err));
      });
    return () => controller.abort();
  }, [token, namespace, nameParam, isCreate]);

  const writable = useMemo(() => {
    if (!writeEnabled) {
      return false;
    }
    if (isCreate) {
      return true;
    }
    return Boolean(doc?.management.writable);
  }, [writeEnabled, isCreate, doc]);

  function update<K extends keyof ProfileForm>(key: K, value: ProfileForm[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function onSave() {
    setError(null);
    setInfo(null);
    const spec = buildSpec(form);
    const annotations = mergePresentationAnnotations(
      doc?.metadata.annotations,
      form.icon,
      form.screenshot,
    );
    if (isCreate) {
      const name = form.name.trim();
      if (!name) {
        setError("Profile name is required");
        return;
      }
      setSaving(true);
      try {
        const created = await createComposition(token, namespace, "agent-run-profiles", {
          metadata: { name, annotations },
          spec,
        });
        navigate(
          `/ns/${encodeURIComponent(namespace)}/profiles/${encodeURIComponent(created.metadata.name)}`,
          { replace: true },
        );
      } catch (err) {
        setError(err instanceof APIError ? err.message : String(err));
      } finally {
        setSaving(false);
      }
      return;
    }
    if (!doc?.metadata.resourceVersion) {
      setError("Missing resourceVersion; reload the profile");
      return;
    }
    setSaving(true);
    try {
      const mergedSpec: Record<string, unknown> = {
        ...(doc.spec ?? {}),
        ...spec,
      };
      if (!form.harnessProfileName.trim()) {
        delete mergedSpec.harnessProfileRef;
      }
      if (form.skillSetNames.length === 0) {
        delete mergedSpec.skillSets;
      }
      if (form.toolSetNames.length === 0) {
        delete mergedSpec.toolSets;
      }
      if (!form.skillMode && !form.skillSelections.length && !form.skillOverrides.length && !form.toolMode && !form.toolSelections.length && !form.mcpMode && !form.mcpSelections.length) {
        delete mergedSpec.capabilities;
      }
      // Keep existing harness fields (backend/image/etc) while applying intent/systemPrompt.
      if (typeof doc.spec?.harness === "object" && doc.spec.harness) {
        const prior = doc.spec.harness as Record<string, unknown>;
        const nextHarness = {
          ...prior,
          ...((spec.harness as Record<string, unknown> | undefined) ?? {}),
        };
        if (!form.intent) {
          delete nextHarness.intent;
        }
        if (!form.systemPrompt.trim()) {
          delete nextHarness.systemPrompt;
        }
        if (Object.keys(nextHarness).length > 0) {
          mergedSpec.harness = nextHarness;
        } else {
          delete mergedSpec.harness;
        }
      }
      const updated = await updateComposition(
        token,
        namespace,
        "agent-run-profiles",
        doc.metadata.name,
        {
          metadata: {
            name: doc.metadata.name,
            resourceVersion: doc.metadata.resourceVersion,
            labels: doc.metadata.labels,
            annotations: annotations ?? {},
          },
          spec: mergedSpec,
        },
      );
      setDoc(updated);
      setForm(formFromDoc(updated));
      setInfo(
        `Profile saved · ${updated.spec && Array.isArray((updated.spec as { skillSets?: { refs?: unknown[] } }).skillSets?.refs) ? (updated.spec as { skillSets: { refs: unknown[] } }).skillSets.refs.length : form.skillSetNames.length} skill set(s), ${form.toolSetNames.length} tool set(s)`,
      );
    } catch (err) {
      setError(err instanceof APIError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function onDelete() {
    if (!doc || deleteConfirm !== doc.metadata.name) {
      setError("Type the profile name to confirm delete");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await deleteComposition(token, namespace, "agent-run-profiles", doc.metadata.name);
      navigate("/profiles");
    } catch (err) {
      setError(err instanceof APIError ? err.message : String(err));
      setSaving(false);
    }
  }

  return (
    <div className="profile-editor">
      <div className="page-header">
        <div>
          <div className="breadcrumb">
            <Link to="/profiles">Profiles</Link>
            <span>/</span>
            <span>{isCreate ? "new" : nameParam}</span>
          </div>
          <h1 className="page-title">{isCreate ? "New AgentRunProfile" : nameParam}</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {" · "}
            compose by picking harness / skill / tool cards
          </p>
        </div>
        <div className="chip-row">
          {writable ? (
            <button type="button" className="btn btn-primary" disabled={saving} onClick={() => void onSave()}>
              {saving ? "Saving…" : isCreate ? "Create profile" : "Save"}
            </button>
          ) : (
            <span className="chip chip-mute">
              {writeEnabled ? "GitOps / non-console profile" : "write disabled"}
            </span>
          )}
        </div>
      </div>

      {!isCreate && doc && !doc.management.writable ? (
        <div className="banner banner-lock">
          {doc.management.reason === "gitops_protected"
            ? `GitOps source of truth (${doc.management.managedBy || "gitops"}). Edit the Git repository instead.`
            : "This profile is not console-managed and cannot be updated through the API."}
        </div>
      ) : null}

      {error ? <div className="banner banner-error">{error}</div> : null}
      {info ? <div className="banner banner-ok">{info}</div> : null}
      {loading ? <div className="empty">Loading…</div> : null}

      {!loading ? (
        <div className="panel">
          <div className="panel-header">
            <h2 className="panel-title">Profile composition</h2>
            {!isCreate && doc ? (
              <span className="chip mono">rv {doc.metadata.resourceVersion}</span>
            ) : null}
          </div>
          <div className="panel-body editor-form">
            <label className="field">
              <span className="label">Name</span>
              <input
                className="input mono"
                value={form.name}
                disabled={!isCreate}
                onChange={(event) => update("name", event.target.value)}
                placeholder="my-agent-profile"
              />
            </label>
            <label className="field">
              <span className="label">Description</span>
              <textarea
                className="textarea"
                value={form.description}
                disabled={!writable}
                onChange={(event) => update("description", event.target.value)}
              />
            </label>

            <IconPicker
              label="Profile avatar / icon"
              help="Assign a robot avatar or any image URL. Stored on the CR as ui.anvil.hazyforge.io/icon (screenshot banner optional)."
              icon={form.icon}
              screenshot={form.screenshot}
              disabled={!writable}
              onIconChange={(next) => update("icon", next)}
              onScreenshotChange={(next) => update("screenshot", next)}
            />

            <CompositionCardPicker
              mode="single"
              label="Harness profile"
              help="Pick one AgentHarnessProfile card (runtime image, identity, placement)."
              options={harnessOptions}
              value={form.harnessProfileName}
              disabled={!writable}
              emptyLabel="No harness profiles in this namespace."
              allowClear
              onChange={(next) => update("harnessProfileName", next)}
            />

            <h3 className="form-section-title">Canonical capabilities</h3>
            <div className="field-row">
              {(["skillMode", "toolMode", "mcpMode"] as const).map((key) => <label className="field" key={key}><span className="label">{key === "skillMode" ? "Skills" : key === "toolMode" ? "Tools" : "MCP servers"} mode</span><select className="select" value={form[key]} disabled={!writable} onChange={(event) => update(key, event.target.value)}><option value="">Append (default)</option><option value="Append">Append</option><option value="Replace">Replace inherited selections</option></select></label>)}
            </div>
            <CapabilitySelectionPicker label="Skills" help="Atomic skills and skill sets share one explicit order. Selecting a tool never selects a skill." atomicLabel="skill" setLabel="skill set" atomics={skillDocs} sets={skillSetDocs} value={form.skillSelections} disabled={!writable} onChange={(next) => update("skillSelections", next)} />
            {form.skillOverrides.length ? <div className="banner banner-info">{form.skillOverrides.length} existing skill override(s) are preserved. Edit their full shape through the advanced/API surface until the guided override editor is added.</div> : null}
            <CapabilitySelectionPicker label="Tools" help="Atomic tools and tool sets share one explicit order. This is code-execution capability, not runtime identity." atomicLabel="tool" setLabel="tool set" atomics={toolDocs} sets={toolSetDocs} value={form.toolSelections} disabled={!writable} onChange={(next) => update("toolSelections", next)} />
            <CapabilitySelectionPicker label="MCP servers" help="MCP servers and MCP sets compose independently from executable tools and skills." atomicLabel="server" setLabel="MCP set" atomics={mcpServerDocs} sets={mcpSetDocs} value={form.mcpSelections} disabled={!writable} onChange={(next) => update("mcpSelections", next)} />

            <details className="spec-preview" open={form.skillSetNames.length > 0 || form.toolSetNames.length > 0}>
              <summary>Deprecated compatibility selections</summary>
              <div className="banner banner-warn">These fields remain supported for existing objects. New profiles should use canonical capabilities above.</div>
            <CompositionCardPicker
              mode="multi"
              label="Skill sets"
              help="Click cards to compose instruction packs. Order matters — later sets can override same-named skills."
              options={skillSetOptions}
              value={form.skillSetNames}
              disabled={!writable}
              emptyLabel="No skill sets in this namespace."
              onChange={(next) => update("skillSetNames", next)}
            />

            <CompositionCardPicker
              mode="multi"
              label="Tool sets"
              help="Click cards to attach code-execution tool packs. Applied in list order after skill-set tools."
              options={toolSetOptions}
              value={form.toolSetNames}
              disabled={!writable}
              emptyLabel="No tool sets in this namespace."
              onChange={(next) => update("toolSetNames", next)}
            />
            </details>

            <div className="field-row">
              <label className="field">
                <span className="label">Intent</span>
                <select
                  className="select"
                  value={form.intent}
                  disabled={!writable}
                  onChange={(event) => update("intent", event.target.value)}
                >
                  <option value="">(unset)</option>
                  <option value="observe">observe</option>
                  <option value="fixTransient">fixTransient</option>
                  <option value="proposeChange">proposeChange</option>
                  <option value="cleanup">cleanup</option>
                </select>
              </label>
              <label className="field">
                <span className="label">Application scope (optional)</span>
                <input
                  className="input mono"
                  value={form.applicationName}
                  disabled={!writable}
                  onChange={(event) => update("applicationName", event.target.value)}
                  placeholder="hazy-trade"
                />
              </label>
            </div>
            <label className="field">
              <span className="label">Standing system prompt</span>
              <textarea
                className="textarea textarea-spec"
                value={form.systemPrompt}
                disabled={!writable}
                onChange={(event) => update("systemPrompt", event.target.value)}
              />
            </label>

            {!isCreate && writable ? (
              <div className="danger-zone">
                <div className="label">Delete</div>
                <p className="page-sub">
                  Type <span className="mono">{doc?.metadata.name}</span> to confirm.
                </p>
                <div className="editor-actions">
                  <input
                    className="input mono"
                    value={deleteConfirm}
                    onChange={(event) => setDeleteConfirm(event.target.value)}
                    placeholder="profile name"
                  />
                  <button
                    type="button"
                    className="btn btn-danger"
                    disabled={saving || deleteConfirm !== doc?.metadata.name}
                    onClick={() => void onDelete()}
                  >
                    Delete
                  </button>
                </div>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
