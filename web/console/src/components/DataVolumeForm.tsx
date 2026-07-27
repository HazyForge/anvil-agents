import {
  DATA_VOLUME_BACKENDS,
  applyBackendDefaults,
  type DataVolumeBackend,
  type DataVolumeForm as DataVolumeFormModel,
} from "../pages/library/dataVolumeForm";

interface Props {
  form: DataVolumeFormModel;
  disabled?: boolean;
  isCreate: boolean;
  volumeProfileNames?: string[];
  onChange: <K extends keyof DataVolumeFormModel>(
    key: K,
    value: DataVolumeFormModel[K],
  ) => void;
  onReplace?: (next: DataVolumeFormModel) => void;
}

export function DataVolumeForm({
  form,
  disabled,
  isCreate,
  volumeProfileNames = [],
  onChange,
  onReplace,
}: Props) {
  function selectBackend(backend: DataVolumeBackend) {
    if (disabled) {
      return;
    }
    if (onReplace) {
      onReplace(applyBackendDefaults(form, backend));
      return;
    }
    onChange("backend", backend);
  }

  return (
    <div className="guided-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is an AgentDataVolume?</h3>
        <p className="explain-body">
          A concrete <strong>PVC-backed durable home</strong> for sessions, OAuth tokens, and caches.
          Harness profiles attach these by name; auth sessions reauth against them.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Not a profile.</strong> Volume profiles are reusable shapes; this object owns a
            real claim in the namespace.
          </li>
          <li>
            <strong>Backend hint.</strong> Documents which adapter should treat this path as home
            (Codex, Grok Build, …). Controller does not hard-require a match.
          </li>
          <li>
            <strong>Auth sessions.</strong> Create reauth/logout with{" "}
            <span className="mono">anvil-agentctl auth</span> targeting this volume.
          </li>
        </ul>
      </section>

      <label className="field">
        <span className="label">Name</span>
        <input
          className="input mono"
          value={form.name}
          disabled={!isCreate || disabled}
          onChange={(event) => onChange("name", event.target.value)}
          placeholder="auditor-grok-home"
        />
      </label>

      <label className="field">
        <span className="label">Notes / description</span>
        <textarea
          className="textarea"
          value={form.notes || form.description}
          disabled={disabled}
          onChange={(event) => {
            onChange("notes", event.target.value);
            onChange("description", event.target.value);
          }}
          placeholder="What agent / backend this durable home is for"
        />
      </label>

      <div className="field">
        <span className="label">Backend (intended home)</span>
        <p className="field-help">
          Sets recommended mount path and home env. Pick the adapter that will use this PVC as its
          durable state root.
        </p>
        <div className="backend-kind-grid">
          {DATA_VOLUME_BACKENDS.map((opt) => {
            const selected = form.backend === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                className={["backend-kind-card", selected ? "backend-kind-card-selected" : ""]
                  .filter(Boolean)
                  .join(" ")}
                disabled={disabled}
                onClick={() => selectBackend(opt.value)}
              >
                <span className="backend-kind-title">{opt.label}</span>
                <span className="backend-kind-summary">{opt.summary}</span>
                <span className="mono backend-kind-id">{opt.value}</span>
              </button>
            );
          })}
        </div>
      </div>

      <h3 className="form-section-title">Identity & scope</h3>
      <div className="field-row">
        <label className="field">
          <span className="label">Agent name</span>
          <input
            className="input mono"
            value={form.agentName}
            disabled={disabled}
            onChange={(event) => onChange("agentName", event.target.value)}
            placeholder="logical agent id"
          />
        </label>
        <label className="field">
          <span className="label">Application name (optional)</span>
          <input
            className="input mono"
            value={form.applicationName}
            disabled={disabled}
            onChange={(event) => onChange("applicationName", event.target.value)}
            placeholder="opaque application key"
          />
        </label>
      </div>

      <h3 className="form-section-title">Storage</h3>
      <div className="field-row">
        <label className="field">
          <span className="label">Volume profile (optional)</span>
          <input
            className="input mono"
            list="volume-profile-names"
            value={form.profileName}
            disabled={disabled}
            onChange={(event) => onChange("profileName", event.target.value)}
            placeholder="reuse a VolumeProfile"
          />
          {volumeProfileNames.length > 0 ? (
            <datalist id="volume-profile-names">
              {volumeProfileNames.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          ) : null}
        </label>
        <label className="field">
          <span className="label">Profile volume name</span>
          <input
            className="input mono"
            value={form.profileVolumeName}
            disabled={disabled}
            onChange={(event) => onChange("profileVolumeName", event.target.value)}
            placeholder="entry name in the profile"
          />
        </label>
      </div>

      <div className="field-row">
        <label className="field">
          <span className="label">Mount path</span>
          <input
            className="input mono"
            value={form.mountPath}
            disabled={disabled}
            onChange={(event) => onChange("mountPath", event.target.value)}
            placeholder="/opt/anvil/grok-build"
          />
        </label>
        <label className="field">
          <span className="label">Size</span>
          <input
            className="input mono"
            value={form.size}
            disabled={disabled}
            onChange={(event) => onChange("size", event.target.value)}
            placeholder="10Gi"
          />
        </label>
      </div>

      <div className="field-row">
        <label className="field">
          <span className="label">Storage class (optional)</span>
          <input
            className="input mono"
            value={form.storageClassName}
            disabled={disabled}
            onChange={(event) => onChange("storageClassName", event.target.value)}
            placeholder="cluster default if empty"
          />
        </label>
        <label className="field">
          <span className="label">Access mode</span>
          <select
            className="select"
            value={form.accessMode}
            disabled={disabled}
            onChange={(event) => onChange("accessMode", event.target.value)}
          >
            <option value="ReadWriteOnce">ReadWriteOnce</option>
            <option value="ReadWriteMany">ReadWriteMany</option>
            <option value="ReadOnlyMany">ReadOnlyMany</option>
          </select>
        </label>
      </div>

      <label className="field">
        <span className="label">Claim name (optional, immutable after create)</span>
        <input
          className="input mono"
          value={form.claimName}
          disabled={disabled || !isCreate}
          onChange={(event) => onChange("claimName", event.target.value)}
          placeholder="defaults to agent-data-&lt;name&gt;"
        />
        <p className="field-help">Leave empty to let the controller name the PVC.</p>
      </label>

      <h3 className="form-section-title">Home env (path-only)</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Projects a named absolute path into the agent container (not secrets). Typical:{" "}
        <span className="mono">GROK_BUILD_HOME</span> or <span className="mono">CODEX_HOME</span>{" "}
        equal to the mount path.
      </p>
      <div className="field-row">
        <label className="field">
          <span className="label">Env name</span>
          <input
            className="input mono"
            value={form.homeEnvName}
            disabled={disabled}
            onChange={(event) => onChange("homeEnvName", event.target.value)}
            placeholder="GROK_BUILD_HOME"
          />
        </label>
        <label className="field">
          <span className="label">Env value (absolute path)</span>
          <input
            className="input mono"
            value={form.homeEnvValue}
            disabled={disabled}
            onChange={(event) => onChange("homeEnvValue", event.target.value)}
            placeholder="/opt/anvil/grok-build"
          />
        </label>
      </div>
    </div>
  );
}
