import type { RunFilters } from "../utils/filters";

interface Props {
  filters: RunFilters;
  phases: string[];
  applications: string[];
  targets: string[];
  backends: string[];
  sources: string[];
  onChange: (next: RunFilters) => void;
  onReset: () => void;
}

export function FiltersBar({
  filters,
  phases,
  applications,
  targets,
  backends,
  sources,
  onChange,
  onReset,
}: Props) {
  function patch(partial: Partial<RunFilters>) {
    onChange({ ...filters, ...partial });
  }

  return (
    <div className="filters">
      <label className="field">
        <span className="label">Search</span>
        <input
          className="input"
          value={filters.search}
          placeholder="name, intent, error…"
          onChange={(e) => patch({ search: e.target.value })}
        />
      </label>
      <label className="field">
        <span className="label">Phase</span>
        <select className="select" value={filters.phase} onChange={(e) => patch({ phase: e.target.value })}>
          <option value="">All</option>
          {phases.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span className="label">Application</span>
        <select
          className="select"
          value={filters.application}
          onChange={(e) => patch({ application: e.target.value })}
        >
          <option value="">All</option>
          {applications.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span className="label">Target</span>
        <select
          className="select"
          value={filters.applicationTarget}
          onChange={(e) => patch({ applicationTarget: e.target.value })}
        >
          <option value="">All</option>
          {targets.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span className="label">Backend</span>
        <select className="select" value={filters.backend} onChange={(e) => patch({ backend: e.target.value })}>
          <option value="">All</option>
          {backends.map((b) => (
            <option key={b} value={b}>
              {b}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span className="label">Source</span>
        <select className="select" value={filters.source} onChange={(e) => patch({ source: e.target.value })}>
          <option value="">All</option>
          {sources.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </label>
      <div className="filters-checks">
        <label className="check">
          <input
            type="checkbox"
            checked={filters.onlyRunning}
            onChange={(e) =>
              patch({
                onlyRunning: e.target.checked,
                // Mutual exclusion: a run cannot be both Running and Failed.
                onlyFailed: e.target.checked ? false : filters.onlyFailed,
              })
            }
          />
          Only running
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={filters.onlyFailed}
            onChange={(e) =>
              patch({
                onlyFailed: e.target.checked,
                onlyRunning: e.target.checked ? false : filters.onlyRunning,
              })
            }
          />
          Only failed
        </label>
        <button type="button" className="btn btn-ghost" onClick={onReset}>
          Reset filters
        </button>
      </div>
    </div>
  );
}
