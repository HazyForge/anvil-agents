import type { CompositionDocument } from "../api/types.composition";
import { getIconUrl, resolveIconSrc } from "../utils/icons";

export interface CompositionOption {
  name: string;
  description?: string;
  meta?: string;
  managedBy?: string;
  danger?: boolean;
  icon?: string;
  /** Namespace-global skill/tool set (spec.global). */
  global?: boolean;
}

interface BaseProps {
  label: string;
  help?: string;
  options: CompositionOption[];
  disabled?: boolean;
  emptyLabel?: string;
}

interface MultiProps extends BaseProps {
  mode: "multi";
  value: string[];
  onChange: (next: string[]) => void;
}

interface SingleProps extends BaseProps {
  mode: "single";
  value: string;
  onChange: (next: string) => void;
  allowClear?: boolean;
}

type Props = MultiProps | SingleProps;

function optionFromDoc(doc: CompositionDocument, meta?: string): CompositionOption {
  const isGlobal =
    (doc.kind === "AgentSkillSet" || doc.kind === "AgentToolSet") && Boolean(doc.spec?.global);
  return {
    name: doc.metadata.name,
    description: String(doc.spec?.description ?? "").trim() || undefined,
    meta,
    managedBy: doc.management.managedBy,
    danger: doc.management.reason === "gitops_protected" ? false : undefined,
    icon: getIconUrl(doc.metadata.annotations) || undefined,
    global: isGlobal || undefined,
  };
}

export function optionsFromDocs(
  docs: CompositionDocument[],
  metaFn?: (doc: CompositionDocument) => string | undefined,
): CompositionOption[] {
  return docs
    .map((doc) => optionFromDoc(doc, metaFn?.(doc)))
    .sort((a, b) => a.name.localeCompare(b.name));
}

/** Pick composition components as cards (single or ordered multi-select). */
export function CompositionCardPicker(props: Props) {
  const { label, help, options, disabled, emptyLabel, mode } = props;
  const selected = mode === "multi" ? props.value : props.value ? [props.value] : [];
  const selectedSet = new Set(selected);

  // Selected first (in order), then remaining available.
  const orderedOptions: CompositionOption[] = [];
  for (const name of selected) {
    const found = options.find((opt) => opt.name === name);
    orderedOptions.push(found ?? { name });
  }
  for (const opt of options) {
    if (!selectedSet.has(opt.name)) {
      orderedOptions.push(opt);
    }
  }
  // Include unknown selected names already handled; also free-type orphans only once.

  function toggleMulti(name: string) {
    if (mode !== "multi" || disabled) {
      return;
    }
    if (props.value.includes(name)) {
      props.onChange(props.value.filter((item) => item !== name));
      return;
    }
    props.onChange([...props.value, name]);
  }

  function toggleSingle(name: string) {
    if (mode !== "single" || disabled) {
      return;
    }
    if (props.value === name && props.allowClear !== false) {
      props.onChange("");
      return;
    }
    props.onChange(name);
  }

  function moveSelected(name: string, delta: number) {
    if (mode !== "multi" || disabled) {
      return;
    }
    const index = props.value.indexOf(name);
    if (index < 0) {
      return;
    }
    const next = index + delta;
    if (next < 0 || next >= props.value.length) {
      return;
    }
    const copy = [...props.value];
    const [item] = copy.splice(index, 1);
    copy.splice(next, 0, item);
    props.onChange(copy);
  }

  return (
    <div className="field composition-picker">
      <span className="label">{label}</span>
      {help ? <p className="field-help">{help}</p> : null}

      {mode === "multi" && selected.length > 0 ? (
        <div className="composition-selected-bar">
          <span className="label">Selected order</span>
          <ol className="composition-selected-order">
            {selected.map((name, index) => {
              const opt = options.find((item) => item.name === name);
              return (
                <li key={name} className="composition-selected-chip">
                  <span className="mono">
                    {index + 1}. {name}
                  </span>
                  {opt?.global ? (
                    <span className="chip chip-global" title="Also auto-attached as namespace global">
                      global
                    </span>
                  ) : null}
                  <span className="ordered-ref-actions">
                    <button
                      type="button"
                      className="btn btn-ghost"
                      disabled={disabled || index === 0}
                      onClick={() => moveSelected(name, -1)}
                      title="Move earlier"
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost"
                      disabled={disabled || index === selected.length - 1}
                      onClick={() => moveSelected(name, 1)}
                      title="Move later"
                    >
                      ↓
                    </button>
                  </span>
                </li>
              );
            })}
          </ol>
        </div>
      ) : null}

      {orderedOptions.length === 0 ? (
        <div className="ordered-ref-empty">
          {emptyLabel ?? "No components available in this namespace."}
        </div>
      ) : (
        <div className="composition-pick-grid">
          {orderedOptions.map((opt) => {
            const isSelected = selectedSet.has(opt.name);
            const order = isSelected ? selected.indexOf(opt.name) + 1 : 0;
            const iconSrc = resolveIconSrc(opt.icon);
            return (
              <button
                key={opt.name}
                type="button"
                className={[
                  "compose-pick-card",
                  isSelected ? "compose-pick-card-selected" : "",
                  opt.danger ? "compose-pick-card-danger" : "",
                  disabled ? "compose-pick-card-disabled" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                disabled={disabled}
                onClick={() => (mode === "multi" ? toggleMulti(opt.name) : toggleSingle(opt.name))}
              >
                <div className="compose-pick-top">
                  {isSelected ? (
                    <span className="compose-pick-badge">{mode === "multi" ? order : "✓"}</span>
                  ) : (
                    <span className="compose-pick-badge compose-pick-badge-off">+</span>
                  )}
                  {iconSrc ? <img src={iconSrc} alt="" className="compose-pick-icon" /> : null}
                  <span className="compose-pick-name mono">{opt.name}</span>
                  {opt.global ? (
                    <span
                      className="chip chip-global"
                      title="Auto-attached to every AgentRun in this namespace"
                    >
                      global
                    </span>
                  ) : null}
                </div>
                {opt.description ? (
                  <p className="compose-pick-desc">{opt.description}</p>
                ) : (
                  <p className="compose-pick-desc compose-pick-desc-mute">No description</p>
                )}
                <div className="compose-pick-meta">
                  {opt.meta ? <span>{opt.meta}</span> : null}
                  {opt.global ? (
                    <span className="compose-pick-global-note">namespace default</span>
                  ) : null}
                  {opt.managedBy ? (
                    <span className="chip chip-mute" style={{ marginLeft: "auto" }}>
                      {opt.managedBy}
                    </span>
                  ) : null}
                </div>
              </button>
            );
          })}
        </div>
      )}

      {mode === "multi" ? (
        <p className="field-help" style={{ marginTop: "0.5rem" }}>
          Click a card to add or remove it. Use ↑/↓ on the selected list to set application order.
        </p>
      ) : (
        <p className="field-help" style={{ marginTop: "0.5rem" }}>
          Click a card to select it{props.mode === "single" && props.allowClear !== false ? "; click again to clear" : ""}.
        </p>
      )}
    </div>
  );
}
