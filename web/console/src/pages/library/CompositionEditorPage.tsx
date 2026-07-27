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
import {
  compositionKindByRoute,
  type CompositionDocument,
} from "../../api/types.composition";
import { DataVolumeForm } from "../../components/DataVolumeForm";
import { HarnessProfileForm } from "../../components/HarnessProfileForm";
import { IconPicker } from "../../components/IconPicker";
import { RelatedAuthSessionCards } from "../../components/RelatedAuthSessionCards";
import { SkillSetForm } from "../../components/SkillSetForm";
import { ToolSetForm } from "../../components/ToolSetForm";
import { VolumeProfileForm } from "../../components/VolumeProfileForm";
import {
  getIconUrl,
  getScreenshotUrl,
  mergePresentationAnnotations,
} from "../../utils/icons";
import {
  buildDataVolumeSpec,
  emptyDataVolumeForm,
  formFromDataVolumeSpec,
  validateDataVolumeForm,
  type DataVolumeForm as DataVolumeFormModel,
} from "./dataVolumeForm";
import {
  buildHarnessSpec,
  emptyHarnessForm,
  formFromHarnessSpec,
  validateHarnessForm,
  type HarnessForm,
} from "./harnessForm";
import {
  buildSkillSetSpec,
  emptySkillSetForm,
  formFromSkillSetSpec,
  validateSkillSetForm,
  type SkillSetForm as SkillSetFormModel,
} from "./skillSetForm";
import {
  buildToolSetSpec,
  emptyToolSetForm,
  formFromToolSetSpec,
  validateToolSetForm,
  type ToolSetForm as ToolSetFormModel,
} from "./toolSetForm";
import {
  buildVolumeProfileSpec,
  emptyVolumeProfileForm,
  formFromVolumeProfileSpec,
  validateVolumeProfileForm,
  type VolumeProfileForm as VolumeProfileFormModel,
} from "./volumeProfileForm";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

type GuidedKind =
  | "AgentHarnessProfile"
  | "AgentDataVolume"
  | "VolumeProfile"
  | "AgentSkillSet"
  | "AgentToolSet";

function guidedKindFrom(
  kindName: string | undefined,
  kindRoute: string,
): GuidedKind | null {
  if (kindName === "AgentHarnessProfile" || kindRoute === "harness-profiles") {
    return "AgentHarnessProfile";
  }
  if (kindName === "AgentDataVolume" || kindRoute === "data-volumes") {
    return "AgentDataVolume";
  }
  if (kindName === "VolumeProfile" || kindRoute === "volume-profiles") {
    return "VolumeProfile";
  }
  if (kindName === "AgentSkillSet" || kindRoute === "skill-sets") {
    return "AgentSkillSet";
  }
  if (kindName === "AgentToolSet" || kindRoute === "tool-sets") {
    return "AgentToolSet";
  }
  return null;
}

const CREATE_TITLES: Record<GuidedKind, string> = {
  AgentHarnessProfile: "New harness profile",
  AgentDataVolume: "New data volume",
  VolumeProfile: "New volume profile",
  AgentSkillSet: "New skill set",
  AgentToolSet: "New tool set",
};

export function CompositionEditorPage({ token, namespace: activeNamespace, writeEnabled }: Props) {
  const { kind: kindRoute = "", name: nameParam = "", namespace: routeNamespace = "" } = useParams();
  const namespace = routeNamespace || activeNamespace;
  const navigate = useNavigate();
  const kind = compositionKindByRoute(kindRoute);
  const isCreate = nameParam === "new" || !nameParam;
  const guided = guidedKindFrom(kind?.kind, kindRoute);
  const isHarness = guided === "AgentHarnessProfile";
  const isDataVolume = guided === "AgentDataVolume";
  const isVolumeProfile = guided === "VolumeProfile";
  const isSkillSet = guided === "AgentSkillSet";
  const isToolSet = guided === "AgentToolSet";
  const isAuthSession =
    kind?.kind === "AgentAuthSession" || kindRoute === "auth-sessions";
  const isGuided = guided !== null;

  const [doc, setDoc] = useState<CompositionDocument | null>(null);
  const [name, setName] = useState("");
  const [specText, setSpecText] = useState("{}");
  const [harnessForm, setHarnessForm] = useState<HarnessForm>(emptyHarnessForm);
  const [dataVolumeForm, setDataVolumeForm] = useState<DataVolumeFormModel>(emptyDataVolumeForm);
  const [volumeProfileForm, setVolumeProfileForm] =
    useState<VolumeProfileFormModel>(emptyVolumeProfileForm);
  const [skillSetForm, setSkillSetForm] = useState<SkillSetFormModel>(emptySkillSetForm);
  const [toolSetForm, setToolSetForm] = useState<ToolSetFormModel>(emptyToolSetForm);
  const [showAdvancedJson, setShowAdvancedJson] = useState(false);
  const [icon, setIcon] = useState("");
  const [screenshot, setScreenshot] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [loading, setLoading] = useState(!isCreate);
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [dataVolumeNames, setDataVolumeNames] = useState<string[]>([]);
  const [volumeProfileNames, setVolumeProfileNames] = useState<string[]>([]);

  useEffect(() => {
    if (!kind || !namespace || !token) {
      return;
    }
    if (!isHarness && !isDataVolume) {
      return;
    }
    const controller = new AbortController();
    if (isHarness) {
      void listComposition(token, namespace, "agent-data-volumes", 200, controller.signal)
        .then((docs) => setDataVolumeNames(docs.map((item) => item.metadata.name)))
        .catch(() => setDataVolumeNames([]));
    }
    if (isDataVolume) {
      void listComposition(token, namespace, "volume-profiles", 200, controller.signal)
        .then((docs) => setVolumeProfileNames(docs.map((item) => item.metadata.name)))
        .catch(() => setVolumeProfileNames([]));
    }
    return () => controller.abort();
  }, [token, namespace, kind, isHarness, isDataVolume]);

  useEffect(() => {
    if (!kind || !namespace || isCreate) {
      if (kind) {
        setShowAdvancedJson(false);
        setName("");
        setHarnessForm(emptyHarnessForm());
        setDataVolumeForm(emptyDataVolumeForm());
        setVolumeProfileForm(emptyVolumeProfileForm());
        setSkillSetForm(emptySkillSetForm());
        setToolSetForm(emptyToolSetForm());
        // Seed advanced JSON from empty guided builders so toggle stays consistent.
        if (kind.kind === "AgentHarnessProfile") {
          setSpecText(JSON.stringify(buildHarnessSpec(emptyHarnessForm()), null, 2));
        } else if (kind.kind === "AgentDataVolume") {
          setSpecText(JSON.stringify(buildDataVolumeSpec(emptyDataVolumeForm()), null, 2));
        } else if (kind.kind === "VolumeProfile") {
          setSpecText(JSON.stringify(buildVolumeProfileSpec(emptyVolumeProfileForm()), null, 2));
        } else if (kind.kind === "AgentSkillSet") {
          setSpecText(JSON.stringify(buildSkillSetSpec(emptySkillSetForm()), null, 2));
        } else if (kind.kind === "AgentToolSet") {
          setSpecText(JSON.stringify(buildToolSetSpec(emptyToolSetForm()), null, 2));
        } else {
          setSpecText("{}");
        }
      }
      setIcon("");
      setScreenshot("");
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void getComposition(token, namespace, kind.segment, nameParam, controller.signal)
      .then((loaded) => {
        setDoc(loaded);
        setName(loaded.metadata.name);
        const spec = loaded.spec ?? {};
        setSpecText(JSON.stringify(spec, null, 2));
        setShowAdvancedJson(false);
        if (kind.kind === "AgentHarnessProfile") {
          setHarnessForm(formFromHarnessSpec(spec, loaded.metadata.name));
        } else if (kind.kind === "AgentDataVolume") {
          setDataVolumeForm(formFromDataVolumeSpec(spec, loaded.metadata.name));
        } else if (kind.kind === "VolumeProfile") {
          setVolumeProfileForm(formFromVolumeProfileSpec(spec, loaded.metadata.name));
        } else if (kind.kind === "AgentSkillSet") {
          setSkillSetForm(formFromSkillSetSpec(spec, loaded.metadata.name));
        } else if (kind.kind === "AgentToolSet") {
          setToolSetForm(formFromToolSetSpec(spec, loaded.metadata.name));
        }
        setIcon(getIconUrl(loaded.metadata.annotations));
        setScreenshot(getScreenshotUrl(loaded.metadata.annotations));
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
  }, [token, namespace, kind, nameParam, isCreate]);

  const writable = useMemo(() => {
    if (!writeEnabled) {
      return false;
    }
    if (isCreate) {
      return true;
    }
    return Boolean(doc?.management.writable);
  }, [writeEnabled, isCreate, doc]);

  const guidedPreview = useMemo(() => {
    if (!guided || showAdvancedJson) {
      return "";
    }
    try {
      switch (guided) {
        case "AgentHarnessProfile":
          return JSON.stringify(buildHarnessSpec(harnessForm), null, 2);
        case "AgentDataVolume":
          return JSON.stringify(buildDataVolumeSpec(dataVolumeForm), null, 2);
        case "VolumeProfile":
          return JSON.stringify(buildVolumeProfileSpec(volumeProfileForm), null, 2);
        case "AgentSkillSet":
          return JSON.stringify(buildSkillSetSpec(skillSetForm), null, 2);
        case "AgentToolSet":
          return JSON.stringify(buildToolSetSpec(toolSetForm), null, 2);
        default:
          return "";
      }
    } catch {
      return "{}";
    }
  }, [
    guided,
    showAdvancedJson,
    harnessForm,
    dataVolumeForm,
    volumeProfileForm,
    skillSetForm,
    toolSetForm,
  ]);

  if (!kind) {
    return (
      <div className="panel">
        <div className="panel-body empty">Unknown composition kind.</div>
      </div>
    );
  }

  function updateHarness<K extends keyof HarnessForm>(key: K, value: HarnessForm[K]) {
    setHarnessForm((prev) => ({ ...prev, [key]: value }));
  }

  function updateDataVolume<K extends keyof DataVolumeFormModel>(
    key: K,
    value: DataVolumeFormModel[K],
  ) {
    setDataVolumeForm((prev) => ({ ...prev, [key]: value }));
  }

  function updateVolumeProfile<K extends keyof VolumeProfileFormModel>(
    key: K,
    value: VolumeProfileFormModel[K],
  ) {
    setVolumeProfileForm((prev) => ({ ...prev, [key]: value }));
  }

  function updateSkillSet<K extends keyof SkillSetFormModel>(
    key: K,
    value: SkillSetFormModel[K],
  ) {
    setSkillSetForm((prev) => ({ ...prev, [key]: value }));
  }

  function updateToolSet<K extends keyof ToolSetFormModel>(
    key: K,
    value: ToolSetFormModel[K],
  ) {
    setToolSetForm((prev) => ({ ...prev, [key]: value }));
  }

  function createNameFromForms(): string {
    if (isHarness) {
      return harnessForm.name.trim();
    }
    if (isDataVolume) {
      return dataVolumeForm.name.trim();
    }
    if (isVolumeProfile) {
      return volumeProfileForm.name.trim();
    }
    if (isSkillSet) {
      return skillSetForm.name.trim();
    }
    if (isToolSet) {
      return toolSetForm.name.trim();
    }
    return name.trim();
  }

  function resolveSpec(): { ok: true; spec: Record<string, unknown> } | { ok: false; error: string } {
    if (isGuided && !showAdvancedJson) {
      switch (guided) {
        case "AgentHarnessProfile": {
          const validation = validateHarnessForm(harnessForm, isCreate);
          if (validation) {
            return { ok: false, error: validation };
          }
          return { ok: true, spec: buildHarnessSpec(harnessForm) };
        }
        case "AgentDataVolume": {
          const validation = validateDataVolumeForm(dataVolumeForm, isCreate);
          if (validation) {
            return { ok: false, error: validation };
          }
          return { ok: true, spec: buildDataVolumeSpec(dataVolumeForm) };
        }
        case "VolumeProfile": {
          const validation = validateVolumeProfileForm(volumeProfileForm, isCreate);
          if (validation) {
            return { ok: false, error: validation };
          }
          return { ok: true, spec: buildVolumeProfileSpec(volumeProfileForm) };
        }
        case "AgentSkillSet": {
          const validation = validateSkillSetForm(skillSetForm, isCreate);
          if (validation) {
            return { ok: false, error: validation };
          }
          return { ok: true, spec: buildSkillSetSpec(skillSetForm) };
        }
        case "AgentToolSet": {
          const validation = validateToolSetForm(toolSetForm, isCreate);
          if (validation) {
            return { ok: false, error: validation };
          }
          return { ok: true, spec: buildToolSetSpec(toolSetForm) };
        }
        default:
          break;
      }
    }
    try {
      return { ok: true, spec: JSON.parse(specText) as Record<string, unknown> };
    } catch (err) {
      return {
        ok: false,
        error: `Invalid JSON spec: ${err instanceof Error ? err.message : String(err)}`,
      };
    }
  }

  function applySpecToForms(spec: Record<string, unknown>, resourceName: string) {
    if (isHarness) {
      setHarnessForm(formFromHarnessSpec(spec, resourceName));
    } else if (isDataVolume) {
      setDataVolumeForm(formFromDataVolumeSpec(spec, resourceName));
    } else if (isVolumeProfile) {
      setVolumeProfileForm(formFromVolumeProfileSpec(spec, resourceName));
    } else if (isSkillSet) {
      setSkillSetForm(formFromSkillSetSpec(spec, resourceName));
    } else if (isToolSet) {
      setToolSetForm(formFromToolSetSpec(spec, resourceName));
    }
  }

  async function onSave() {
    if (!kind) {
      return;
    }
    setError(null);
    setInfo(null);
    const resolved = resolveSpec();
    if (!resolved.ok) {
      setError(resolved.error);
      return;
    }
    const { spec } = resolved;
    const annotations = mergePresentationAnnotations(
      doc?.metadata.annotations,
      icon,
      screenshot,
    );
    if (isCreate) {
      const createName = createNameFromForms();
      if (!createName) {
        setError("Name is required");
        return;
      }
      setSaving(true);
      try {
        const created = await createComposition(token, namespace, kind.segment, {
          metadata: { name: createName, annotations },
          spec,
        });
        navigate(
          `/ns/${encodeURIComponent(namespace)}/${kind.route}/${encodeURIComponent(created.metadata.name)}`,
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
      setError("Missing resourceVersion; reload the object");
      return;
    }
    setSaving(true);
    try {
      const updated = await updateComposition(token, namespace, kind.segment, doc.metadata.name, {
        metadata: {
          name: doc.metadata.name,
          resourceVersion: doc.metadata.resourceVersion,
          labels: doc.metadata.labels,
          annotations: annotations ?? {},
        },
        spec,
      });
      setDoc(updated);
      setSpecText(JSON.stringify(updated.spec ?? {}, null, 2));
      applySpecToForms(updated.spec ?? {}, updated.metadata.name);
      setIcon(getIconUrl(updated.metadata.annotations));
      setScreenshot(getScreenshotUrl(updated.metadata.annotations));
      setInfo("Saved");
    } catch (err) {
      setError(err instanceof APIError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function onDelete() {
    if (!kind || !doc || deleteConfirm !== doc.metadata.name) {
      setError("Type the resource name to confirm delete");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await deleteComposition(token, namespace, kind.segment, doc.metadata.name);
      navigate(`/ns/${encodeURIComponent(namespace)}/${kind.route}`);
    } catch (err) {
      setError(err instanceof APIError ? err.message : String(err));
      setSaving(false);
    }
  }

  function toggleAdvancedJson() {
    if (!showAdvancedJson) {
      setSpecText(guidedPreview || "{}");
    } else {
      try {
        const parsed = JSON.parse(specText) as Record<string, unknown>;
        const resourceName = isCreate ? createNameFromForms() : nameParam;
        applySpecToForms(parsed, resourceName);
      } catch {
        // keep form as-is if JSON invalid
      }
    }
    setShowAdvancedJson((prev) => !prev);
  }

  const panelTitle = isGuided
    ? writable
      ? isCreate
        ? `Compose ${kind.plural.replace(/s$/, "")}`
        : `Edit ${kind.plural.replace(/s$/, "")}`
      : `View ${kind.plural.replace(/s$/, "")}`
    : writable
      ? "Edit"
      : "View";

  return (
    <div className="library-editor">
      <div className="page-header">
        <div>
          <div className="breadcrumb">
            <Link to="/library">Library</Link>
            <span>/</span>
            <Link to={`/ns/${encodeURIComponent(namespace)}/${kind.route}`}>{kind.title}</Link>
            <span>/</span>
            <span>{isCreate ? "new" : nameParam}</span>
          </div>
          <h1 className="page-title">
            {isCreate
              ? guided
                ? CREATE_TITLES[guided]
                : `New ${kind.kind}`
              : nameParam}
          </h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {isHarness ? " · runtime machine for AgentRuns" : null}
            {isDataVolume ? " · durable PVC home" : null}
            {isVolumeProfile ? " · reusable storage shape" : null}
            {isSkillSet ? " · instruction packs" : null}
            {isToolSet ? " · setup / verify tools" : null}
            {isAuthSession ? " · append-only auth maintenance" : null}
          </p>
        </div>
      </div>

      {kind.danger ? (
        <div className="banner banner-warn">
          Editing {kind.plural} grants substantial code-execution authority in this namespace
          (images, setup scripts, ServiceAccount and Secret refs). Prefer GitOps for shared
          production objects.
        </div>
      ) : null}

      {!isCreate && doc && !doc.management.writable ? (
        <div className="banner banner-lock">
          {doc.management.reason === "gitops_protected"
            ? `GitOps source of truth (${doc.management.managedBy || "gitops"}). Edit the Git
              repository instead of overriding the live object.`
            : "This object is not console-managed. The API will not update or delete it."}
        </div>
      ) : null}

      {error ? <div className="banner banner-error">{error}</div> : null}
      {info ? <div className="banner banner-ok">{info}</div> : null}
      {loading ? <div className="empty">Loading…</div> : null}

      {!loading ? (
        <div className="panel">
          <div className="panel-header">
            <h2 className="panel-title">{panelTitle}</h2>
            {!isCreate && doc ? (
              <span className="chip mono">rv {doc.metadata.resourceVersion}</span>
            ) : null}
          </div>
          <div className="panel-body editor-form">
            {isAuthSession ? (
              <>
                <section className="explain-panel">
                  <h3 className="explain-title">AgentAuthSession (append-only)</h3>
                  <p className="explain-body">
                    Reauth/logout maintenance for a durable home. Specs are immutable. Create
                    sessions with{" "}
                    <span className="mono">anvil-agentctl auth codex|grok reauth|logout</span>.
                    While phase is non-terminal, AgentRuns mounting the target data volume stay{" "}
                    <span className="mono">Pending</span> (
                    <span className="mono">AuthSessionActive</span>).
                  </p>
                </section>

                <label className="field">
                  <span className="label">Name</span>
                  <input className="input mono" value={nameParam} disabled />
                </label>

                {doc ? (
                  <div className="chip-row" style={{ marginBottom: "0.5rem" }}>
                    <span className="chip chip-mute">AgentAuthSession</span>
                    <span className="chip mono">{String(doc.spec?.provider ?? "")}</span>
                    <span className="chip mono">{String(doc.spec?.action ?? "")}</span>
                    <span className="chip">{String(doc.status?.phase ?? "Pending")}</span>
                    {typeof doc.spec?.dataVolumeRef === "object" &&
                    doc.spec.dataVolumeRef &&
                    "name" in (doc.spec.dataVolumeRef as object) ? (
                      <Link
                        className="chip"
                        to={`/ns/${encodeURIComponent(namespace)}/data-volumes/${encodeURIComponent(String((doc.spec.dataVolumeRef as { name?: string }).name ?? ""))}`}
                      >
                        vol:{(doc.spec.dataVolumeRef as { name?: string }).name}
                      </Link>
                    ) : null}
                  </div>
                ) : null}

                <label className="field">
                  <span className="label">Spec + status (read-only JSON)</span>
                  <p className="field-help">
                    Append-only. Use anvil-agentctl for reauth/logout.
                  </p>
                  <textarea
                    className="textarea textarea-spec"
                    value={
                      doc
                        ? JSON.stringify({ spec: doc.spec, status: doc.status }, null, 2)
                        : specText
                    }
                    disabled
                    spellCheck={false}
                  />
                </label>
              </>
            ) : isGuided ? (
              <>
                {!showAdvancedJson ? (
                  <>
                    {isHarness ? (
                      <HarnessProfileForm
                        form={harnessForm}
                        disabled={!writable}
                        isCreate={isCreate}
                        dataVolumeNames={dataVolumeNames}
                        token={token}
                        namespace={namespace}
                        onChange={updateHarness}
                      />
                    ) : null}
                    {isDataVolume ? (
                      <DataVolumeForm
                        form={dataVolumeForm}
                        disabled={!writable}
                        isCreate={isCreate}
                        volumeProfileNames={volumeProfileNames}
                        onChange={updateDataVolume}
                        onReplace={setDataVolumeForm}
                      />
                    ) : null}
                    {isVolumeProfile ? (
                      <VolumeProfileForm
                        form={volumeProfileForm}
                        disabled={!writable}
                        isCreate={isCreate}
                        onChange={updateVolumeProfile}
                      />
                    ) : null}
                    {isSkillSet ? (
                      <SkillSetForm
                        form={skillSetForm}
                        disabled={!writable}
                        isCreate={isCreate}
                        onChange={updateSkillSet}
                      />
                    ) : null}
                    {isToolSet ? (
                      <ToolSetForm
                        form={toolSetForm}
                        disabled={!writable}
                        isCreate={isCreate}
                        onChange={updateToolSet}
                      />
                    ) : null}

                    {isDataVolume && !isCreate ? (
                      <RelatedAuthSessionCards
                        token={token}
                        namespace={namespace}
                        dataVolumeNames={[nameParam]}
                        title="Auth sessions for this data volume"
                      />
                    ) : null}

                    <IconPicker
                      label={`${kind.title.replace(/\.$/, "")} icon`}
                      help="Optional avatar for library cards and composition pickers."
                      icon={icon}
                      screenshot={screenshot}
                      disabled={!writable}
                      onIconChange={setIcon}
                      onScreenshotChange={setScreenshot}
                    />
                  </>
                ) : (
                  <>
                    {isCreate ? (
                      <label className="field">
                        <span className="label">Name</span>
                        <input
                          className="input mono"
                          value={createNameFromForms()}
                          disabled={!writable}
                          onChange={(event) => {
                            const value = event.target.value;
                            if (isHarness) {
                              updateHarness("name", value);
                            } else if (isDataVolume) {
                              updateDataVolume("name", value);
                            } else if (isVolumeProfile) {
                              updateVolumeProfile("name", value);
                            } else if (isSkillSet) {
                              updateSkillSet("name", value);
                            } else if (isToolSet) {
                              updateToolSet("name", value);
                            } else {
                              setName(value);
                            }
                          }}
                          placeholder="dns-1123 name"
                        />
                      </label>
                    ) : null}
                    <IconPicker
                      label={`${kind.title.replace(/\.$/, "")} icon`}
                      help="Optional avatar for library cards and composition pickers."
                      icon={icon}
                      screenshot={screenshot}
                      disabled={!writable}
                      onIconChange={setIcon}
                      onScreenshotChange={setScreenshot}
                    />
                    <label className="field">
                      <span className="label">Spec (JSON)</span>
                      <textarea
                        className="textarea textarea-spec"
                        value={specText}
                        disabled={!writable}
                        onChange={(event) => setSpecText(event.target.value)}
                        spellCheck={false}
                      />
                    </label>
                  </>
                )}

                <div className="advanced-json">
                  <button type="button" className="btn btn-ghost" onClick={toggleAdvancedJson}>
                    {showAdvancedJson ? "Back to guided form" : "Advanced: edit raw JSON"}
                  </button>
                  {!showAdvancedJson ? (
                    <details className="spec-preview">
                      <summary className="text-mute">Preview generated spec</summary>
                      <pre className="spec-preview-pre mono">{guidedPreview}</pre>
                    </details>
                  ) : null}
                </div>
              </>
            ) : (
              <>
                <label className="field">
                  <span className="label">Name</span>
                  <input
                    className="input mono"
                    value={isCreate ? name : nameParam}
                    disabled={!isCreate}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="dns-1123 name"
                  />
                </label>
                <IconPicker
                  label={`${kind.title.replace(/\.$/, "")} icon`}
                  help="Optional avatar for library cards."
                  icon={icon}
                  screenshot={screenshot}
                  disabled={!writable}
                  onIconChange={setIcon}
                  onScreenshotChange={setScreenshot}
                />
                <label className="field">
                  <span className="label">Spec (JSON)</span>
                  <textarea
                    className="textarea textarea-spec"
                    value={specText}
                    disabled={!writable}
                    onChange={(event) => setSpecText(event.target.value)}
                    spellCheck={false}
                  />
                </label>
              </>
            )}

            <div className="editor-actions">
              {writable && !isAuthSession ? (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={saving}
                  onClick={() => void onSave()}
                >
                  {saving ? "Saving…" : isCreate ? "Create" : "Save"}
                </button>
              ) : (
                <span className="text-mute">
                  {isAuthSession
                    ? "Append-only · create with anvil-agentctl auth"
                    : writeEnabled
                      ? "Read-only · GitOps / non-console object"
                      : "Composition write is disabled on this API"}
                </span>
              )}
            </div>

            {!isCreate && writable && !isAuthSession ? (
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
                    placeholder="resource name"
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
