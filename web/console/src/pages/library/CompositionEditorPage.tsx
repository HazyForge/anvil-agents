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
import { HarnessProfileForm } from "../../components/HarnessProfileForm";
import { IconPicker } from "../../components/IconPicker";
import { RelatedAuthSessionCards } from "../../components/RelatedAuthSessionCards";
import {
  getIconUrl,
  getScreenshotUrl,
  mergePresentationAnnotations,
} from "../../utils/icons";
import {
  buildHarnessSpec,
  emptyHarnessForm,
  formFromHarnessSpec,
  validateHarnessForm,
  type HarnessForm,
} from "./harnessForm";

interface Props {
  token: string;
  namespace: string;
  writeEnabled: boolean;
}

const emptySpecByKind: Record<string, Record<string, unknown>> = {
  AgentSkillSet: { description: "", skills: [] },
  AgentToolSet: { description: "", tools: [] },
  AgentRunProfile: { description: "" },
  AgentHarnessProfile: {
    description: "",
    backend: { kind: "codex" },
    execution: {},
  },
  VolumeProfile: {
    description: "",
    volumes: [{ name: "home", purpose: "agent-home", mountPath: "/agent-home" }],
  },
  AgentDataVolume: { agentName: "", notes: "" },
};

const KIND_INTRO: Record<string, { title: string; body: string }> = {
  AgentSkillSet: {
    title: "What is an AgentSkillSet?",
    body: "Backend-neutral instruction packs and personas. Skills teach agents how to think and when to use tools — they should not carry images, Secrets, or ServiceAccounts.",
  },
  AgentToolSet: {
    title: "What is an AgentToolSet?",
    body: "Setup scripts and verify contracts for tools the agent can run. This is code-execution authority; keep setup scripts pinned and credentials in harness secret refs.",
  },
  VolumeProfile: {
    title: "What is a VolumeProfile?",
    body: "A reusable durable storage shape (sizes, mount paths, purposes) that AgentDataVolumes can instantiate.",
  },
  AgentDataVolume: {
    title: "What is an AgentDataVolume?",
    body: "A concrete PVC-backed agent home (sessions, OAuth, caches). Attach it from a harness profile's data volume refs.",
  },
};

export function CompositionEditorPage({ token, namespace: activeNamespace, writeEnabled }: Props) {
  const { kind: kindRoute = "", name: nameParam = "", namespace: routeNamespace = "" } = useParams();
  const namespace = routeNamespace || activeNamespace;
  const navigate = useNavigate();
  const kind = compositionKindByRoute(kindRoute);
  const isCreate = nameParam === "new" || !nameParam;
  // Match both kind metadata and the URL segment so a routing glitch cannot fall back to JSON-only.
  const isHarness =
    kind?.kind === "AgentHarnessProfile" || kindRoute === "harness-profiles";
  const isAuthSession =
    kind?.kind === "AgentAuthSession" || kindRoute === "auth-sessions";
  const isDataVolume =
    kind?.kind === "AgentDataVolume" || kindRoute === "data-volumes";

  const [doc, setDoc] = useState<CompositionDocument | null>(null);
  const [name, setName] = useState("");
  const [specText, setSpecText] = useState("{}");
  const [harnessForm, setHarnessForm] = useState<HarnessForm>(emptyHarnessForm);
  const [showAdvancedJson, setShowAdvancedJson] = useState(false);
  const [icon, setIcon] = useState("");
  const [screenshot, setScreenshot] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [loading, setLoading] = useState(!isCreate);
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [dataVolumeNames, setDataVolumeNames] = useState<string[]>([]);

  useEffect(() => {
    if (!kind || !namespace || !isHarness || !token) {
      return;
    }
    const controller = new AbortController();
    void listComposition(token, namespace, "agent-data-volumes", 200, controller.signal)
      .then((docs) => setDataVolumeNames(docs.map((item) => item.metadata.name)))
      .catch(() => setDataVolumeNames([]));
    return () => controller.abort();
  }, [token, namespace, kind, isHarness]);

  useEffect(() => {
    if (!kind || !namespace || isCreate) {
      if (kind) {
        setSpecText(JSON.stringify(emptySpecByKind[kind.kind] ?? {}, null, 2));
        if (kind.kind === "AgentHarnessProfile") {
          setHarnessForm(emptyHarnessForm());
          setShowAdvancedJson(false);
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
        if (kind.kind === "AgentHarnessProfile") {
          setHarnessForm(formFromHarnessSpec(spec, loaded.metadata.name));
          setShowAdvancedJson(false);
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

  const harnessPreview = useMemo(() => {
    if (!isHarness) {
      return "";
    }
    try {
      return JSON.stringify(buildHarnessSpec(harnessForm), null, 2);
    } catch {
      return "{}";
    }
  }, [isHarness, harnessForm]);

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

  function resolveSpec(): { ok: true; spec: Record<string, unknown> } | { ok: false; error: string } {
    if (isHarness && !showAdvancedJson) {
      const validation = validateHarnessForm(harnessForm, isCreate);
      if (validation) {
        return { ok: false, error: validation };
      }
      return { ok: true, spec: buildHarnessSpec(harnessForm) };
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
      const createName = (isHarness ? harnessForm.name : name).trim();
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
      if (isHarness) {
        setHarnessForm(formFromHarnessSpec(updated.spec ?? {}, updated.metadata.name));
      }
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

  const intro = KIND_INTRO[kind.kind];

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
              ? isHarness
                ? "New harness profile"
                : `New ${kind.kind}`
              : nameParam}
          </h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
            {isHarness ? " · runtime machine for AgentRuns" : null}
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
            <h2 className="panel-title">
              {isHarness
                ? writable
                  ? isCreate
                    ? "Compose harness"
                    : "Edit harness"
                  : "View harness"
                : writable
                  ? "Edit"
                  : "View"}
            </h2>
            {!isCreate && doc ? (
              <span className="chip mono">rv {doc.metadata.resourceVersion}</span>
            ) : null}
          </div>
          <div className="panel-body editor-form">
            {intro && !isHarness ? (
              <section className="explain-panel">
                <h3 className="explain-title">{intro.title}</h3>
                <p className="explain-body">{intro.body}</p>
              </section>
            ) : null}

            {isHarness ? (
              <>
                <HarnessProfileForm
                  form={harnessForm}
                  disabled={!writable}
                  isCreate={isCreate}
                  dataVolumeNames={dataVolumeNames}
                  token={token}
                  namespace={namespace}
                  onChange={updateHarness}
                />
                <IconPicker
                  label="Harness icon"
                  help="Optional avatar for library cards and profile composition picker."
                  icon={icon}
                  screenshot={screenshot}
                  disabled={!writable}
                  onIconChange={setIcon}
                  onScreenshotChange={setScreenshot}
                />
                <div className="advanced-json">
                  <button
                    type="button"
                    className="btn btn-ghost"
                    onClick={() => {
                      if (!showAdvancedJson) {
                        setSpecText(harnessPreview || "{}");
                      } else {
                        try {
                          const parsed = JSON.parse(specText) as Record<string, unknown>;
                          setHarnessForm(
                            formFromHarnessSpec(parsed, isCreate ? harnessForm.name : nameParam),
                          );
                        } catch {
                          // keep form as-is if JSON invalid
                        }
                      }
                      setShowAdvancedJson((prev) => !prev);
                    }}
                  >
                    {showAdvancedJson ? "Back to guided form" : "Advanced: edit raw JSON"}
                  </button>
                  {showAdvancedJson ? (
                    <label className="field" style={{ marginTop: "0.5rem" }}>
                      <span className="label">Spec (JSON)</span>
                      <textarea
                        className="textarea textarea-spec"
                        value={specText}
                        disabled={!writable}
                        onChange={(event) => setSpecText(event.target.value)}
                        spellCheck={false}
                      />
                    </label>
                  ) : (
                    <details className="spec-preview">
                      <summary className="text-mute">Preview generated spec</summary>
                      <pre className="spec-preview-pre mono">{harnessPreview}</pre>
                    </details>
                  )}
                </div>
              </>
            ) : (
              <>
                {isAuthSession ? (
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
                ) : null}

                <label className="field">
                  <span className="label">Name</span>
                  <input
                    className="input mono"
                    value={isCreate ? name : nameParam}
                    disabled={!isCreate || isAuthSession}
                    onChange={(event) => setName(event.target.value)}
                    placeholder="dns-1123 name"
                  />
                </label>

                {!isAuthSession ? (
                  <IconPicker
                    label={`${kind.title.replace(/\.$/, "")} icon`}
                    help="Same robot pack and custom URLs as profiles. Stored as ui.anvil.hazyforge.io/icon (+ optional screenshot)."
                    icon={icon}
                    screenshot={screenshot}
                    disabled={!writable}
                    onIconChange={setIcon}
                    onScreenshotChange={setScreenshot}
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

                {isAuthSession && doc ? (
                  <div className="chip-row" style={{ marginBottom: "0.5rem" }}>
                    <span className="chip chip-mute">AgentAuthSession</span>
                    <span className="chip mono">{String(doc.spec?.provider ?? "")}</span>
                    <span className="chip mono">{String(doc.spec?.action ?? "")}</span>
                    <span className="chip">
                      {String(doc.status?.phase ?? "Pending")}
                    </span>
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
                  <span className="label">
                    {isAuthSession ? "Spec + status (read-only JSON)" : "Spec (JSON)"}
                  </span>
                  <p className="field-help">
                    {isAuthSession
                      ? "Append-only. Use anvil-agentctl for reauth/logout."
                      : "Guided forms for skill sets, tool sets, and volumes are next. Until then, edit the CRD shape as JSON."}
                  </p>
                  <textarea
                    className="textarea textarea-spec"
                    value={
                      isAuthSession && doc
                        ? JSON.stringify({ spec: doc.spec, status: doc.status }, null, 2)
                        : specText
                    }
                    disabled={!writable || isAuthSession}
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
