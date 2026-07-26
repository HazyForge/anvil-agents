interface Props {
  label: string;
  help?: string;
  value: string[];
  options: string[];
  disabled?: boolean;
  placeholder?: string;
  onChange: (next: string[]) => void;
}

/** Ordered multi-select for composition refs (skill sets, tool sets, …). */
export function OrderedRefList({
  label,
  help,
  value,
  options,
  disabled,
  placeholder,
  onChange,
}: Props) {
  const available = options.filter((name) => !value.includes(name)).sort();

  function add(name: string) {
    const trimmed = name.trim();
    if (!trimmed || value.includes(trimmed)) {
      return;
    }
    onChange([...value, trimmed]);
  }

  function remove(index: number) {
    onChange(value.filter((_, i) => i !== index));
  }

  function move(index: number, delta: number) {
    const next = index + delta;
    if (next < 0 || next >= value.length) {
      return;
    }
    const copy = [...value];
    const [item] = copy.splice(index, 1);
    copy.splice(next, 0, item);
    onChange(copy);
  }

  return (
    <div className="field ordered-ref-list">
      <span className="label">{label}</span>
      {help ? <p className="field-help">{help}</p> : null}

      {value.length === 0 ? (
        <div className="ordered-ref-empty">None selected — add one or more below.</div>
      ) : (
        <ol className="ordered-ref-items">
          {value.map((name, index) => (
            <li key={`${name}-${index}`} className="ordered-ref-item">
              <span className="ordered-ref-index mono">{index + 1}</span>
              <span className="ordered-ref-name mono">{name}</span>
              <span className="ordered-ref-actions">
                <button
                  type="button"
                  className="btn btn-ghost"
                  disabled={disabled || index === 0}
                  onClick={() => move(index, -1)}
                  title="Move up"
                >
                  ↑
                </button>
                <button
                  type="button"
                  className="btn btn-ghost"
                  disabled={disabled || index === value.length - 1}
                  onClick={() => move(index, 1)}
                  title="Move down"
                >
                  ↓
                </button>
                <button
                  type="button"
                  className="btn btn-danger"
                  disabled={disabled}
                  onClick={() => remove(index)}
                  title="Remove"
                >
                  Remove
                </button>
              </span>
            </li>
          ))}
        </ol>
      )}

      <div className="ordered-ref-add">
        <select
          className="select"
          disabled={disabled || available.length === 0}
          defaultValue=""
          key={value.join("|")}
          onChange={(event) => {
            const name = event.target.value;
            if (name) {
              add(name);
            }
          }}
        >
          <option value="">
            {available.length === 0 ? "All known sets already added" : "Add from namespace…"}
          </option>
          {available.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
        <form
          className="ordered-ref-manual"
          onSubmit={(event) => {
            event.preventDefault();
            const form = event.currentTarget;
            const input = form.elements.namedItem("manual") as HTMLInputElement | null;
            if (input?.value) {
              add(input.value);
              input.value = "";
            }
          }}
        >
          <input
            className="input mono"
            name="manual"
            disabled={disabled}
            placeholder={placeholder ?? "or type a name and press Add"}
            autoComplete="off"
          />
          <button type="submit" className="btn" disabled={disabled}>
            Add
          </button>
        </form>
      </div>
    </div>
  );
}
