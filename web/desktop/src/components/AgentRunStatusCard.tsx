import {
  INTERRUPT_DUPLICATE_ERROR_PREFIX,
  INTERRUPT_DUPLICATE_REASON,
  isInterruptDuplicateHold,
  readyReason,
  type AgentRunStatusSlice,
} from "../wrapper/runStatus";

interface Props {
  run: AgentRunStatusSlice;
  label?: string;
}

export function AgentRunStatusCard({ run, label }: Props) {
  const phase = (run.phase || "").trim() || "—";
  const ready = readyReason(run) || "—";
  const error = (run.error || "").trim() || "—";
  const held = isInterruptDuplicateHold(run);
  const failed = phase === "Failed";
  return (
    <div className={`cr-status-card ${held ? "cr-status-card-held" : ""}`}>
      {label ? <div className="muted">{label}</div> : null}
      {run.name ? <div className="mono">{run.name}</div> : null}
      <div className="chip-row cr-status-fields">
        <span className={`chip ${failed ? "chip-fail" : phase === "Running" ? "chip-ok" : ""}`}>
          Phase {phase}
        </span>
        <span className={`chip ${ready === INTERRUPT_DUPLICATE_REASON ? "chip-fail" : ""}`}>
          Ready {ready}
        </span>
      </div>
      <div
        className={`cr-status-error ${error.startsWith(INTERRUPT_DUPLICATE_ERROR_PREFIX) ? "cr-status-error-held" : "muted"}`}
      >
        Error {error}
      </div>
    </div>
  );
}
