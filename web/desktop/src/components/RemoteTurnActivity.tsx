import { useEffect, useRef, useState } from 'react';
import { turnWaitFeedback } from '../api/turnWait';
import { openAgentRunStream } from '../api/stream';
import { activityFromLog, type RunActivity } from '../api/runActivity';
import type { RemoteTurn } from '../api/remoteChat';
import { ensureAccessToken } from '../auth/oidc';
import { chatFailureLabel, runnerFailureLabel, runnerStateLabel, isStandingAPIHold, activityTreatAsFailed } from '../api/runnerState';

interface Props {
  token: string;
  namespace: string;
  turn?: RemoteTurn;
  agentLabel?: string;
  recoveryPending?: boolean;
}
type ActivityRow = RunActivity & { at: number };
const done = (status?: string) => status === 'succeeded' || status === 'failed';

/** Public runner events, kept separate from the agent's answer. */
export function RemoteTurnActivity({token, namespace, turn, agentLabel, recoveryPending}: Props) {
  const [rows, setRows] = useState<ActivityRow[]>([]);
  const [workObserved, setWorkObserved] = useState(false);
  const [connection, setConnection] = useState('Connecting to agent activity…');
  const [now, setNow] = useState(Date.now());
  const [lastUpdate, setLastUpdate] = useState(Date.now());
  const [terminalPhase, setTerminalPhase] = useState('');
  const [terminalLabel, setTerminalLabel] = useState('Agent could not finish this turn');
  const [runnerBlocker, setRunnerBlocker] = useState<string | undefined>();
  const finished = useRef(done(turn?.status));
  const runName = turn?.runName;
  const status = turn?.status;
  const retrying = !done(status) && (turn?.retryCount ?? 0) > 0;
  const retrySeconds = Math.max(0, Math.ceil((Date.parse(turn?.retryAt || '') - now) / 1000)) || 0;
  const acceptedAt = turn?.createdAt ? Date.parse(turn.createdAt) : undefined;
  const mountedAt = useRef(Date.now());
  finished.current = done(status);

  useEffect(() => {
    setRows([]); setWorkObserved(false); setTerminalPhase(''); setTerminalLabel('Agent could not finish this turn'); setRunnerBlocker(undefined); setConnection('Connecting to agent activity…');
    mountedAt.current = Date.now(); setNow(Date.now()); setLastUpdate(Number.isFinite(acceptedAt) ? Math.min(acceptedAt!, Date.now()) : Date.now());
    if (!runName) return;
    let cancelled = false;
    let stopped = false;
    let reconnect: ReturnType<typeof setTimeout> | undefined;
    let handle: {abort: () => void} | undefined;
    const seen = new Set<string>();
    let latestLogTime = 0;
    let streamToken = token;
    let rejectedToken = '';
    let refreshing: Promise<void> | undefined;
    const append = (item: RunActivity, timestamp?: string) => {
      if (cancelled || seen.has(item.key)) return;
      seen.add(item.key);
      // Keep reconnection tails bounded even on long tool-heavy executions.
      if (seen.size > 400) seen.delete(seen.values().next().value!);
      const parsed = timestamp ? Date.parse(timestamp) : NaN;
      const at = Number.isFinite(parsed) && parsed > 0 ? parsed : Date.now();
      setRows(previous => [...previous, {...item, at}].slice(-80));
    };
    const connect = () => {
      if (cancelled || stopped) return;
      handle = openAgentRunStream(streamToken, namespace, runName, {
        onEvent: (event, payload) => {
          if (cancelled) return;
          if (event === 'snapshot' || event === 'status' || event === 'terminal') {
            const phase = payload.run?.phase;
            if (payload.run) setRunnerBlocker(runnerStateLabel(payload.run.runnerState));
            setConnection('Connected to agent activity');
            if (phase === 'Running') append({key: 'runner-preparing', label: 'Preparing the remote runner', kind: 'setup'});
            if (phase === 'Pending') append({key: 'runner-queued', label: 'Waiting for a remote runner', kind: 'setup'});
            if (phase === 'Succeeded') {
              stopped = true;
              setTerminalPhase(phase);
              append({key: 'runner-complete', label: 'Harness finished', kind: 'reply'});
            }
            if (phase === 'Failed' || (phase === 'NeedsHuman' && !isStandingAPIHold(payload.run))) {
              stopped = true;
              setTerminalPhase(phase);
              setTerminalLabel(runnerFailureLabel(payload.run));
              append({key: 'runner-failed', label: runnerFailureLabel(payload.run), kind: 'error'});
            }
            if (event === 'terminal' && (payload.code === 'deleted' || payload.code === 'replaced')) {
              setConnection('Runner activity is no longer available');
            }
            if (event === 'terminal') stopped = true;
          } else if (event === 'log') {
            setConnection('Connected to agent activity');
            const logTime = payload.timestamp ? Date.parse(payload.timestamp) : NaN;
            // Reconnect tails replay older lines. Keep the visible sequence forward.
            if (Number.isFinite(logTime) && logTime > 0 && logTime < latestLogTime) return;
            if (Number.isFinite(logTime) && logTime > 0) latestLogTime = logTime;
            const activity = activityFromLog(payload.line ?? '');
            if (activity) {
              if (activity.kind === 'work' || activity.kind === 'reply') setWorkObserved(true);
              // A repeated native activity is still evidence of liveness, while
              // replayed timestamped tails must not appear newly active.
              const at = Number.isFinite(logTime) && logTime > 0 ? Math.min(logTime, Date.now()) : Date.now();
              setLastUpdate(previous => Math.max(previous, at));
              append(activity, payload.timestamp);
            }
          } else if (event === 'error') {
            if (['http_401', 'token_expired'].includes(payload.code ?? '') && rejectedToken !== streamToken) {
              rejectedToken = streamToken;
              setConnection('Refreshing your session to reconnect activity…');
              refreshing = ensureAccessToken().then(access => {
                if (cancelled) return;
                if (access && access !== streamToken) streamToken = access;
                else {
                  stopped = true;
                  setConnection('Sign in again to reconnect agent activity');
                }
              }).catch(() => {
                if (!cancelled) { stopped = true; setConnection('Sign in again to reconnect agent activity'); }
              });
            } else if (payload.code === 'http_404' && !finished.current) {
              setConnection('Waiting for the remote runner to become available…');
            } else if (['http_401', 'http_403', 'http_404', 'token_expired'].includes(payload.code ?? '')) {
              stopped = true;
              setConnection(payload.code === 'http_404' ? 'Runner activity is no longer available' : 'Sign in with access to this run to see its activity');
            } else {
              setConnection('Waiting for activity updates; your message is saved');
            }
          } else if (event === 'reset' || event === 'complete') {
            if (!stopped && !finished.current) setConnection('Reconnecting to agent activity…');
            // A bounded log tail can reset while its SSE connection stays open.
            // Closing it lets onDone schedule exactly one fresh bounded tail.
            if (event === 'reset' && !stopped && !finished.current) handle?.abort();
          }
        },
        onTransportError: () => {
          if (!cancelled) setConnection('Connection interrupted; reconnecting to agent activity…');
        },
        onDone: () => {
          void (async () => {
            await refreshing;
            if (!cancelled && !stopped && !finished.current) reconnect = setTimeout(connect, 3000);
          })();
        },
      }, {tailLines: 200});
    };
    connect();
    return () => {cancelled = true; clearTimeout(reconnect); handle?.abort();};
  }, [token, namespace, runName]);

  useEffect(() => {
    if (!turn || done(status)) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [turn?.id, status]);

  if (!turn) return null;
  const elapsed = Math.max(0, Math.floor((now - (Number.isFinite(acceptedAt) ? acceptedAt! : mountedAt.current)) / 1000));
  const latest = rows.at(-1);
  const cannotCheck = recoveryPending && !done(status) && !terminalPhase;
  const wait = cannotCheck ? {tone: 'delayed', label: 'Cannot check agent progress right now. Your message is saved.', note: 'Waiting for the server to reconnect to the runner. Keep your draft; sending will become available when the server confirms this turn has finished.'} : turnWaitFeedback(terminalPhase === 'Succeeded' ? 'succeeded' : activityTreatAsFailed(status, terminalPhase) ? 'failed' : status, elapsed, Math.max(0, (now - lastUpdate) / 1000), workObserved);
  const failed = activityTreatAsFailed(status, terminalPhase);
  const label = retrying && status === 'queued' ? `Retrying startup${retrySeconds ? ` in ${retrySeconds}s` : ''} · attempt ${(turn?.retryCount ?? 0) + 1} of 3` : failed ? (chatFailureLabel(turn.error) || terminalLabel)
    : status === 'succeeded' ? 'Reply received'
    : terminalPhase === 'Succeeded' ? 'Harness finished; saving the reply'
    : cannotCheck ? 'Cannot check agent progress right now. Your message is saved.'
    : runnerBlocker ? runnerBlocker
    : wait?.label ? wait.label
    : status === 'waiting' ? 'Message saved; waiting for this agent to become available'
    : latest?.label || (status === 'queued' ? 'Message received; waiting for the agent to start' : 'Preparing the remote runner');
  const quiet = !done(status) && now - lastUpdate > 15000;
  return <section className={`turn-activity${done(status) ? ' turn-activity-finished' : ''}${retrying ? ' turn-activity-delayed' : wait ? ` turn-activity-${wait.tone}` : ''}`} aria-label="Agent activity">
    <div className="turn-activity-heading">
      <span className={`turn-activity-indicator${failed ? ' is-error' : ''}`} aria-hidden="true"/>
      <div><span className="turn-activity-agent">{agentLabel || 'Remote agent'}</span><p role="status">{label}</p></div>
      {!done(status) && <span className="turn-activity-elapsed" aria-hidden="true">{elapsed < 60 ? `${elapsed}s` : `${Math.floor(elapsed / 60)}m ${elapsed % 60}s`}</span>}
    </div>
    {rows.length > 0 && <ol className="turn-activity-events" aria-label="Reported actions">
      {rows.slice(-4).map(row => <li key={row.key} className={`activity-${row.kind}`}><span>{row.label}</span><time dateTime={new Date(row.at).toISOString()}>{new Date(row.at).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit', second: '2-digit'})}</time></li>)}
    </ol>}
    {retrying && <p className="turn-activity-note">{turn?.recoveryReason || 'The previous runner did not start.'} Your saved message will continue automatically.</p>}
    {!retrying && wait?.note && <p className="turn-activity-note">{wait.note}</p>}
    {!retrying && !wait?.note && !done(status) && (quiet || connection !== 'Connected to agent activity') && <p className="turn-activity-note">{quiet && connection === 'Connected to agent activity' ? 'No new activity reported recently. Waiting for the next update.' : connection}</p>}
    {rows.length > 4 && <details className="turn-activity-history"><summary>Earlier activity ({rows.length - 4})</summary><ol>{rows.slice(0, -4).map(row => <li key={row.key}>{row.label}</li>)}</ol></details>}
  </section>;
}
