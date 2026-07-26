import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { APIError, getAgentRun } from "../api/client";
import type { AgentRunView } from "../api/types";
import { RunDetail } from "../components/RunDetail";

interface Props {
  token: string;
  /** Sync top-bar active namespace (and persistence) with the run being viewed. */
  onViewNamespace?: (namespace: string) => void;
}

export function RunPage({ token, onViewNamespace }: Props) {
  const params = useParams();
  const namespace = params.namespace ?? "";
  const name = params.name ?? "";
  const [run, setRun] = useState<AgentRunView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const requestIdRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);

  // Keep top-bar active namespace aligned with the run being viewed so
  // "← Run board" returns to the correct board after deep links.
  useEffect(() => {
    if (!namespace) {
      return;
    }
    onViewNamespace?.(namespace);
  }, [namespace, onViewNamespace]);

  // Drop prior run immediately when route identity changes so a slow
  // in-flight response cannot paint under the wrong URL even briefly.
  useEffect(() => {
    setRun(null);
    setError(null);
    setLoading(Boolean(namespace && name));
  }, [namespace, name, token]);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const requestId = ++requestIdRef.current;

    if (!namespace || !name) {
      if (requestId !== requestIdRef.current) {
        return;
      }
      setError("Missing namespace or run name.");
      setLoading(false);
      setRun(null);
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const view = await getAgentRun(token, namespace, name, controller.signal);
      if (requestId !== requestIdRef.current) {
        return;
      }
      setRun(view);
    } catch (err) {
      if (controller.signal.aborted || requestId !== requestIdRef.current) {
        return;
      }
      if (err instanceof APIError) {
        setError(`${err.code}: ${err.message}`);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
      setRun(null);
    } finally {
      if (requestId === requestIdRef.current) {
        setLoading(false);
      }
    }
  }, [token, namespace, name]);

  useEffect(() => {
    void load();
    return () => {
      abortRef.current?.abort();
      requestIdRef.current += 1;
    };
  }, [load]);

  // Back always returns to the run's namespace board, even if the operator
  // changed the top-bar switcher while still on this detail route.
  function handleBackToBoard() {
    if (namespace) {
      onViewNamespace?.(namespace);
    }
  }

  return (
    <div className="stack">
      <div className="toolbar-inline">
        <Link className="back-link" to="/" onClick={handleBackToBoard}>
          ← Run board{namespace ? ` (${namespace})` : ""}
        </Link>
        <button type="button" className="btn" onClick={() => void load()} disabled={loading}>
          {loading ? "Loading…" : "Reload"}
        </button>
      </div>
      {error ? <div className="banner banner-error">{error}</div> : null}
      {run && run.namespace === namespace && run.name === name ? (
        <RunDetail run={run} token={token} onRunUpdate={setRun} />
      ) : !error ? (
        <div className="empty">Loading run…</div>
      ) : null}
    </div>
  );
}
