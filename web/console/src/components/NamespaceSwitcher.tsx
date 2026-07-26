import { useState, type FormEvent } from "react";

interface Props {
  namespaces: string[];
  active: string;
  onSelect: (namespace: string) => void;
  onAdd: (namespace: string) => void;
  onRemove: (namespace: string) => void;
}

export function NamespaceSwitcher({ namespaces, active, onSelect, onAdd, onRemove }: Props) {
  const [draft, setDraft] = useState("");

  function handleAdd(event: FormEvent) {
    event.preventDefault();
    const value = draft.trim();
    if (!value) {
      return;
    }
    onAdd(value);
    setDraft("");
  }

  return (
    <div className="namespace-bar" aria-label="Namespaces">
      <span className="label" style={{ margin: 0 }}>
        NS
      </span>
      {namespaces.map((ns) => (
        <span key={ns} className={`chip ${ns === active ? "active" : ""}`}>
          <button type="button" className="btn btn-ghost" style={{ padding: 0 }} onClick={() => onSelect(ns)}>
            {ns}
          </button>
          {namespaces.length > 1 ? (
            <button type="button" title={`Remove ${ns}`} onClick={() => onRemove(ns)} aria-label={`Remove ${ns}`}>
              ×
            </button>
          ) : null}
        </span>
      ))}
      <form onSubmit={handleAdd} className="toolbar-inline">
        <input
          className="input"
          style={{ width: "10rem" }}
          placeholder="add namespace"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          aria-label="Add namespace"
        />
        <button type="submit" className="btn">
          Add
        </button>
      </form>
    </div>
  );
}
