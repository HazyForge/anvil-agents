import { useCallback, useEffect, useRef, useState } from "react";
import { listControls, updateControl } from "../api/controls";
import { APIError } from "../api/client";
import type { ControlView } from "../api/types";
import { Icon } from "../components/Icon";

interface Props {
  token: string;
  writeEnabled: boolean;
}

const POLL_MS = 15_000;
const DURATIONS = [
  { label: "1 hour", value: "1h" },
  { label: "4 hours", value: "4h" },
  { label: "12 hours", value: "12h" },
  { label: "24 hours", value: "24h" },
  { label: "48 hours", value: "48h" },
  { label: "Indefinite", value: "" },
];

function expiryFor(duration: string, from: Date): string {
  if (!duration) {
    return "";
  }
  const match = duration.match(/^(\d+)h$/);
  if (!match) {
    return "";
  }
  const hours = Number(match[1]);
  return new Date(from.getTime() + hours * 60 * 60 * 1000).toISOString();
}

function PolicyBadge({ policy }: { policy: string }) {
  const value = policy?.trim() || "Unknown";
  const cls = `phase phase-${value.toLowerCase().replace(/[^a-z0-9]+/g, "") || "unknown"}`;
  return <span className={cls}>{value}</span>;
}

interface MutateFormProps {
  control: ControlView;
  action: "pause" | "resume";
  token: string;
  onDone: (updated: ControlView) => void;
  onError: (message: string) => void;
}

function MutateForm({ control, action, token, onDone, onError }: MutateFormProps) {
  const [reason, setReason] = useState("");
  const [duration, setDuration] = useState("4h");
  const [sourceName, setSourceName] = useState("");
  const [sourceURL, setSourceURL] = useState("");
  const [sourceActor, setSourceActor] = useState("");
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const submit = useCallback(
    async (event: React.FormEvent) => {
      event.preventDefault();
      const trimmed = reason.trim();
      if (!trimmed) {
        setLocalError(`${action === "pause" ? "Pausing" : "Resuming"} requires a reason.`);
        return;
      }
      setBusy(true);
      setLocalError(null);
      try {
        const updated = await updateControl(token, control.name, {
          launchPolicy: action === "pause" ? "Paused" : "Allowed",
          reason: trimmed,
          expiresAt:
            action === "pause" ? expiryFor(duration, new Date()) : undefined,
          sourceName: sourceName.trim() || undefined,
          sourceUrl: sourceURL.trim() || undefined,
          sourceActor: sourceActor.trim() || undefined,
          resourceVersion: control.resourceVersion,
        });
        onDone(updated);
      } catch (err) {
        const message =
          err instanceof APIError ? `${err.code}: ${err.message}` : err instanceof Error ? err.message : String(err);
        setLocalError(message);
        onError(message);
      } finally {
        setBusy(false);
      }
    },
    [action, control, duration, onDone, onError, reason, sourceActor, sourceName, sourceURL, token],
  );

  return (
    <form className={`control-form control-form-${action}`} onSubmit={submit}>
      {localError ? <div className="banner banner-error">{localError}</div> : null}
      <label className="field">
        <span className="label">Reason</span>
        <textarea
          className="textarea"
          rows={2}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder={`Why ${action === "pause" ? "pause" : "resume"} ${control.application}?`}
        />
      </label>
      {action === "pause" ? (
        <label className="field">
          <span className="label">Duration</span>
          <select
            className="select"
            value={duration}
            onChange={(event) => setDuration(event.target.value)}
          >
            {DURATIONS.map((option) => (
              <option key={option.label} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      <details className="control-source">
        <summary>Source metadata (optional)</summary>
        <div className="control-source-fields">
        <label className="field">
          <span className="label">Source name / event id</span>
          <input
            className="input"
            value={sourceName}
            onChange={(event) => setSourceName(event.target.value)}
            placeholder="e.g. MDExOlJlZExhYmVsZWRFdmVudDEyMw=="
          />
        </label>
        <label className="field">
          <span className="label">Source URL</span>
          <input
            className="input"
            value={sourceURL}
            onChange={(event) => setSourceURL(event.target.value)}
            placeholder="https://github.com/…/pull/123"
          />
        </label>
        <label className="field">
          <span className="label">Source actor</span>
          <input
            className="input"
            value={sourceActor}
            onChange={(event) => setSourceActor(event.target.value)}
            placeholder="octocat"
          />
        </label>
        </div>
      </details>
      <div className="chip-row">
        <button type="submit" className={action === "pause" ? "btn btn-warn" : "btn btn-primary"} disabled={busy}>
          {busy ? "Saving…" : action === "pause" ? "Pause launches" : "Resume launches"}
        </button>
      </div>
    </form>
  );
}

export function ControlsPage({ token, writeEnabled }: Props) {
  const [controls, setControls] = useState<ControlView[]>([]);
  const [initialLoading, setInitialLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [refreshedAt, setRefreshedAt] = useState("");
  const [expanded, setExpanded] = useState<Record<string, "pause" | "resume">>({});
  const requestIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async (mode: "initial" | "refresh" = "refresh") => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const requestId = ++requestIdRef.current;
    if (mode === "initial") {
      setInitialLoading(true);
    } else {
      setRefreshing(true);
    }
    setError(null);
    try {
      const items = await listControls(token, controller.signal);
      if (requestId !== requestIdRef.current) {
        return;
      }
      setControls(items);
      setRefreshedAt(new Date().toISOString());
    } catch (err) {
      if (controller.signal.aborted || requestId !== requestIdRef.current) {
        return;
      }
      if (err instanceof APIError) {
        setError(`${err.code}: ${err.message}`);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      if (requestId === requestIdRef.current) {
        setInitialLoading(false);
        setRefreshing(false);
      }
    }
  }, [token]);

  useEffect(() => {
    void load("initial");
    const id = window.setInterval(() => void load("refresh"), POLL_MS);
    return () => {
      window.clearInterval(id);
      abortRef.current?.abort();
      requestIdRef.current += 1;
    };
  }, [load]);

  const applyUpdate = useCallback((updated: ControlView) => {
    setControls((prev) => prev.map((item) => (item.name === updated.name ? updated : item)));
    setExpanded((prev) => {
      const next = { ...prev };
      delete next[updated.name];
      return next;
    });
  }, []);

  const toggle = useCallback((name: string, action: "pause" | "resume") => {
    setExpanded((prev) => (prev[name] === action ? {} : { [name]: action }));
  }, []);

  const pausedCount = controls.filter((control) => control.launchPolicy === "Paused").length;
  const activeRuns = controls.reduce((sum, control) => sum + (control.activeRuns ?? 0), 0);
  const pendingRuns = controls.reduce((sum, control) => sum + (control.pendingRuns ?? 0), 0);
  const schedules = controls.reduce((sum, control) => sum + (control.affectedSchedules ?? 0), 0);

  return (
    <div className="controls-page">
      <header className="page-header">
        <div>
          <span className="page-kicker kicker-id">AgentRunControl · cluster scope</span>
          <h1 className="page-title">Launch controls</h1>
        </div>
        <div className="page-actions">
          <span className="count-pill">{controls.length} gates</span>
          <span className={`count-pill${pausedCount > 0 ? " count-pill-warn" : ""}`}>{pausedCount} paused</span>
          {refreshedAt ? (
            <span className="count-pill">refreshed {new Date(refreshedAt).toLocaleTimeString()}</span>
          ) : null}
          <button
            type="button"
            className="btn"
            onClick={() => void load(controls.length === 0 ? "initial" : "refresh")}
            disabled={initialLoading || refreshing}
          >
            <Icon name="refresh" className={refreshing || initialLoading ? "spin" : undefined} />
            {initialLoading ? "Loading…" : refreshing ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </header>

      {controls.length > 0 ? (
        <section className="stat-grid controls-stats" aria-label="Launch gate summary">
          <div className={`stat stat-hero plate${pausedCount > 0 ? " warn" : " live"}`}>
            <div className="stat-head">
              <span className="cap">Gates open</span>
              <span className={`led${pausedCount > 0 ? " led-warn" : ""}`} aria-hidden="true" />
            </div>
            <div className="stat-value">
              {controls.length - pausedCount}
              <small>/{controls.length}</small>
            </div>
            <div className="meter" aria-hidden="true">
              {controls.map((control) => (
                <i key={control.name} className={control.launchPolicy === "Paused" ? "warn" : "on"} />
              ))}
            </div>
          </div>
          <div className={`stat plate tone-warn${pausedCount > 0 ? " on warn" : ""}`}>
            <span className="cap">Paused</span>
            <div className="stat-value">{pausedCount}</div>
          </div>
          <div className={`stat plate tone-running${activeRuns > 0 ? " on" : ""}`}>
            <span className="cap">Active runs</span>
            <div className="stat-value">{activeRuns}</div>
          </div>
          <div className={`stat plate tone-pending${pendingRuns > 0 ? " on" : ""}`}>
            <span className="cap">Pending runs</span>
            <div className="stat-value">{pendingRuns}</div>
          </div>
          <div className="stat plate">
            <span className="cap">Schedules</span>
            <div className="stat-value">{schedules}</div>
          </div>
        </section>
      ) : null}

      <div className="banner banner-info">
        A paused launch gate blocks new AgentRuns for that application; it never
        terminates an already-running Job. A bounded pause recovers automatically
        at its expiry. GitOps-owned gates stay locked — change those in the Git
        source of truth.
      </div>
      {error ? <div className="banner banner-error">{error}</div> : null}
      {!writeEnabled ? (
        <div className="banner banner-warn">
          Launch gate writes are disabled on this API (controls.writeEnabled=false). Read-only view.
        </div>
      ) : null}

      {initialLoading && controls.length === 0 ? (
        <div className="empty empty-loading">Loading launch gates…</div>
      ) : controls.length === 0 ? (
        <div className="empty-state plate">
          <Icon name="controls" />
          <p className="empty">No launch gates found for your authorized applications.</p>
        </div>
      ) : (
        <section className="panel gate-panel">
          <div className="panel-header">
            <h2 className="panel-title">Gates</h2>
          </div>
          <div className="table-wrap">
            <table className="runs run-table gate-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Application</th>
                  <th>Policy</th>
                  <th>Phase</th>
                  <th>Reason</th>
                  <th>Expires</th>
                  <th>Sched</th>
                  <th>Active</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {controls.map((control) => (
                  <ControlRow
                    key={control.name}
                    control={control}
                    writeEnabled={writeEnabled}
                    expanded={expanded[control.name]}
                    token={token}
                    onToggle={toggle}
                    onDone={applyUpdate}
                    onError={setError}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

interface RowProps {
  control: ControlView;
  writeEnabled: boolean;
  expanded?: "pause" | "resume";
  token: string;
  onToggle: (name: string, action: "pause" | "resume") => void;
  onDone: (updated: ControlView) => void;
  onError: (message: string) => void;
}

function ControlRow({ control, writeEnabled, expanded, token, onToggle, onDone, onError }: RowProps) {
  const paused = control.launchPolicy === "Paused";
  const locked = !control.management.writable;
  const canMutate = writeEnabled && !locked;
  return (
    <>
      <tr className={`gate-row${paused ? " is-paused" : ""}${expanded && canMutate ? " is-expanded" : ""}`}>
        <td className="mono gate-name" data-label="Name">{control.name}</td>
        <td className="mono" data-label="Application">{control.application}</td>
        <td data-label="Policy">
          <PolicyBadge policy={control.launchPolicy} />
        </td>
        <td className="muted" data-label="Phase">{control.phase || "-"}</td>
        <td className="muted gate-reason" data-label="Reason">{control.reason || "-"}</td>
        <td className="mono" data-label="Expires">
          {control.expiresAt ? new Date(control.expiresAt).toLocaleString() : "-"}
        </td>
        <td className="mono" data-label="Sched">{control.affectedSchedules ?? 0}</td>
        <td className="mono" data-label="Active">{control.activeRuns ?? 0}</td>
        <td data-label="Actions">
          {locked ? (
            <span className="chip chip-lock" title={`GitOps-owned (${control.management.managedBy ?? ""})`}>
              <Icon name="lock" />
              locked
            </span>
          ) : (
            <span className="chip-row">
              {paused ? (
                <button type="button" className="btn btn-primary" disabled={!canMutate} onClick={() => onToggle(control.name, "resume")}>
                  <Icon name="play" />
                  Resume
                </button>
              ) : (
                <button type="button" className="btn btn-warn" disabled={!canMutate} onClick={() => onToggle(control.name, "pause")}>
                  <Icon name="pause" />
                  Pause
                </button>
              )}
            </span>
          )}
        </td>
      </tr>
      {expanded && canMutate ? (
        <tr className="gate-form-row">
          <td colSpan={9}>
            <MutateForm
              control={control}
              action={expanded}
              token={token}
              onDone={onDone}
              onError={onError}
            />
          </td>
        </tr>
      ) : null}
    </>
  );
}
