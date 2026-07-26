import { useEffect, useRef, useState } from "react";
import { openAgentRunStream } from "../api/stream";
import type { AgentRunView, StreamEnvelope } from "../api/types";
import { copyText, formatTime } from "../utils/format";

interface StreamRow {
  key: string;
  kind: string;
  timestamp: string;
  body: string;
}

interface Props {
  token: string;
  namespace: string;
  name: string;
  onRunUpdate?: (run: AgentRunView) => void;
}

/** Soft cap so long-running streams cannot grow tab memory without bound. */
const MAX_STREAM_ROWS = 5000;

export function LiveStream({ token, namespace, name, onRunUpdate }: Props) {
  const [rows, setRows] = useState<StreamRow[]>([]);
  const [status, setStatus] = useState<"idle" | "connecting" | "live" | "ended" | "error">("idle");
  const [statusText, setStatusText] = useState("idle");
  const [follow, setFollow] = useState(true);
  const [copyState, setCopyState] = useState("");
  const [reconnectKey, setReconnectKey] = useState(0);
  /** When true, next effect open clears Last-Event-ID (full restart). */
  const restartFromBeginning = useRef(false);
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const lastEventID = useRef<string | undefined>(undefined);
  const streamIdentity = useRef(`${namespace}/${name}`);
  const rowCounter = useRef(0);
  const onRunUpdateRef = useRef(onRunUpdate);
  onRunUpdateRef.current = onRunUpdate;

  useEffect(() => {
    const identity = `${namespace}/${name}`;
    // Never resume across run or namespace changes.
    if (streamIdentity.current !== identity || restartFromBeginning.current) {
      lastEventID.current = undefined;
      streamIdentity.current = identity;
      restartFromBeginning.current = false;
    }

    setRows([]);
    setStatus("connecting");
    setStatusText("connecting");
    rowCounter.current = 0;

    const append = (kind: string, body: string, timestamp?: string) => {
      rowCounter.current += 1;
      setRows((prev) => {
        const next = [
          ...prev,
          {
            key: `${rowCounter.current}`,
            kind,
            timestamp: timestamp || new Date().toISOString(),
            body,
          },
        ];
        if (next.length <= MAX_STREAM_ROWS) {
          return next;
        }
        return next.slice(next.length - MAX_STREAM_ROWS);
      });
    };

    const handle = openAgentRunStream(
      token,
      namespace,
      name,
      {
        onEvent: (event, payload, raw) => {
          if (raw.id) {
            lastEventID.current = raw.id;
          }
          setStatus("live");
          setStatusText(event);
          switch (event) {
            case "snapshot":
            case "status":
            case "terminal": {
              if (payload.run) {
                onRunUpdateRef.current?.(payload.run);
              }
              append(
                event,
                summarizeRunEvent(event, payload),
                payload.run?.completedAt || payload.run?.startedAt,
              );
              if (event === "terminal") {
                setStatus("ended");
                setStatusText(payload.code ? `terminal:${payload.code}` : "terminal");
              }
              break;
            }
            case "log": {
              const line = payload.line ?? "";
              append("log", line, payload.timestamp);
              break;
            }
            case "reset":
              append("reset", payload.message || payload.reason || "stream reset");
              break;
            case "error":
              append("error", payload.message || payload.code || "stream error");
              setStatus("error");
              setStatusText(payload.code || "error");
              break;
            case "complete":
              append("complete", payload.message || payload.code || "stream complete");
              setStatus("ended");
              setStatusText(payload.code || "complete");
              break;
            default:
              append(event, payload.message || JSON.stringify(payload));
          }
        },
        onTransportError: (error) => {
          append("error", error.message);
          setStatus("error");
          setStatusText("transport");
        },
        onDone: () => {
          setStatus((prev) => (prev === "live" || prev === "connecting" ? "ended" : prev));
        },
      },
      { lastEventID: lastEventID.current },
    );

    return () => handle.abort();
  }, [token, namespace, name, reconnectKey]);

  useEffect(() => {
    if (!follow || !scrollerRef.current) {
      return;
    }
    scrollerRef.current.scrollTop = scrollerRef.current.scrollHeight;
  }, [rows, follow]);

  async function handleCopyName() {
    const ok = await copyText(name);
    setCopyState(ok ? "copied" : "failed");
    window.setTimeout(() => setCopyState(""), 1500);
  }

  return (
    <div className="panel stream-panel span-2">
      <div className="stream-toolbar">
        <span className="panel-title">Live stream</span>
        <button type="button" className="btn" onClick={handleCopyName}>
          Copy run name{copyState ? ` (${copyState})` : ""}
        </button>
        <label className="check">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
          Follow latest
        </label>
        <button
          type="button"
          className="btn"
          title="Open a new stream without Last-Event-ID"
          onClick={() => {
            restartFromBeginning.current = true;
            setReconnectKey((k) => k + 1);
          }}
        >
          Restart stream
        </button>
        <button
          type="button"
          className="btn"
          title="Reconnect using Last-Event-ID when available"
          onClick={() => {
            setReconnectKey((k) => k + 1);
          }}
        >
          Resume / reconnect
        </button>
        <span className={`stream-status ${status === "live" ? "live" : status === "error" ? "error" : ""}`}>
          {statusText}
        </span>
      </div>
      <div className="stream-log" ref={scrollerRef}>
        {rows.length === 0 ? (
          <div className="empty">Waiting for SSE events…</div>
        ) : (
          rows.map((row) => (
            <div key={row.key} className={`stream-line kind-${row.kind}`}>
              <span className="ts">{formatTime(row.timestamp)}</span>
              <span className="kind">{row.kind}</span>
              <span className="body">{row.body}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function summarizeRunEvent(event: string, payload: StreamEnvelope): string {
  if (payload.message) {
    return payload.message;
  }
  const run = payload.run;
  if (!run) {
    return event;
  }
  const bits = [
    `phase=${run.phase ?? "—"}`,
    run.backend ? `backend=${run.backend}` : "",
    run.error ? `error=${run.error}` : "",
    run.decision?.action ? `action=${run.decision.action}` : "",
  ].filter(Boolean);
  return bits.join(" · ");
}
