import { useEffect, useRef, useState } from "react";
import { getAgentRun, type AgentRunView } from "../api/client";
import { openAgentRunStream } from "../api/stream";
import type { StreamEnvelope } from "../api/types.stream";
import { peerSignalText } from "../wrapper/collaboration";
import {
  isInterruptDuplicateLogLine,
  isRequestPeerLogLine,
  preferStickyConferral,
  stickyConferralAction,
  STATUS_JSON_PREFIX,
} from "../wrapper/requestPeer";
import { isInterruptDuplicateHold, readyReason, stickAgentRunStatus } from "../wrapper/runStatus";
import { AgentRunStatusCard } from "./AgentRunStatusCard";

function statusJsonHighlight(body: string): boolean {
  return (
    body.includes(STATUS_JSON_PREFIX) ||
    isRequestPeerLogLine(body) ||
    isInterruptDuplicateLogLine(body) ||
    peerSignalText(body)
  );
}

function requestPeerHighlight(body: string): boolean {
  return isRequestPeerLogLine(body);
}

function interruptDuplicateHighlight(body: string): boolean {
  return (
    isInterruptDuplicateLogLine(body) ||
    (body.includes(STATUS_JSON_PREFIX) && body.includes("interruptDuplicate")) ||
    body.includes("ready=InterruptDuplicate") ||
    (body.includes("phase=Failed") && body.includes("interruptDuplicate:"))
  );
}

const MAX_STREAM_ROWS = 2000;

interface StreamRow {
  key: string;
  kind: string;
  timestamp: string;
  body: string;
  peerHighlight: boolean;
  interruptHighlight: boolean;
  requestPeerHighlight: boolean;
}

interface Props {
  token: string;
  namespace: string;
  name: string;
  title?: string;
}

export function LiveStream({ token, namespace, name, title }: Props) {
  const [rows, setRows] = useState<StreamRow[]>([]);
  const [crStatus, setCrStatus] = useState<AgentRunView | null>(null);
  const [status, setStatus] = useState<"idle" | "connecting" | "live" | "ended" | "error">("idle");
  const [statusText, setStatusText] = useState("idle");
  const [outputConferral, setOutputConferral] = useState("");
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const rowCounter = useRef(0);

  useEffect(() => {
    setRows([]);
    setCrStatus(null);
    setStatus("connecting");
    setStatusText("connecting");
    setOutputConferral("");
    rowCounter.current = 0;

    const append = (kind: string, body: string, timestamp?: string) => {
      rowCounter.current += 1;
      const peerHighlight = statusJsonHighlight(body);
      const interruptHighlight = interruptDuplicateHighlight(body);
      const requestPeer = requestPeerHighlight(body);
      setRows((prev) => {
        const next = [
          ...prev,
          {
            key: `${rowCounter.current}`,
            kind,
            timestamp: timestamp || new Date().toISOString(),
            body,
            peerHighlight,
            interruptHighlight,
            requestPeerHighlight: requestPeer,
          },
        ];
        if (next.length <= MAX_STREAM_ROWS) {
          return next;
        }
        return next.slice(next.length - MAX_STREAM_ROWS);
      });
    };

    const ingestRun = (run: AgentRunView) => {
      setCrStatus((prev) => stickAgentRunStatus(prev, run));
      setOutputConferral((prev) => preferStickyConferral(prev, stickyConferralAction(run)));
    };

    let cancelled = false;
    void (async () => {
      try {
        const run = await getAgentRun(token, namespace, name);
        if (!cancelled) {
          ingestRun(run);
        }
      } catch {
        // stream snapshot still hydrates CR fields
      }
    })();
    const poll = window.setInterval(() => {
      void getAgentRun(token, namespace, name)
        .then((run) => {
          if (!cancelled) {
            ingestRun(run);
          }
        })
        .catch(() => {
          // keep last sticky CR status
        });
    }, 3000);

    const handle = openAgentRunStream(token, namespace, name, {
      onEvent: (event, payload) => {
        setStatus("live");
        setStatusText(event);
        switch (event) {
          case "snapshot":
          case "status":
          case "terminal":
            if (payload.run) {
              ingestRun({
                name: payload.run.name || name,
                namespace: payload.run.namespace || namespace,
                ...payload.run,
              });
            }
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

    return () => {
      cancelled = true;
      window.clearInterval(poll);
      handle.abort();
    };
  }, [token, namespace, name]);

  useEffect(() => {
    const node = scrollerRef.current;
    if (!node) {
      return;
    }
    node.scrollTop = node.scrollHeight;
  }, [rows]);

  const peerHits = rows.filter((row) => row.peerHighlight).length;
  const interruptHits = rows.filter((row) => row.interruptHighlight).length;
  const requestPeerHits = rows.filter((row) => row.requestPeerHighlight).length;
  const showRequestPeer = requestPeerHits > 0 || outputConferral === "requestPeer";
  const showInterrupt = interruptHits > 0 || outputConferral === "interruptDuplicate";
  const crHeld = isInterruptDuplicateHold(crStatus);

  return (
    <div className="stream-panel">
      <div className="stream-toolbar">
        <span className="panel-title">{title || name}</span>
        <span className={`stream-status ${status === "live" ? "live" : status === "error" ? "error" : ""}`}>
          {statusText}
        </span>
        {peerHits > 0 ? <span className="chip chip-ok">{peerHits} peer signal(s)</span> : null}
        {showRequestPeer ? (
          <span className="chip chip-ok" data-conferral="requestPeer">
            requestPeer
          </span>
        ) : null}
        {showInterrupt ? (
          <span className="chip chip-ok" data-conferral="interruptDuplicate">
            interruptDuplicate
          </span>
        ) : null}
        {crHeld ? <span className="chip chip-fail">CR InterruptDuplicate</span> : null}
      </div>
      {crStatus ? <AgentRunStatusCard run={crStatus} /> : null}
      <div className="stream-log" ref={scrollerRef}>
        {rows.length === 0 ? <div className="empty">Waiting for stream events…</div> : null}
        {rows.map((row) => (
          <div
            key={row.key}
            className={`stream-line kind-${row.kind}${row.peerHighlight ? " stream-line-peer" : ""}${row.interruptHighlight ? " stream-line-interrupt" : ""}${row.requestPeerHighlight ? " stream-line-request-peer" : ""}`}
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
  const run = payload.run;
  const bits = [
    payload.message || "",
    run ? `phase=${run.phase ?? "—"}` : "",
    run && readyReason(run) ? `ready=${readyReason(run)}` : "",
    run?.error ? `error=${run.error}` : "",
    run?.decision?.action ? `action=${run.decision.action}` : "",
    run?.decision?.summary ? `summary=${run.decision.summary}` : "",
    run?.backend ? `backend=${run.backend}` : "",
  ].filter(Boolean);
  return bits.join(" · ") || event;
}
