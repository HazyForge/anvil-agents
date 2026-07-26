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
import { OrderedRefList } from "../../components/OrderedRefList";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

interface ProfileForm {
  name: string;
  description: string;
  harnessProfileName: string;
  skillSetNames: string[];
  toolSetNames: string[];
  intent: string;
  systemPrompt: string;
  applicationName: string;
}

function emptyForm(): ProfileForm {
  return {
    name: "",
    description: "",
    harnessProfileName: "",
    skillSetNames: [],
    toolSetNames: [],
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
  return {
    name: doc.metadata.name,
    description: String(spec.description ?? ""),
    harnessProfileName: harnessRef,
    skillSetNames: uniqueNames(skillRefs),
    toolSetNames: uniqueNames(toolRefs),
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
  const [harnesses, setHarnesses] = useState<string[]>([]);
  const [skillSets, setSkillSets] = useState<string[]>([]);
  const [toolSets, setToolSets] = useState<string[]>([]);

  useEffect(() => {
    if (!namespace || !token) {
      return;
    }
    const controller = new AbortController();
    void Promise.all([
      listComposition(token, namespace, "agent-harness-profiles", 200, controller.signal),
      listComposition(token, namespace, "agent-skill-sets", 200, controller.signal),
      listComposition(token, namespace, "agent-tool-sets", 200, controller.signal),
    ])
      .then(([h, s, t]) => {
        setHarnesses(h.map((item) => item.metadata.name));
        setSkillSets(s.map((item) => item.metadata.name));
        setToolSets(t.map((item) => item.metadata.name));
      })
      .catch(() => {
        // free-text still works
      });
    return () => controller.abort();
  }, [token, namespace]);

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
    if (isCreate) {
      const name = form.name.trim();
      if (!name) {
        setError("Profile name is required");
        return;
      }
      setSaving(true);
      try {
        const created = await createComposition(token, namespace, "agent-run-profiles", {
          metadata: { name },
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
            annotations: doc.metadata.annotations,
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
            composition CRD · multiple skill/tool sets supported
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
            <label className="field">
              <span className="label">Harness profile</span>
              <input
                className="input mono"
                list="profile-harnesses"
                value={form.harnessProfileName}
                disabled={!writable}
                onChange={(event) => update("harnessProfileName", event.target.value)}
                placeholder="optional AgentHarnessProfile name"
              />
              <datalist id="profile-harnesses">
                {harnesses.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
            </label>

            <OrderedRefList
              label="Skill sets (ordered, multiple allowed)"
              help="Applied in list order. Later sets can override same-named skills."
              value={form.skillSetNames}
              options={skillSets}
              disabled={!writable}
              placeholder="AgentSkillSet name"
              onChange={(next) => update("skillSetNames", next)}
            />

            <OrderedRefList
              label="Tool sets (ordered, multiple allowed)"
              help="Applied in list order after skill-set tools. Independent of skill packs."
              value={form.toolSetNames}
              options={toolSets}
              disabled={!writable}
              placeholder="AgentToolSet name"
              onChange={(next) => update("toolSetNames", next)}
            />

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
