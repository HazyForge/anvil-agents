import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { APIError } from "../../api/client";
import {
  createComposition,
  deleteComposition,
  getComposition,
  updateComposition,
} from "../../api/composition";
import {
  compositionKindByRoute,
  type CompositionDocument,
} from "../../api/types.composition";

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

export function CompositionEditorPage({ token, namespace: activeNamespace, writeEnabled }: Props) {
  const { kind: kindRoute = "", name: nameParam = "", namespace: routeNamespace = "" } = useParams();
  const namespace = routeNamespace || activeNamespace;
  const navigate = useNavigate();
  const kind = compositionKindByRoute(kindRoute);
  const isCreate = nameParam === "new" || !nameParam;

  const [doc, setDoc] = useState<CompositionDocument | null>(null);
  const [name, setName] = useState("");
  const [specText, setSpecText] = useState("{}");
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [loading, setLoading] = useState(!isCreate);
  const [saving, setSaving] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState("");

  useEffect(() => {
    if (!kind || !namespace || isCreate) {
      if (kind) {
        setSpecText(JSON.stringify(emptySpecByKind[kind.kind] ?? {}, null, 2));
      }
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
        setSpecText(JSON.stringify(loaded.spec ?? {}, null, 2));
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

  if (!kind) {
    return (
      <div className="panel">
        <div className="panel-body empty">Unknown composition kind.</div>
      </div>
    );
  }

  async function onSave() {
    if (!kind) {
      return;
    }
    setError(null);
    setInfo(null);
    let spec: Record<string, unknown>;
    try {
      spec = JSON.parse(specText) as Record<string, unknown>;
    } catch (err) {
      setError(`Invalid JSON spec: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    if (isCreate) {
      const createName = name.trim();
      if (!createName) {
        setError("Name is required");
        return;
      }
      setSaving(true);
      try {
        const created = await createComposition(token, namespace, kind.segment, {
          metadata: { name: createName },
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
          annotations: doc.metadata.annotations,
        },
        spec,
      });
      setDoc(updated);
      setSpecText(JSON.stringify(updated.spec ?? {}, null, 2));
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
          <h1 className="page-title">{isCreate ? `New ${kind.kind}` : nameParam}</h1>
          <p className="page-sub">
            Namespace <span className="mono">{namespace}</span>
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
            <h2 className="panel-title">{writable ? "Edit" : "View"}</h2>
            {!isCreate && doc ? (
              <span className="chip mono">rv {doc.metadata.resourceVersion}</span>
            ) : null}
          </div>
          <div className="panel-body editor-form">
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

            <div className="editor-actions">
              {writable ? (
                <button type="button" className="btn btn-primary" disabled={saving} onClick={() => void onSave()}>
                  {saving ? "Saving…" : isCreate ? "Create" : "Save"}
                </button>
              ) : (
                <span className="text-mute">
                  {writeEnabled
                    ? "Read-only · GitOps / non-console object"
                    : "Composition write is disabled on this API"}
                </span>
              )}
            </div>

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
