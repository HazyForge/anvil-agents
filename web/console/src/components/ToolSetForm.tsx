import {
  emptyToolEntry,
  type ToolSetForm as ToolSetFormModel,
} from "../pages/library/toolSetForm";

interface Props {
  form: ToolSetFormModel;
  disabled?: boolean;
  isCreate: boolean;
  onChange: <K extends keyof ToolSetFormModel>(key: K, value: ToolSetFormModel[K]) => void;
}

export function ToolSetForm({ form, disabled, isCreate, onChange }: Props) {
  function updateTool(index: number, patch: Partial<(typeof form.tools)[number]>) {
    const next = form.tools.map((entry, i) => (i === index ? { ...entry, ...patch } : entry));
    onChange("tools", next);
  }

  function removeTool(index: number) {
    if (form.tools.length <= 1) {
      onChange("tools", [emptyToolEntry()]);
      return;
    }
    onChange(
      "tools",
      form.tools.filter((_, i) => i !== index),
    );
  }

  return (
    <div className="guided-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is an AgentToolSet?</h3>
        <p className="explain-body">
          Setup scripts and verify contracts for tools the agent can run. This is{" "}
          <strong>code-execution authority</strong> — keep scripts pinned and credentials in harness
          secret refs, never inline.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Compose on run profiles</strong> as ordered tool-set refs.
          </li>
          <li>
            <strong>Verify command</strong> is argv (space-separated in this form), e.g.{" "}
            <span className="mono">kbctl --version</span>.
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
          placeholder="kb-tools"
        />
      </label>

      <label className="field">
        <span className="label">Description</span>
        <textarea
          className="textarea"
          value={form.description}
          disabled={disabled}
          onChange={(event) => onChange("description", event.target.value)}
          placeholder="What integration this tool set exposes"
        />
      </label>

      <h3 className="form-section-title">Tools</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Empty tool names are ignored on save. Prefer GitOps for shared production tool sets.
      </p>

      <div className="form-entry-list">
        {form.tools.map((tool, index) => (
          <div key={index} className="form-entry-card">
            <div className="form-entry-header">
              <span className="mono">tool #{index + 1}</span>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={disabled}
                onClick={() => removeTool(index)}
              >
                Remove
              </button>
            </div>
            <div className="field-row">
              <label className="field">
                <span className="label">Name</span>
                <input
                  className="input mono"
                  value={tool.name}
                  disabled={disabled}
                  onChange={(event) => updateTool(index, { name: event.target.value })}
                  placeholder="kbctl"
                />
              </label>
              <label className="field">
                <span className="label">Description</span>
                <input
                  className="input"
                  value={tool.description}
                  disabled={disabled}
                  onChange={(event) => updateTool(index, { description: event.target.value })}
                  placeholder="what this tool does"
                />
              </label>
            </div>
            <label className="field">
              <span className="label">Setup script (shell)</span>
              <textarea
                className="textarea textarea-spec"
                value={tool.setupScript}
                disabled={disabled}
                onChange={(event) => updateTool(index, { setupScript: event.target.value })}
                placeholder="#!/usr/bin/env bash&#10;set -euo pipefail&#10;…"
                spellCheck={false}
              />
            </label>
            <label className="field">
              <span className="label">Verify command (space-separated argv)</span>
              <input
                className="input mono"
                value={tool.verifyCommand}
                disabled={disabled}
                onChange={(event) => updateTool(index, { verifyCommand: event.target.value })}
                placeholder="kbctl --version"
              />
            </label>
          </div>
        ))}
      </div>

      <button
        type="button"
        className="btn btn-ghost"
        disabled={disabled}
        onClick={() => onChange("tools", [...form.tools, emptyToolEntry()])}
      >
        Add tool
      </button>
    </div>
  );
}
