import { useCallback, useEffect, useRef, useState } from "react";
import { listControls, updateControl } from "../api/controls";
import { APIError } from "../api/client";
import type { ControlView } from "../api/types";

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
    <form className="control-form" onSubmit={submit}>
      {localError ? <div className="banner banner-error">{localError}</div> : null}
      <label className="field">
        Reason
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
          Duration
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
        <label className="field">
          Source name / event id
          <input
            className="input"
            value={sourceName}
            onChange={(event) => setSourceName(event.target.value)}
            placeholder="e.g. MDExOlJlZExhYmVsZWRFdmVudDEyMw=="
          />
        </label>
        <label className="field">
          Source URL
          <input
            className="input"
            value={sourceURL}
            onChange={(event) => setSourceURL(event.target.value)}
            placeholder="https://github.com/…/pull/123"
          />
        </label>
        <label className="field">
          Source actor
          <input
            className="input"
            value={sourceActor}
            onChange={(event) => setSourceActor(event.target.value)}
            placeholder="octocat"
          />
        </label>
      </details>
      <div className="chip-row">
        <button type="submit" className="btn btn-primary" disabled={busy}>
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

  return (
    <div className="panel">
      <div className="panel-header">
        <div>
          <h2 className="panel-title">Launch controls</h2>
          <div className="panel-subtitle mono">AgentRunControl · cluster scope</div>
        </div>
        <div className="toolbar-inline">
          <span className="count-pill">{controls.length} gates</span>
          <span className="count-pill">{pausedCount} paused</span>
          {refreshedAt ? (
            <span className="count-pill">refreshed {new Date(refreshedAt).toLocaleTimeString()}</span>
          ) : null}
          <button
            type="button"
            className="btn"
            onClick={() => void load(controls.length === 0 ? "initial" : "refresh")}
            disabled={initialLoading || refreshing}
          >
            {initialLoading ? "Loading…" : refreshing ? "Refreshing…" : "Refresh"}
          </button>
        </div>
      </div>

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
        <div className="empty">Loading launch gates…</div>
      ) : controls.length === 0 ? (
        <div className="empty">No launch gates found for your authorized applications.</div>
      ) : (
        <div className="table-wrap">
          <table className="runs">
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
      <tr>
        <td className="mono">{control.name}</td>
        <td className="mono">{control.application}</td>
        <td>
          <PolicyBadge policy={control.launchPolicy} />
        </td>
        <td className="muted">{control.phase || "-"}</td>
        <td className="muted">{control.reason || "-"}</td>
        <td className="mono">
          {control.expiresAt ? new Date(control.expiresAt).toLocaleString() : "-"}
        </td>
        <td>{control.affectedSchedules ?? 0}</td>
        <td>{control.activeRuns ?? 0}</td>
        <td>
          {locked ? (
            <span className="chip chip-lock" title={`GitOps-owned (${control.management.managedBy ?? ""})`}>
              locked
            </span>
          ) : (
            <span className="chip-row">
              {paused ? (
                <button type="button" className="btn btn-ghost" disabled={!canMutate} onClick={() => onToggle(control.name, "resume")}>
                  Resume
                </button>
              ) : (
                <button type="button" className="btn btn-danger" disabled={!canMutate} onClick={() => onToggle(control.name, "pause")}>
                  Pause
                </button>
              )}
            </span>
          )}
        </td>
      </tr>
      {expanded && canMutate ? (
        <tr>
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
