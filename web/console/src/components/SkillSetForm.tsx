import {
  emptySkillEntry,
  type SkillSetForm as SkillSetFormModel,
} from "../pages/library/skillSetForm";

interface Props {
  form: SkillSetFormModel;
  disabled?: boolean;
  isCreate: boolean;
  onChange: <K extends keyof SkillSetFormModel>(key: K, value: SkillSetFormModel[K]) => void;
}

export function SkillSetForm({ form, disabled, isCreate, onChange }: Props) {
  function updateSkill(index: number, patch: Partial<(typeof form.skills)[number]>) {
    const next = form.skills.map((entry, i) => (i === index ? { ...entry, ...patch } : entry));
    onChange("skills", next);
  }

  function removeSkill(index: number) {
    if (form.skills.length <= 1) {
      onChange("skills", [emptySkillEntry()]);
      return;
    }
    onChange(
      "skills",
      form.skills.filter((_, i) => i !== index),
    );
  }

  return (
    <div className="guided-form">
      <section className="explain-panel">
        <h3 className="explain-title">What is an AgentSkillSet?</h3>
        <p className="explain-body">
          Backend-neutral <strong>instruction packs and personas</strong>. Skills teach agents how
          to think and when to use tools — not images, Secrets, or ServiceAccounts.
        </p>
        <ul className="explain-list">
          <li>
            <strong>Compose on run profiles</strong> as ordered skill-set refs.
          </li>
          <li>
            <strong>Content</strong> is Markdown injected into the harness prompt layers.
          </li>
          <li>
            Prefer <span className="mono">AgentToolSet</span> for setup scripts and verify contracts.
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
          placeholder="pr-review-skills"
        />
      </label>

      <label className="field">
        <span className="label">Description</span>
        <textarea
          className="textarea"
          value={form.description}
          disabled={disabled}
          onChange={(event) => onChange("description", event.target.value)}
          placeholder="When to select this capability pack"
        />
      </label>

      <h3 className="form-section-title">Skills</h3>
      <p className="field-help" style={{ marginTop: 0 }}>
        Named instruction packs. Empty skill names are ignored on save.
      </p>

      <div className="form-entry-list">
        {form.skills.map((skill, index) => (
          <div key={index} className="form-entry-card">
            <div className="form-entry-header">
              <span className="mono">skill #{index + 1}</span>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={disabled}
                onClick={() => removeSkill(index)}
              >
                Remove
              </button>
            </div>
            <div className="field-row">
              <label className="field">
                <span className="label">Name</span>
                <input
                  className="input mono"
                  value={skill.name}
                  disabled={disabled}
                  onChange={(event) => updateSkill(index, { name: event.target.value })}
                  placeholder="code-review"
                />
              </label>
              <label className="field">
                <span className="label">Description</span>
                <input
                  className="input"
                  value={skill.description}
                  disabled={disabled}
                  onChange={(event) => updateSkill(index, { description: event.target.value })}
                  placeholder="when to apply"
                />
              </label>
            </div>
            <label className="field">
              <span className="label">Content (Markdown)</span>
              <textarea
                className="textarea textarea-spec"
                value={skill.content}
                disabled={disabled}
                onChange={(event) => updateSkill(index, { content: event.target.value })}
                placeholder="## Review checklist&#10;- …"
                spellCheck={false}
              />
            </label>
          </div>
        ))}
      </div>

      <button
        type="button"
        className="btn btn-ghost"
        disabled={disabled}
        onClick={() => onChange("skills", [...form.skills, emptySkillEntry()])}
      >
        Add skill
      </button>
    </div>
  );
}
