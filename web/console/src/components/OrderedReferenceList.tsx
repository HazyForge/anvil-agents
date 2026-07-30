interface Props { label: string; help?: string; value: string[]; suggestions?: string[]; disabled?: boolean; onChange: (value: string[]) => void; }

export function OrderedReferenceList({ label, help, value, suggestions = [], disabled, onChange }: Props) {
  function move(index: number, delta: number) { const next = [...value]; const target = index + delta; if (target < 0 || target >= next.length) return; [next[index], next[target]] = [next[target], next[index]]; onChange(next); }
  function add(name: string) { const clean = name.trim(); if (clean && !value.includes(clean)) onChange([...value, clean]); }
  return <div className="field"><span className="label">{label}</span>{help ? <p className="field-help">{help}</p> : null}
    <ol className="ordered-ref-list">{value.map((name, index) => <li key={name}><span className="mono">{index + 1}. {name}</span><span className="ordered-ref-actions"><button type="button" className="btn btn-ghost" disabled={disabled || index === 0} onClick={() => move(index, -1)}>↑</button><button type="button" className="btn btn-ghost" disabled={disabled || index === value.length - 1} onClick={() => move(index, 1)}>↓</button><button type="button" className="btn btn-ghost" disabled={disabled} onClick={() => onChange(value.filter((_, i) => i !== index))}>Remove</button></span></li>)}</ol>
    <select className="select" disabled={disabled} value="" onChange={(event) => add(event.target.value)}><option value="">Add a resource…</option>{suggestions.filter((name) => !value.includes(name)).map((name) => <option key={name} value={name}>{name}</option>)}</select>
  </div>;
}
