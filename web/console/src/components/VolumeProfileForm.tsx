import {
  emptyVolumeEntry,
  type VolumeProfileForm as VolumeProfileFormModel,
} from "../pages/library/volumeProfileForm";

interface Props {
  form: VolumeProfileFormModel;
  disabled?: boolean;
  isCreate: boolean;
  onChange: <K extends keyof VolumeProfileFormModel>(
    key: K,
    value: VolumeProfileFormModel[K],
  ) => void;
}

export function VolumeProfileForm({ form, disabled, isCreate, onChange }: Props) {
  function updateVolume(index: number, patch: Partial<(typeof form.volumes)[number]>) {
    const next = form.volumes.map((entry, i) => (i === index ? { ...entry, ...patch } : entry));
    onChange("volumes", next);
  }

  function removeVolume(index: number) {
    if (form.volumes.length <= 1) {
      onChange("volumes", [emptyVolumeEntry()]);
      return;
    }
    onChange(
      "volumes",
      form.volumes.filter((_, i) => i !== index),
    );
  }

  return (
    <div className="guided-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is a VolumeProfile?</h3>
        <p className="explain-body">
          A reusable <strong>durable storage shape</strong> — named volume entries with purposes,
          mount paths, sizes, and access modes. AgentDataVolumes can reference a profile for
          defaults instead of re-typing every field.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Template, not PVC.</strong> This does not create storage by itself.
          </li>
          <li>
            <strong>Multiple entries.</strong> e.g. agent-home + cargo-cache with different sizes.
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
          placeholder="standard-agent-homes"
        />
      </label>

      <label className="field">
        <span className="label">Description</span>
        <textarea
          className="textarea"
          value={form.description}
          disabled={disabled}
          onChange={(event) => onChange("description", event.target.value)}
          placeholder="When to use this storage shape"
        />
      </label>

      <h3 className="form-section-title">Volume entries</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Each entry becomes a selectable profile volume name on AgentDataVolumes.
      </p>

      <div className="form-entry-list">
        {form.volumes.map((entry, index) => (
          <div key={index} className="form-entry-card">
            <div className="form-entry-header">
              <span className="mono">#{index + 1}</span>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={disabled}
                onClick={() => removeVolume(index)}
              >
                Remove
              </button>
            </div>
            <div className="field-row">
              <label className="field">
                <span className="label">Name</span>
                <input
                  className="input mono"
                  value={entry.name}
                  disabled={disabled}
                  onChange={(event) => updateVolume(index, { name: event.target.value })}
                  placeholder="home"
                />
              </label>
              <label className="field">
                <span className="label">Purpose</span>
                <input
                  className="input mono"
                  value={entry.purpose}
                  disabled={disabled}
                  onChange={(event) => updateVolume(index, { purpose: event.target.value })}
                  placeholder="agent-home"
                />
              </label>
            </div>
            <div className="field-row">
              <label className="field">
                <span className="label">Mount path</span>
                <input
                  className="input mono"
                  value={entry.mountPath}
                  disabled={disabled}
                  onChange={(event) => updateVolume(index, { mountPath: event.target.value })}
                  placeholder="/agent-home"
                />
              </label>
              <label className="field">
                <span className="label">Size request</span>
                <input
                  className="input mono"
                  value={entry.sizeRequest}
                  disabled={disabled}
                  onChange={(event) => updateVolume(index, { sizeRequest: event.target.value })}
                  placeholder="10Gi"
                />
              </label>
            </div>
            <label className="field">
              <span className="label">Access mode</span>
              <select
                className="select"
                value={entry.accessMode}
                disabled={disabled}
                onChange={(event) => updateVolume(index, { accessMode: event.target.value })}
              >
                <option value="ReadWriteOnce">ReadWriteOnce</option>
                <option value="ReadWriteMany">ReadWriteMany</option>
                <option value="ReadOnlyMany">ReadOnlyMany</option>
              </select>
            </label>
          </div>
        ))}
      </div>

      <button
        type="button"
        className="btn btn-ghost"
        disabled={disabled}
        onClick={() => onChange("volumes", [...form.volumes, emptyVolumeEntry()])}
      >
        Add volume entry
      </button>
    </div>
  );
}
