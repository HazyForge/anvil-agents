import { useEffect, useRef, useState } from "react";
import { openAgentRunStream } from "../api/stream";
import type { StreamEnvelope } from "../api/types.stream";
import { peerSignalText } from "../wrapper/collaboration";

const MAX_STREAM_ROWS = 2000;

interface StreamRow {
  key: string;
  kind: string;
  timestamp: string;
  body: string;
  peerHighlight: boolean;
}

interface Props {
  token: string;
  namespace: string;
  name: string;
  title?: string;
}

export function LiveStream({ token, namespace, name, title }: Props) {
  const [rows, setRows] = useState<StreamRow[]>([]);
  const [status, setStatus] = useState<"idle" | "connecting" | "live" | "ended" | "error">("idle");
  const [statusText, setStatusText] = useState("idle");
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const rowCounter = useRef(0);

  useEffect(() => {
    setRows([]);
    setStatus("connecting");
    setStatusText("connecting");
    rowCounter.current = 0;

    const append = (kind: string, body: string, timestamp?: string) => {
      rowCounter.current += 1;
      const peerHighlight = peerSignalText(body);
      setRows((prev) => {
        const next = [
          ...prev,
          {
            key: `${rowCounter.current}`,
            kind,
            timestamp: timestamp || new Date().toISOString(),
            body,
            peerHighlight,
          },
        ];
        if (next.length <= MAX_STREAM_ROWS) {
          return next;
        }
        return next.slice(next.length - MAX_STREAM_ROWS);
      });
    };

    const handle = openAgentRunStream(token, namespace, name, {
      onEvent: (event, payload) => {
        setStatus("live");
        setStatusText(event);
        switch (event) {
          case "snapshot":
          case "status":
          case "terminal":
            append(event, summarizeRunEvent(event, payload));
            if (event === "terminal") {
              setStatus("ended");
            }
            break;
          case "log":
            append("log", payload.line ?? "", payload.timestamp);
            break;
          case "reset":
            append("reset", payload.message || payload.reason || "stream reset");
            break;
          case "error":
            append("error", payload.message || payload.code || "stream error");
            setStatus("error");
            break;
          case "complete":
            append("complete", payload.message || payload.code || "stream complete");
            setStatus("ended");
            break;
          default:
            append(event, payload.message || JSON.stringify(payload));
        }
      },
      onTransportError: (error) => {
        append("error", error.message);
        setStatus("error");
      },
      onDone: () => {
        setStatus((prev) => (prev === "live" || prev === "connecting" ? "ended" : prev));
      },
    });

    return () => handle.abort();
  }, [token, namespace, name]);

  useEffect(() => {
    const node = scrollerRef.current;
    if (!node) {
      return;
    }
    node.scrollTop = node.scrollHeight;
  }, [rows]);

  const peerHits = rows.filter((row) => row.peerHighlight).length;

  return (
    <div className="stream-panel">
      <div className="stream-toolbar">
        <span className="panel-title">{title || name}</span>
        <span className={`stream-status ${status === "live" ? "live" : status === "error" ? "error" : ""}`}>
          {statusText}
        </span>
        {peerHits > 0 ? <span className="chip chip-ok">{peerHits} peer signal(s)</span> : null}
      </div>
      <div className="stream-log" ref={scrollerRef}>
        {rows.length === 0 ? <div className="empty">Waiting for stream events…</div> : null}
        {rows.map((row) => (
          <div
            key={row.key}
            className={`stream-line kind-${row.kind}${row.peerHighlight ? " stream-line-peer" : ""}`}
          >
            <span className="ts">{row.timestamp.slice(11, 19)}</span>
            <span className="kind">{row.kind}</span>
            <span className="body">{row.body}</span>
          </div>
        ))}
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
    run.decision?.summary ? `summary=${run.decision.summary}` : "",
  ].filter(Boolean);
  return bits.join(" · ");
}
