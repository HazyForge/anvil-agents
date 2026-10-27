import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { AgentAvatar, DesktopIcon } from '../components/AgentAvatar';
import { MarkdownBody } from '../components/MarkdownBody';
import { useStickToBottom } from '../components/useStickToBottom';
import {
  fetchOnechatHostStatus, OnechatClient, OnechatError, startOnechatHost,
  type OnechatEvent, type OnechatFollowup, type OnechatHello, type OnechatHostStatus, type OnechatLiveState, type OnechatSession, type OnechatTurnSummary,
} from '../api/onechat';
import { actionLabel, applyTurnEvent, intentLabel, stopReason, turnFromStatus, turnFromTrace, type TurnView } from '../wrapper/onechatTurn';
import { eventsRetryDelay } from '../wrapper/threadSync';

type Link = 'checking' | 'missing' | 'unconfigured' | 'stopped' | 'connecting' | 'live' | 'retrying';

const draftKey = 'anvil-agents-desktop.manager.draft';
const readDraft = () => { try { return localStorage.getItem(draftKey) ?? ''; } catch { return ''; } };
const saveDraft = (value: string) => { try { localStorage.setItem(draftKey, value); } catch { /* The draft stays in memory. */ } };
const fromPhone = (kind?: string) => kind === 'ios';

/**
 * One chat's manager on this machine (`onechat serve`), reached through the
 * Desktop host. Each exchange can expand into what the manager decided and
 * what every session did; the running turn shows those steps live.
 */
export function ManagerPage() {
  const [hostStatus, setHostStatus] = useState<OnechatHostStatus | null>(null);
  const [link, setLink] = useState<Link>('checking');
  const [hello, setHello] = useState<OnechatHello | null>(null);
  const [busy, setBusy] = useState(false);
  const [sessions, setSessions] = useState<OnechatSession[]>([]);
  const [sessionsStale, setSessionsStale] = useState(false);
  const [turns, setTurns] = useState<OnechatTurnSummary[]>([]);
  const [followups, setFollowups] = useState<OnechatFollowup[]>([]);
  const [live, setLive] = useState<Record<string, OnechatLiveState>>({});
  const [queuedAhead, setQueuedAhead] = useState(0);
  const [details, setDetails] = useState<Record<string, TurnView>>({});
  const [running, setRunning] = useState<TurnView | undefined>();
  const [draft, setDraft] = useState(readDraft);
  const [error, setError] = useState('');
  const [sending, setSending] = useState(false);
  const [now, setNow] = useState(Date.now());
  const client = useRef<OnechatClient | null>(null);
  const pendingRequest = useRef<{text: string; requestId: string} | null>(null);
  // Events arrive in order on one socket; the ref lets a finished turn move
  // into the conversation without side effects inside a state updater.
  const runningRef = useRef<TurnView | undefined>(undefined);
  const jumpToBottom = useRef<() => void>(() => {});
  const showRunning = (turn: TurnView | undefined) => { runningRef.current = turn; setRunning(turn); };

  const loadLists = useCallback(async (connection: OnechatClient) => {
    const history = connection.request('history.list', {limit: 100}).then(body => {
      setTurns(((body.turns as OnechatTurnSummary[]) ?? []).slice().sort((a, b) => Date.parse(a.at) - Date.parse(b.at)));
      setFollowups(((body.followups as OnechatFollowup[]) ?? []).slice().sort((a, b) => Date.parse(a.at) - Date.parse(b.at)));
    });
    // Live session state exists once the daemon runs sessions as workers; older hosts do not answer it.
    void connection.request('session.list', {}).then(body => {
      const states = (body.sessions as OnechatLiveState[] | undefined) ?? [];
      setLive(Object.fromEntries(states.filter(item => item.sessionId).map(item => [item.sessionId, item])));
    }).catch(() => undefined);
    // A failed refresh (say, an expired cloud sign-in) still shows the sessions the host last saw.
    const sessions = connection.request('sessions.list', {refresh: true}, 120_000).catch(err => {
      setError(`Could not refresh sessions: ${err instanceof Error ? err.message : String(err)}`);
      return connection.request('sessions.list', {refresh: false});
    }).then(list => {
      setSessions((list.sessions as OnechatSession[]) ?? []);
      setSessionsStale(Boolean(list.stale));
    });
    await Promise.all([history, sessions]);
  }, []);

  const onEvent = useCallback((event: OnechatEvent) => {
    if (event.event === 'status') {
      const status = event.body as unknown as OnechatHello['status'];
      setBusy(status.state === 'busy');
      return;
    }
    if (event.event === 'sessions.changed') {
      setSessions((event.body.sessions as OnechatSession[]) ?? []);
      setSessionsStale(false);
      return;
    }
    if (event.event === 'session.state' || event.event === 'session.activity') {
      const body = event.body as Partial<OnechatLiveState> & {summary?: string};
      if (!body.sessionId) return;
      setLive(all => {
        const prior = all[body.sessionId!] ?? {sessionId: body.sessionId!, state: 'running'};
        const next: OnechatLiveState = event.event === 'session.state'
          ? {...prior, ...body, sessionId: body.sessionId!, state: body.state ?? prior.state}
          : {...prior, activity: body.summary ?? prior.activity};
        return {...all, [body.sessionId!]: next};
      });
      return;
    }
    if (event.event === 'session.queued') {
      const body = event.body as {sessionId?: string; count?: number};
      if (body.sessionId) setLive(all => ({...all, [body.sessionId!]: {...(all[body.sessionId!] ?? {sessionId: body.sessionId!, state: 'running'}), queued: body.count ?? 0}}));
      return;
    }
    if (event.event === 'chat.message') {
      const body = event.body as Partial<OnechatFollowup> & {kind?: string; role?: string};
      if (body.role === 'assistant' && body.text) {
        const item: OnechatFollowup = {id: body.id ?? `${event.at}-${body.sessionId ?? ''}`, at: body.at ?? event.at, sessionId: body.sessionId, turnId: body.turnId, text: body.text};
        setFollowups(all => all.some(existing => existing.id === item.id) ? all : [...all, item]);
      }
      return;
    }
    if (event.event === 'turn.queued') {
      setQueuedAhead(Number((event.body as {position?: number}).position ?? 0));
      return;
    }
    if (event.event === 'turn.started') setQueuedAhead(0);
    const next = applyTurnEvent(runningRef.current, event);
    if (!next?.finished || event.event !== 'turn.finished') {
      if (next !== runningRef.current) showRunning(next);
      return;
    }
    setDetails(all => ({...all, [next.turnId]: next}));
    setTurns(all => all.some(item => item.turnId === next.turnId) ? all : [...all, {
      turnId: next.turnId, at: next.startedAt ?? event.at, message: next.text,
      answer: next.answer ?? '', error: next.error, actionCount: next.actions.length,
    }]);
    if (pendingRequest.current?.requestId === next.requestId) pendingRequest.current = null;
    showRunning(undefined);
  }, []);

  // Find the host, connect, and reconnect with backoff when it drops.
  useEffect(() => {
    let cancelled = false;
    let failures = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const retry = (state: Link) => {
      if (cancelled) return;
      setLink(state);
      timer = setTimeout(() => void start(), state === 'stopped' || state === 'unconfigured' || state === 'missing' ? 5_000 : eventsRetryDelay(++failures));
    };
    const start = async () => {
      let status: OnechatHostStatus;
      try {
        status = await fetchOnechatHostStatus();
      } catch {
        retry('retrying');
        return;
      }
      if (cancelled) return;
      setHostStatus(status);
      if (status.installed === false) { retry('missing'); return; }
      if (!status.configured) { retry('unconfigured'); return; }
      if (!status.running) { retry('stopped'); return; }
      setLink(failures ? 'retrying' : 'connecting');
      const connection = new OnechatClient({
        onHello: result => {
          if (cancelled) return;
          failures = 0;
          setHello(result);
          setBusy(result.status.state === 'busy');
          showRunning(turnFromStatus(result.status.turn));
          setLink('live');
          void loadLists(connection).catch(err => setError(err instanceof Error ? err.message : String(err)));
        },
        onEvent,
        onClose: () => { if (!cancelled) retry('retrying'); },
      });
      client.current = connection;
      connection.connect();
    };
    void start();
    return () => { cancelled = true; clearTimeout(timer); client.current?.close(); client.current = null; };
  }, [loadLists, onEvent]);

  useEffect(() => {
    if (!running) return;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [running]);

  async function send(event?: FormEvent) {
    event?.preventDefault();
    const text = draft.trim();
    const connection = client.current;
    if (!text || !connection || link !== 'live' || sending) return;
    // A retry of the same words reuses its request id, so a dropped reply never sends the work twice.
    const request = pendingRequest.current?.text === text ? pendingRequest.current : {text, requestId: crypto.randomUUID()};
    pendingRequest.current = request;
    setSending(true); setError(''); jumpToBottom.current();
    try {
      const reply = await connection.request('turn.start', {text, requestId: request.requestId});
      if (reply.queued) setQueuedAhead(Number(reply.position ?? 1));
      setDraft(''); saveDraft('');
    } catch (err) {
      setError(err instanceof OnechatError && err.code === 'busy' ? 'The manager is already working on a message. Wait for it to finish or cancel it.' : err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  }

  async function cancel() {
    if (!running || !client.current) return;
    try { await client.current.request('turn.cancel', {turnId: running.turnId}); } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  async function stopSession(sessionId: string) {
    if (!client.current) return;
    try { await client.current.request('session.stop', {sessionId}); } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  async function refreshSessions() {
    if (!client.current) return;
    try {
      const list = await client.current.request('sessions.list', {refresh: true}, 120_000);
      setSessions((list.sessions as OnechatSession[]) ?? []);
      setSessionsStale(Boolean(list.stale));
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  async function loadDetail(turnId: string) {
    if (details[turnId] || !client.current) return;
    try {
      const body = await client.current.request('trace.get', {turnId});
      const view = turnFromTrace(body.trace as Parameters<typeof turnFromTrace>[0]);
      setDetails(all => ({...all, [turnId]: view}));
    } catch (err) { setError(err instanceof Error ? err.message : String(err)); }
  }

  if (link === 'checking' || link === 'missing' || link === 'unconfigured' || link === 'stopped') {
    return <ManagerOffline link={link} status={hostStatus} onStarted={status => { setHostStatus(status); setLink(status.running ? 'connecting' : 'stopped'); }}/>;
  }

  const local = sessions.filter(session => session.location === 'local');
  const cloud = sessions.filter(session => session.location === 'cloud');
  const elapsed = running?.startedAt ? Math.max(0, Math.round((now - Date.parse(running.startedAt)) / 1000)) : 0;

  return <div className="human-page manager-page">
    {error && <div className="banner banner-error" role="alert"><span>{error}</span><button type="button" className="btn btn-ghost" onClick={() => setError('')}>Dismiss</button></div>}
    <div className="remote-chat-layout">
      <aside className="panel agent-roster" aria-label="Sessions">
        <div className="agent-roster-heading"><h2>Sessions</h2><button type="button" className="btn btn-ghost" onClick={() => void refreshSessions()} disabled={link !== 'live'}>Refresh</button></div>
        {sessionsStale && <p className="agent-meta manager-stale">Shown as of the last check; the manager is busy.</p>}
        {[{label: 'On this machine', items: local}, {label: 'In the cloud', items: cloud}].filter(group => group.items.length).map(group => <section key={group.label} className="agent-roster-group" aria-label={group.label}>
          <h2 className="agent-roster-heading">{group.label}</h2>
          {group.items.map(session => {
            const state = live[session.id]?.state ?? session.state;
            const activity = live[session.id]?.activity;
            const queued = live[session.id]?.queued ?? 0;
            const working = state === 'running' || state === 'waiting';
            return <div key={session.id} className={`agent-row manager-session${working ? ' is-working' : ''}`} title={session.workdir || session.namespace || session.id}>
              <AgentAvatar name={session.title || session.harness} identity={session.id}/>
              <span className="agent-row-copy">
                <span className="agent-name">{session.title || session.id}</span>
                <span className="agent-meta">{session.harness} · {state}{queued ? ` · ${queued} queued` : ''}</span>
                {working && activity && <span className="manager-session-activity">{activity}</span>}
              </span>
              {working && <button type="button" className="btn btn-ghost manager-session-stop" onClick={() => void stopSession(session.id)} aria-label={`Stop ${session.title || session.id}`}>Stop</button>}
            </div>;
          })}
        </section>)}
        {!sessions.length && <p className="agent-empty">{link === 'live' ? 'No sessions yet. Ask the manager to start one.' : 'Connecting to the manager…'}</p>}
      </aside>
      <section className="panel entity-chat-main">
        <header className="agent-chat-header">
          <div className="agent-chat-identity"><AgentAvatar name="One chat" identity={hello?.host.id ?? 'manager'} size="lg"/>
            <div><h2 className="agent-name">Manager</h2><p className="agent-meta">{hello ? `${hello.host.name} · onechat ${hello.host.version}${hostStatus?.mode === 'wsl' ? ` · in WSL${hostStatus.distro ? ` (${hostStatus.distro})` : ''}` : ''}` : 'Connecting…'}</p></div>
          </div>
          {link === 'live' ? <span className="chat-sync chat-sync-live" title="Connected to the manager on this machine">{busy ? 'Working' : 'Live'}</span>
            : <span className="chat-sync chat-sync-retrying">Reconnecting…</span>}
        </header>
        <Transcript hostId={hello?.host.id ?? 'manager'} turns={turns} followups={followups} sessionTitle={id => sessions.find(item => item.id === id)?.title} details={details} running={running} elapsed={elapsed}
          onOpen={turnId => void loadDetail(turnId)} jumpToBottom={jumpToBottom}/>
        <form className="chat-composer plate" onSubmit={event => void send(event)}>
          <label className="field"><span className="label">Message</span><textarea className="textarea chat-composer-input" aria-label="Message" rows={3} value={draft}
            onChange={event => { setDraft(event.target.value); saveDraft(event.target.value); }}
            onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); } }}
            placeholder="Tell the manager what you need…"/></label>
          <div className="chat-composer-actions">
            <span className="chat-composer-hint">{queuedAhead ? `Queued: the manager reads it after ${queuedAhead === 1 ? 'the message before it' : `${queuedAhead} messages`}` : busy ? 'The manager is working; a new message waits its turn' : 'Enter to send · Shift + Enter for a new line'}</span>
            {running && <button type="button" className="btn btn-warn" onClick={() => void cancel()}>Cancel</button>}
            <button className="btn btn-primary" disabled={link !== 'live' || sending || !draft.trim()}>{sending ? 'Sending…' : busy ? 'Queue' : 'Send'}</button>
          </div>
        </form>
      </section>
    </div>
  </div>;
}

/** The scrolling conversation; it mounts with its scroller so the follow-the-bottom hook can attach. */
function Transcript({hostId, turns, followups, sessionTitle, details, running, elapsed, onOpen, jumpToBottom}: {
  hostId: string; turns: OnechatTurnSummary[]; followups: OnechatFollowup[]; sessionTitle: (id: string) => string | undefined;
  details: Record<string, TurnView>; running?: TurnView; elapsed: number;
  onOpen: (turnId: string) => void; jumpToBottom: {current: () => void};
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const follow = useStickToBottom(scroller, {
    resetKey: hostId,
    contentKey: `${turns.length}:${followups.length}:${running?.lastSeq ?? 0}:${running?.answer?.length ?? 0}:${Object.keys(details).length}`,
    arrivals: turns.length + followups.length + (running ? 1 : 0),
  });
  const timeline = [
    ...turns.map(item => ({at: item.at, key: `turn-${item.turnId}`, node: <Exchange key={item.turnId} summary={item} detail={details[item.turnId]} onOpen={() => onOpen(item.turnId)}/>})),
    ...followups.map(item => ({at: item.at, key: `followup-${item.id}`, node: <Followup key={item.id} item={item} title={item.sessionId ? sessionTitle(item.sessionId) : undefined}/>})),
  ].sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  jumpToBottom.current = follow.jumpToBottom;
  return <div className="chat-messages" aria-live="polite" ref={scroller}>
    {!turns.length && !running && <div className="agent-empty">
      <AgentAvatar name="One chat" identity={hostId} size="lg"/>
      <h3>One chat for all your sessions</h3>
      <p>Say what you want done. The manager sends it to the session that holds the work, starts a new one, or asks you when it is not sure.</p>
    </div>}
    {timeline.map(item => item.node)}
    {running && <RunningTurn turn={running} elapsed={elapsed}/>}
    {follow.unseen > 0 && <button type="button" className="chat-jump" onClick={follow.jumpToBottom}>{follow.unseen === 1 ? '1 new message' : `${follow.unseen} new messages`}</button>}
  </div>;
}

function Exchange({summary, detail, onOpen}: {summary: OnechatTurnSummary; detail?: TurnView; onOpen: () => void}) {
  return <>
    <article className="chat-bubble chat-bubble-user">
      <header className="chat-bubble-header"><span className="chat-bubble-role">{fromPhone(detail?.by?.kind) ? 'You · iPhone' : 'You'}</span></header>
      <div className="chat-bubble-body"><MarkdownBody content={summary.message}/></div>
    </article>
    <article className="chat-bubble chat-bubble-run">
      <header className="chat-bubble-header"><span className="chat-bubble-role">Manager</span></header>
      {summary.error && !summary.answer ? <p className="remote-chat-caption">{stopReason(summary.error)}</p> : <div className="chat-bubble-body"><MarkdownBody content={summary.answer}/></div>}
      <details className="chat-reasoning manager-detail" onToggle={event => { if (event.currentTarget.open) onOpen(); }}>
        <summary>What the manager did{summary.actionCount ? ` (${summary.actionCount})` : ''}</summary>
        {detail ? <TurnSteps turn={detail}/> : <p className="chat-reasoning-body">Loading…</p>}
      </details>
    </article>
  </>;
}

function Followup({item, title}: {item: OnechatFollowup; title?: string}) {
  return <article className="chat-bubble chat-bubble-run manager-followup">
    <header className="chat-bubble-header"><span className="chat-bubble-role">Manager{title ? ` · ${title}` : ''}</span></header>
    <div className="chat-bubble-body"><MarkdownBody content={item.text}/></div>
  </article>;
}

function RunningTurn({turn, elapsed}: {turn: TurnView; elapsed: number}) {
  const step = turn.answer ? 'Answer ready' : turn.actions.some(item => item.status === 'running') ? 'Working in a session' : turn.decision ? 'Writing the answer' : turn.sessionCount !== undefined ? 'Deciding where this goes' : 'Reading your sessions';
  return <>
    <article className="chat-bubble chat-bubble-user" aria-label="Your message">
      <header className="chat-bubble-header"><span className="chat-bubble-role">{fromPhone(turn.by?.kind) ? 'You · iPhone' : 'You'}</span></header>
      <div className="chat-bubble-body"><MarkdownBody content={turn.text}/></div>
    </article>
    <section className="turn-activity manager-rail" aria-label="Manager activity">
      <div className="turn-activity-heading">
        <span className="turn-activity-indicator" aria-hidden="true"/>
        <div><span className="turn-activity-agent">Manager</span><p role="status">{step}</p></div>
        <span className="turn-activity-elapsed" aria-hidden="true">{elapsed < 60 ? `${elapsed}s` : `${Math.floor(elapsed / 60)}m ${elapsed % 60}s`}</span>
      </div>
      <TurnSteps turn={turn}/>
    </section>
    {turn.answer && <article className="chat-bubble chat-bubble-run chat-bubble-enter">
      <header className="chat-bubble-header"><span className="chat-bubble-role">Manager</span></header>
      <div className="chat-bubble-body"><MarkdownBody content={turn.answer}/></div>
    </article>}
  </>;
}

function TurnSteps({turn}: {turn: TurnView}) {
  return <ol className="manager-steps">
    {turn.sessionCount !== undefined && <li className="manager-step"><span className="manager-step-mark done" aria-hidden="true"/>Looked at {turn.sessionCount} {turn.sessionCount === 1 ? 'session' : 'sessions'}</li>}
    {turn.decision && <li className="manager-step"><span className="manager-step-mark done" aria-hidden="true"/><span>Decided{turn.decision.reasoning_summary ? `: ${turn.decision.reasoning_summary}` : ''}{intentLabel(turn.decision.intent) && <span className="manager-step-meta"> · read as {intentLabel(turn.decision.intent).toLowerCase()}</span>}</span></li>}
    {turn.validationErrors.length > 0 && <li className="manager-step failed"><span className="manager-step-mark failed" aria-hidden="true"/>Could not route safely: {turn.validationErrors.join('; ')}</li>}
    {turn.actions.map(item => <li key={item.index} className={`manager-step ${item.status}`}>
      <span className={`manager-step-mark ${item.status}`} aria-hidden="true"/>
      <div className="manager-step-copy">
        <span>{actionLabel(item)}{item.harness ? <span className="manager-step-meta"> · {item.harness}{item.location ? ` · ${item.location === 'local' ? 'this machine' : 'cloud'}` : ''}</span> : null}</span>
        {item.result?.error && <p className="manager-step-error">{item.result.error}</p>}
        {item.result?.reply && item.action.type !== 'answer' && item.action.type !== 'ask_user' && <details className="manager-reply"><summary>Reply</summary><div className="chat-bubble-body"><MarkdownBody content={item.result.reply}/></div>{item.result.truncated && <p className="agent-meta">Shortened here; the full reply is in the trace.</p>}</details>}
      </div>
    </li>)}
    {turn.error && <li className="manager-step failed"><span className="manager-step-mark failed" aria-hidden="true"/>{stopReason(turn.error)}</li>}
  </ol>;
}

function ManagerOffline({link, status, onStarted}: {link: Link; status: OnechatHostStatus | null; onStarted: (status: OnechatHostStatus) => void}) {
  const [starting, setStarting] = useState(false);
  const [failure, setFailure] = useState('');
  const where = status?.mode === 'wsl' ? `in WSL${status.distro ? ` (${status.distro})` : ''}` : 'on this machine';
  async function start() {
    setStarting(true); setFailure('');
    try {
      const next = await startOnechatHost();
      if (next.running) onStarted(next);
      else setFailure(next.message || 'The manager did not start. Check onechat daemon logs.');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  }
  const title = link === 'checking' ? 'Looking for the manager' : link === 'missing' ? 'Install the manager' : link === 'unconfigured' ? 'Set up the manager' : 'Start the manager';
  return <div className="human-page manager-page">
    <section className="panel token-gate manager-offline" aria-live="polite">
      <DesktopIcon kind="manager"/>
      <span className="page-kicker">One chat · {where}</span>
      <h1>{title}</h1>
      {link === 'stopped' && <>
        <p>The manager is a daemon that runs {where} and keeps working when Desktop is closed, so your sessions and your phone can always reach it.</p>
        <div className="manager-offline-actions">
          <button type="button" className="btn btn-primary" onClick={() => void start()} disabled={starting}>{starting ? 'Starting…' : 'Start the manager'}</button>
        </div>
        <p className="muted">To start it with WSL and keep it running, run <code>onechat daemon bootstrap</code> once{status?.mode === 'wsl' ? ' inside WSL' : ''}.</p>
      </>}
      {link === 'missing' && <>
        <p>Desktop could not find <code>onechat</code> {where}.</p>
        <pre className="hint">go build -o ~/.local/bin/onechat ./cmd/onechat</pre>
        <p className="muted">Run it from an anvil-agents checkout{status?.mode === 'wsl' ? ' inside WSL' : ''}, so <code>onechat</code> is on the PATH there.</p>
      </>}
      {link === 'unconfigured' && <>
        <p>The manager needs its config at <code>~/.config/onechat/onechat.yaml</code>{status?.mode === 'wsl' ? ' inside WSL' : ''}. Start from <code>examples/onechat/onechat.prod.yaml</code>.</p>
      </>}
      {failure && <p className="manager-offline-error" role="alert">{failure}</p>}
      {link !== 'checking' && !failure && status?.message && <p className="muted">{status.message}</p>}
    </section>
  </div>;
}
