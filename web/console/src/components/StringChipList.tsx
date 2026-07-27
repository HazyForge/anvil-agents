interface Props {
  label: string;
  help?: string;
  value: string[];
  disabled?: boolean;
  placeholder?: string;
  suggestions?: string[];
  onChange: (next: string[]) => void;
}

/** Simple ordered chip list with type-to-add and optional datalist suggestions. */
export function StringChipList({
  label,
  help,
  value,
  disabled,
  placeholder,
  suggestions = [],
  onChange,
}: Props) {
  const available = suggestions.filter((name) => !value.includes(name)).sort();

  function add(name: string) {
    const trimmed = name.trim();
    if (!trimmed || value.includes(trimmed)) {
      return;
    }
    onChange([...value, trimmed]);
  }

  function remove(name: string) {
    onChange(value.filter((item) => item !== name));
  }

  return (
    <div className="field">
      <span className="label">{label}</span>
      {help ? <p className="field-help">{help}</p> : null}
      {value.length === 0 ? (
        <div className="ordered-ref-empty">None selected.</div>
      ) : (
        <ul className="chip-edit-list">
          {value.map((name) => (
            <li key={name} className="chip-edit-item">
              <span className="mono">{name}</span>
              <button
                type="button"
                className="btn btn-ghost"
                disabled={disabled}
                onClick={() => remove(name)}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="chip-edit-add">
        {available.length > 0 ? (
          <select
            className="select"
            disabled={disabled}
            defaultValue=""
            key={value.join("|")}
            onChange={(event) => {
              if (event.target.value) {
                add(event.target.value);
              }
            }}
          >
            <option value="">Add from namespace…</option>
            {available.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        ) : null}
        <form
          className="chip-edit-manual"
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
            placeholder={placeholder ?? "type a name and press Add"}
            autoComplete="off"
            list={suggestions.length ? `${label}-suggestions` : undefined}
          />
          {suggestions.length ? (
            <datalist id={`${label}-suggestions`}>
              {suggestions.map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
          ) : null}
          <button type="submit" className="btn" disabled={disabled}>
            Add
          </button>
        </form>
      </div>
    </div>
  );
}
