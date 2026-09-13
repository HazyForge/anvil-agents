import { useEffect, useRef, useState } from "react";
import { createAgentRun, getAgentRun } from "../api/client";
import { openAgentRunStream } from "../api/stream";
import { isRunningPhase } from "../wrapper/collaboration";
import { parseRequestPeerFromLogLine, STATUS_JSON_PREFIX, type RequestPeerPayload } from "../wrapper/requestPeer";

interface Props {
  token: string;
  namespace: string;
  sourceRun: string;
  createEnabled: boolean;
}

type PostedPeer = {
  peerProfileName: string;
  peerRunName: string;
  at: string;
};

export function RequestPeerMonitor({ token, namespace, sourceRun, createEnabled }: Props) {
  const [phase, setPhase] = useState("");
  const [logHits, setLogHits] = useState<string[]>([]);
  const [posted, setPosted] = useState<PostedPeer[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [streamStatus, setStreamStatus] = useState("connecting");
  const fulfilled = useRef<Set<string>>(new Set());
  const posting = useRef(false);

  useEffect(() => {
    let cancelled = false;
    const pollPhase = async () => {
      try {
        const run = await getAgentRun(token, namespace, sourceRun);
        if (!cancelled) {
          setPhase(run.phase || "");
        }
      } catch {
        // keep last phase
      }
    };
    void pollPhase();
    const id = window.setInterval(() => void pollPhase(), 3000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [token, namespace, sourceRun]);

  useEffect(() => {
    setLogHits([]);
    setPosted([]);
    setError(null);
    setStreamStatus("connecting");
    fulfilled.current = new Set();

    const handle = openAgentRunStream(token, namespace, sourceRun, {
      onEvent: (event, payload) => {
        setStreamStatus(event);
        if (event === "log" && payload.line) {
          const line = payload.line;
          const peer = parseRequestPeerFromLogLine(line);
          if (peer) {
            setLogHits((prev) => uniqueLines([...prev, line]));
            void maybePostPeer(peer, line);
          } else if (line.includes(STATUS_JSON_PREFIX) && line.includes("requestPeer")) {
            setLogHits((prev) => uniqueLines([...prev, line]));
          }
        }
      },
      onTransportError: (err) => setError(err.message),
    });

    async function maybePostPeer(peer: RequestPeerPayload, line: string) {
      const key = `${sourceRun}:${peer.peerProfileName}`;
      if (fulfilled.current.has(key) || posting.current) {
        return;
      }
      if (!createEnabled) {
        setError("AgentRun create is disabled — cannot POST peer run");
        return;
      }
      let running = false;
      for (let attempt = 0; attempt < 48; attempt += 1) {
        try {
          const run = await getAgentRun(token, namespace, sourceRun);
          setPhase(run.phase || "");
          if (isRunningPhase(run.phase)) {
            running = true;
            break;
          }
        } catch (err) {
          setError(err instanceof Error ? err.message : String(err));
          return;
        }
        await sleep(2500);
      }
      if (!running) {
        setError(`requestPeer seen but ${sourceRun} never reached Running`);
        fulfilled.current.delete(key);
        return;
      }
      posting.current = true;
      fulfilled.current.add(key);
      try {
        const created = await createAgentRun(token, namespace, {
          generateName: "desktop-peer-",
          profileName: peer.peerProfileName,
          prompt: [
            `Desktop spawned this peer AgentRun after requestPeer from ${sourceRun}.`,
            peer.summary ? `Summary: ${peer.summary}` : "",
            `Source log line: ${line}`,
          ]
            .filter(Boolean)
            .join("\n"),
        });
        const name =
          created && typeof created === "object" && "name" in created && typeof created.name === "string"
            ? created.name
            : "unknown";
        setPosted((prev) => [
          ...prev,
          { peerProfileName: peer.peerProfileName, peerRunName: name, at: new Date().toISOString() },
        ]);
        setError(null);
      } catch (err) {
        fulfilled.current.delete(key);
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        posting.current = false;
      }
    }

    return () => handle.abort();
  }, [token, namespace, sourceRun, createEnabled]);

  const sawRequestPeer = logHits.some((line) => line.includes("requestPeer"));
  const running = isRunningPhase(phase);

  return (
    <section className="panel" style={{ marginTop: "0.75rem" }}>
      <div className="panel-header">
        <h2 className="panel-title">requestPeer · live stream</h2>
        <span className={`chip ${running ? "chip-ok" : ""}`}>{phase || "—"}</span>
        {sawRequestPeer ? <span className="chip chip-ok">requestPeer line</span> : null}
        {posted.length > 0 ? <span className="chip chip-ok">peer posted</span> : null}
      </div>
      <div className="panel-body">
        <p className="muted mono">{sourceRun}</p>
        {error ? <div className="banner banner-error">{error}</div> : null}
        <p className="muted">Stream: {streamStatus}</p>
        {logHits.length > 0 ? (
          <pre className="hint">{logHits.join("\n")}</pre>
        ) : (
          <p className="muted">Waiting for {STATUS_JSON_PREFIX} with type requestPeer while Running…</p>
        )}
        {posted.length > 0 ? (
          <ul className="collab-peer-notes">
            {posted.map((item) => (
              <li key={`${item.peerProfileName}-${item.at}`}>
                POSTed peer {item.peerRunName} (profile {item.peerProfileName})
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </section>
  );
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function uniqueLines(lines: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const line of lines) {
    if (!line || seen.has(line)) {
      continue;
    }
    seen.add(line);
    out.push(line);
  }
  return out;
}
