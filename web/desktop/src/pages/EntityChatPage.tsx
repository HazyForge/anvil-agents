import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { UIConfig } from '../auth/config';
import { APIError, backendKindFromComposition, harnessRefFromRunProfile, listRunProfiles, type CompositionDocument } from '../api/client';
import { createRemoteThread, getRemoteThread, listRemoteHarnesses, listRemoteThreads, sendRemoteMessage, threadHarness, type RemoteThread, type RemoteThreadDetail } from '../api/remoteChat';
import { loadNamespace, saveNamespace } from '../state/namespace';
import { LiveStream } from '../components/LiveStream';
import { ensureAccessToken } from '../auth/oidc';
import { readPendingSend, rememberPendingSend, clearPendingSend } from '../api/pendingChat';
import { formatTurnError } from '../wrapper/turn';

interface Props { token: string; config: UIConfig }

export function EntityChatPage({token, config}: Props) {
  const [namespace, setNamespace] = useState(() => loadNamespace(config.defaultNamespaces[0] || 'agents'));
  const [profiles, setProfiles] = useState<CompositionDocument[]>([]);
  const [harnesses, setHarnesses] = useState<CompositionDocument[]>([]);
  const [threads, setThreads] = useState<RemoteThread[]>([]);
  const [profile, setProfile] = useState('');
  const [harness, setHarness] = useState('');
  const [coordinate, setCoordinate] = useState(false);
  const [peers, setPeers] = useState<string[]>([]);
  const [mode, setMode] = useState<'persona' | 'fleet'>('persona');
  const [threadID, setThreadID] = useState('');
  const [detail, setDetail] = useState<RemoteThreadDetail | null>(null);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const loadedNamespace = useRef('');
  const pending = useRef<{content: string; id: string; threadID: string} | null>(null);
  const end = useRef<HTMLDivElement>(null);
  const enabled = Boolean(config.chat?.enabled);
  const active = detail?.activeTurn ?? detail?.turns?.find(t => t.status === 'waiting' || t.status === 'queued' || t.status === 'running');
  const selectedProfile = profiles.find(p => p.metadata.name === profile);
  const effectiveHarness = harness || harnessRefFromRunProfile(selectedProfile);
  const selectedHarness = harnesses.find(h => h.metadata.name === effectiveHarness);
  const backend = backendKindFromComposition(selectedHarness) || backendKindFromComposition(selectedProfile);
  const namespaces = [...new Set([...config.defaultNamespaces, namespace])];

  useEffect(() => {
    const controller = new AbortController();
    const namespaceChanged = loadedNamespace.current !== namespace;
    loadedNamespace.current = namespace;
    if (namespaceChanged) {
      setProfiles([]); setHarnesses([]); setThreads([]); setDetail(null); setThreadID('');
      setProfile(''); setHarness(''); setCoordinate(false); setPeers([]); setDraft(''); setError(''); pending.current = null;
    }
    if (!enabled) return;
    setLoading(true);
    void Promise.all([
      listRunProfiles(token, namespace, controller.signal),
      listRemoteHarnesses(token, namespace, controller.signal),
      listRemoteThreads(token, namespace, controller.signal),
    ]).then(([ps, hs, ts]) => {
      if (controller.signal.aborted) return;
      setProfiles(ps); setHarnesses(hs); setThreads(ts);
      if (namespaceChanged) setProfile(ps[0]?.metadata.name ?? '');
    }).catch(err => {
      if (!controller.signal.aborted) setError(formatTurnError(err));
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [token, namespace, enabled]);

  useEffect(() => {
    if (!threadID) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    let pollToken = token;
    let refreshed = false;
    const poll = async () => {
      try {
        const next = await getRemoteThread(pollToken, namespace, threadID, controller.signal);
        if (controller.signal.aborted) return;
        setDetail(next);
        const unresolved = readPendingSend(namespace, threadID);
        if (unresolved && next.turns?.some(t => t.requestId === unresolved.id)) {
          clearPendingSend(namespace, threadID); pending.current = null;
          setDraft(previous => previous === unresolved.content ? '' : previous);
        }
        setThreads(previous => [next, ...previous.filter(t => t.id !== next.id)]);
      } catch (err) {
        if (!controller.signal.aborted) {
          if (err instanceof APIError && err.status === 401 && !refreshed) {
            refreshed = true;
            const access = await ensureAccessToken();
            if (access && access !== pollToken) {pollToken = access; return;}
          }
          if (err instanceof APIError && (err.status === 401 || err.status === 404 || err.status === 403)) stopped = true;
          setError(formatTurnError(err));
        }
      } finally {
        if (!controller.signal.aborted && !stopped) timer = setTimeout(() => void poll(), 2500);
      }
    };
    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [token, namespace, threadID]);

  useEffect(() => { end.current?.scrollIntoView({block: 'nearest'}); }, [detail?.messages.length, active?.status]);

  function openThread(thread: RemoteThread) {
    setThreadID(thread.id); setDetail(null); setProfile(thread.profileName || '');
    setHarness(threadHarness(thread)); setMode(thread.mode === 'fleet' ? 'fleet' : 'persona');
    const coordination = (thread.metadata as {coordination?: {enabled?: boolean; allowedProfiles?: string[]}} | undefined)?.coordination;
    setCoordinate(Boolean(coordination?.enabled)); setPeers(coordination?.allowedProfiles ?? []);
    pending.current = readPendingSend(namespace, thread.id);
    setError(''); setDraft(pending.current?.content ?? '');
  }
  function newChat() {
    setThreadID(''); setDetail(null); setError(''); setDraft(''); pending.current = null;
  }
  async function submit(event?: FormEvent) {
    event?.preventDefault();
    const content = draft.trim();
    if (!content || busy || active || (!profile && !harness) || !enabled || (coordinate && !peers.length)) return;
    setBusy(true); setError('');
    let target = threadID;
    try {
      if (!target) {
        const thread = await createRemoteThread(token, namespace, {
          profileName: profile || undefined, harnessProfileName: harness || undefined, mode,
          metadata: coordinate ? {coordination: {enabled: true, allowedProfiles: peers}} : undefined,
          title: `${mode === 'fleet' ? 'Manager · ' : ''}${profile || harness}`,
        });
        target = thread.id; setThreadID(target);
        setThreads(previous => [thread, ...previous]);
      }
      if (!pending.current || pending.current.content !== content || pending.current.threadID !== target) {
        pending.current = rememberPendingSend(namespace, target, content);
      }
      const result = await sendRemoteMessage(token, namespace, target, content, pending.current.id);
      setDraft(''); clearPendingSend(namespace, target); pending.current = null;
      setDetail(previous => ({...result.thread, messages: [...(previous?.messages ?? []).filter(m => m.id !== result.user.id), result.user], activeTurn: result.turn}));
    } catch (err) { setError(formatTurnError(err)); }
    finally { setBusy(false); }
  }

  return <div className="human-page">
    <div className="page-header"><div><h1 className="page-title">Chat</h1>
      <p className="page-sub">Talk to a remote agent using its configured harness, or choose another harness for a new conversation.</p>
    </div></div>
    {!enabled && <div className="banner banner-error">Remote chat is not enabled on this server.</div>}
    {error && <div className="banner banner-error" role="alert">{error}</div>}
    <div className="remote-chat-layout">
      <aside className="panel remote-chat-sidebar" aria-label="Conversations">
        <label className="field"><span className="label">Namespace</span><select className="input" aria-label="Namespace" value={namespace} disabled={busy} onChange={e => {saveNamespace(e.target.value); setThreadID(''); setDetail(null); setNamespace(e.target.value);}}>
          {namespaces.map(ns => <option key={ns}>{ns}</option>)}
        </select></label>
        <button className="btn" onClick={newChat} disabled={busy}>New conversation</button>
        {threads.map(thread => <button className={`remote-thread ${thread.id === threadID ? 'remote-thread-selected' : ''}`} key={thread.id} onClick={() => openThread(thread)} disabled={busy}>
          <strong>{thread.title || thread.profileName}</strong><small>{thread.profileName}{threadHarness(thread) ? ` · ${threadHarness(thread)}` : ''}</small>
        </button>)}
        {!threads.length && <p className="muted">{loading ? 'Loading conversations…' : 'Your conversations will appear here.'}</p>}
      </aside>
      <section className="panel entity-chat-main">
        <div className="remote-chat-config">
          <label className="field"><span className="label">Agent</span><select className="input" aria-label="Agent" value={profile} disabled={busy || Boolean(threadID)} onChange={e => setProfile(e.target.value)}>
            <option value="">Harness only</option>{profiles.map(p => <option key={p.metadata.name}>{p.metadata.name}</option>)}
          </select></label>
          <label className="field"><span className="label">Remote harness</span><select className="input" aria-label="Remote harness" value={harness} disabled={busy || Boolean(threadID)} onChange={e => setHarness(e.target.value)}>
            <option value="">Agent's configured harness</option>{harnesses.map(h => <option key={h.metadata.name} value={h.metadata.name}>{h.metadata.name} ({backendKindFromComposition(h) || 'custom'})</option>)}
          </select></label>
          <label className="field"><span className="label">Role</span><select className="input" aria-label="Role" value={mode} disabled={busy || Boolean(threadID)} onChange={e => setMode(e.target.value as 'persona' | 'fleet')}>
            <option value="persona">Agent</option><option value="fleet">Manager</option>
          </select></label>
          <fieldset className="remote-coordination" disabled={busy || Boolean(threadID)}>
            <label><input type="checkbox" checked={coordinate} onChange={e => setCoordinate(e.target.checked)}/> Allow this agent to delegate messages</label>
            {coordinate && <div className="remote-peer-options"><span>Allowed peers</span>{profiles.filter(p => p.metadata.name !== profile).map(p => <label key={p.metadata.name}><input type="checkbox" checked={peers.includes(p.metadata.name)} onChange={e => setPeers(previous => e.target.checked ? [...previous, p.metadata.name] : previous.filter(n => n !== p.metadata.name))}/>{p.metadata.name}</label>)}</div>}
          </fieldset>
          <p className="remote-chat-caption">{backend ? `Harness: ${backend}. ` : ''}Messages and replies are saved. Each turn starts a remote runner with this conversation's history.</p>
        </div>
        <div className="chat-messages" aria-live="polite">
          {!detail?.messages.length && <div className="empty">{threadID ? 'Loading conversation…' : 'Choose an agent and send a message.'}</div>}
          {detail?.messages.filter(m => m.role !== 'system').map(message => <article key={message.id} className={`chat-bubble ${message.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}`}>
            <header className="chat-bubble-header"><span className="chat-bubble-role">{message.role === 'user' ? ((message.metadata as {authorProfile?: string} | undefined)?.authorProfile || 'You') : message.role === 'tool' ? 'Coordination' : detail.profileName || 'Agent'}</span></header>
            <pre className="chat-bubble-body">{message.content}</pre>
          </article>)}
          {active && <div className="remote-turn-status" role="status">{active.status === 'waiting' ? 'Waiting for this agent to finish its current work…' : active.status === 'queued' ? 'Queued for the remote harness…' : 'Remote harness is working…'} <code>{active.runName}</code></div>}
          {detail?.turns?.flatMap(t => t.delegates ?? []).map(delivery => <div className="remote-turn-status" key={delivery.turnId}>Message saved for {delivery.profileName}. <button className="btn btn-ghost" disabled={busy} onClick={() => {const thread = threads.find(t => t.id === delivery.threadId); openThread(thread ?? {id: delivery.threadId, namespace, profileName: delivery.profileName, mode: 'persona', title: delivery.profileName, createdAt: '', updatedAt: '', createdBy: ''});}}>Open peer conversation</button></div>)}
          {detail?.turns?.filter(t => t.status === 'failed').map(t => <div key={t.id} className="banner banner-error">Turn failed: {t.error || t.runName}</div>)}
          <div ref={end}/>
        </div>
        <form className="chat-composer" onSubmit={e => void submit(e)}>
          <label className="field"><span className="label">Message</span><textarea className="textarea chat-composer-input" rows={3} aria-label="Message" value={draft} disabled={busy || !enabled} onChange={e => setDraft(e.target.value)} onKeyDown={e => {if(e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {e.preventDefault(); void submit();}}} placeholder="Message this agent…"/></label>
          <div className="chat-composer-actions"><button className="btn btn-primary" disabled={busy || Boolean(active) || !draft.trim() || (!profile && !harness) || !enabled || (coordinate && !peers.length)}>{busy ? 'Sending…' : active ? 'Waiting for reply…' : 'Send'}</button></div>
        </form>
        {active?.runName && <details className="remote-run-details"><summary>Runner activity</summary><LiveStream token={token} namespace={namespace} name={active.runName}/></details>}
      </section>
    </div>
  </div>;
}
