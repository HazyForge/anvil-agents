import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { UIConfig } from '../auth/config';
import { APIError, backendKindFromComposition, harnessRefFromRunProfile, listRunProfiles, type CompositionDocument } from '../api/client';
import { createRemoteThread, getRemoteThread, listRemoteHarnesses, listRemoteThreads, sendRemoteMessage, threadHarness, type RemoteThread, type RemoteThreadDetail } from '../api/remoteChat';
import { loadNamespace, saveNamespace } from '../state/namespace';
import { readChatDraft, saveChatDraft, readSelectedChat, saveSelectedChat, readNewChatConfig, saveNewChatConfig, type NewChatConfig } from '../state/chatWorkspace';
import { RemoteTurnActivity } from '../components/RemoteTurnActivity';
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
  const [initializing, setInitializing] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [optimistic, setOptimistic] = useState<{content: string; requestId?: string} | null>(null);
  const [sendPhase, setSendPhase] = useState<'saving' | 'sending' | 'unconfirmed'>('sending');
  const [rawActivityOpen, setRawActivityOpen] = useState(false);
  const restoredNamespace = useRef('');
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
  const optimisticVisible = optimistic && !detail?.turns?.some(turn => optimistic.requestId && turn.requestId === optimistic.requestId);

  useEffect(() => {
    const controller = new AbortController();
    const namespaceChanged = loadedNamespace.current !== namespace;
    loadedNamespace.current = namespace;
    if (namespaceChanged) {
      restoredNamespace.current = '';
      setProfiles([]); setHarnesses([]); setThreads([]); setDetail(null); setThreadID('');
      setInitializing(true); setUnavailable(false); setOptimistic(null); setRawActivityOpen(false);
      setProfile(''); setHarness(''); setCoordinate(false); setPeers([]); setDraft(''); setError(''); pending.current = null;
    }
    if (!enabled) { setInitializing(false); return; }
    setLoading(true);
    void Promise.all([
      listRunProfiles(token, namespace, controller.signal),
      listRemoteHarnesses(token, namespace, controller.signal),
      listRemoteThreads(token, namespace, controller.signal),
    ]).then(([ps, hs, ts]) => {
      if (controller.signal.aborted) return;
      setProfiles(ps); setHarnesses(hs); setThreads(ts);
      if (restoredNamespace.current !== namespace) {
        restoredNamespace.current = namespace;
        const selected = readSelectedChat(namespace);
        if (selected) {
          const thread = ts.find(item => item.id === selected) ?? {
            id: selected, namespace, title: 'Saved conversation', mode: 'persona',
            createdAt: '', updatedAt: '', createdBy: '',
          };
          if (!ts.some(item => item.id === selected)) setThreads([thread, ...ts]);
          openThread(thread);
        } else restoreNewChat(ps);
      }
      setInitializing(false);
    }).catch(err => {
      if (!controller.signal.aborted) { setError(formatTurnError(err)); setInitializing(false); }
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
        setDetail(next); applyThreadIdentity(next); setUnavailable(false);
        const unresolved = readPendingSend(namespace, threadID);
        if (unresolved && next.turns?.some(t => t.requestId === unresolved.id)) {
          clearPendingSend(namespace, threadID); pending.current = null; setError('');
          setOptimistic(previous => previous?.requestId === unresolved.id ? null : previous);
          setDraft(previous => {
            if (previous.trim() !== unresolved.content) return previous;
            saveChatDraft(namespace, threadID, ''); return '';
          });
        }
        setThreads(previous => [next, ...previous.filter(t => t.id !== next.id)]);
      } catch (err) {
        if (!controller.signal.aborted) {
          if (err instanceof APIError && err.status === 401 && !refreshed) {
            refreshed = true;
            const access = await ensureAccessToken();
            if (controller.signal.aborted) return;
            if (access && access !== pollToken) {pollToken = access; return;}
          }
          if (err instanceof APIError && (err.status === 401 || err.status === 404 || err.status === 403)) stopped = true;
          if (err instanceof APIError && (err.status === 403 || err.status === 404)) { setUnavailable(true); setDetail(null); }
          setError(formatTurnError(err));
        }
      } finally {
        if (!controller.signal.aborted && !stopped) timer = setTimeout(() => void poll(), 2500);
      }
    };
    void poll();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [token, namespace, threadID]);

  useEffect(() => { end.current?.scrollIntoView({block: 'nearest'}); }, [detail?.messages.length, active?.status, optimistic?.content]);

  function applyThreadIdentity(thread: RemoteThread) {
    setProfile(thread.profileName || ''); setHarness(threadHarness(thread));
    setMode(thread.mode === 'fleet' ? 'fleet' : 'persona');
    const coordination = (thread.metadata as {coordination?: {enabled?: boolean; allowedProfiles?: string[]}} | undefined)?.coordination;
    setCoordinate(Boolean(coordination?.enabled)); setPeers(coordination?.allowedProfiles ?? []);
  }
  function openThread(thread: RemoteThread) {
    if (thread.id === threadID) return;
    saveSelectedChat(namespace, thread.id);
    setThreadID(thread.id); setDetail(null); setUnavailable(false); setRawActivityOpen(false); applyThreadIdentity(thread);
    pending.current = readPendingSend(namespace, thread.id);
    setOptimistic(pending.current ? {content: pending.current.content, requestId: pending.current.id} : null);
    setSendPhase('unconfirmed'); setError('');
    setDraft(readChatDraft(namespace, thread.id) ?? pending.current?.content ?? '');
  }
  function restoreNewChat(choices = profiles) {
    const saved = readNewChatConfig(namespace);
    setProfile(saved && choices.some(p => p.metadata.name === saved.profile) ? saved.profile : '');
    setHarness(saved?.harness ?? ''); setMode(saved?.mode ?? 'persona');
    setCoordinate(saved?.coordinate ?? false); setPeers((saved?.peers ?? []).filter(peer => choices.some(p => p.metadata.name === peer)));
    setDraft(readChatDraft(namespace, '') ?? '');
  }
  function newChat() {
    saveSelectedChat(namespace, '');
    setThreadID(''); setDetail(null); setError(''); setUnavailable(false); setOptimistic(null); setRawActivityOpen(false);
    pending.current = null; restoreNewChat();
  }
  function newConfig(): NewChatConfig { return {profile, harness, mode, coordinate, peers}; }
  function changeConfig(patch: Partial<NewChatConfig>) {
    const next = {...newConfig(), ...patch};
    if (patch.profile !== undefined) next.peers = next.peers.filter(peer => peer !== patch.profile);
    setProfile(next.profile); setHarness(next.harness); setMode(next.mode);
    setCoordinate(next.coordinate); setPeers(next.peers); saveNewChatConfig(namespace, next);
  }
  function changeDraft(content: string) {
    setDraft(content); saveChatDraft(namespace, threadID, content);
    if (!threadID) saveNewChatConfig(namespace, newConfig());
  }
  async function submit(event?: FormEvent) {
    event?.preventDefault();
    const content = draft.trim();
    if (!content || busy || active || unavailable || initializing || (!profile && !harness) || !enabled || (coordinate && !peers.length)) return;
    setBusy(true); setError(''); setOptimistic({content}); setSendPhase(threadID ? 'sending' : 'saving');
    let target = threadID;
    try {
      if (!target) {
        const thread = await createRemoteThread(token, namespace, {
          profileName: profile || undefined, harnessProfileName: harness || undefined, mode,
          metadata: coordinate ? {coordination: {enabled: true, allowedProfiles: peers}} : undefined,
          title: `${mode === 'fleet' ? 'Manager · ' : ''}${profile || harness}`,
        });
        target = thread.id; setThreadID(target); saveSelectedChat(namespace, target);
        saveChatDraft(namespace, target, draft); saveChatDraft(namespace, '', '');
        setThreads(previous => [thread, ...previous]);
      }
      if (!pending.current || pending.current.content !== content || pending.current.threadID !== target) {
        pending.current = rememberPendingSend(namespace, target, content);
      }
      setOptimistic({content, requestId: pending.current.id}); setSendPhase('sending');
      const result = await sendRemoteMessage(token, namespace, target, content, pending.current.id);
      setDraft(''); saveChatDraft(namespace, target, ''); clearPendingSend(namespace, target); pending.current = null; setOptimistic(null);
      setDetail(previous => {
        const prior = previous?.id === result.thread.id ? previous : null;
        const messages = prior?.messages ?? [];
        return {...prior, ...result.thread,
          messages: messages.some(message => message.id === result.user.id) ? messages : [...messages, result.user].sort((a, b) => a.sequence - b.sequence),
          activeTurn: ['waiting', 'queued', 'running'].includes(result.turn.status) ? result.turn : undefined,
        };
      });
    } catch (err) { setError(formatTurnError(err)); setSendPhase('unconfirmed'); }
    finally { setBusy(false); }
  }

  const configuration = (
    <div className="remote-chat-config">
          <label className="field"><span className="label">Agent</span><select className="input" aria-label="Agent" value={profile} disabled={busy || Boolean(threadID)} onChange={e => changeConfig({profile: e.target.value})}>
            <option value="">Harness only</option>{profiles.map(p => <option key={p.metadata.name}>{p.metadata.name}</option>)}
          </select></label>
          <label className="field"><span className="label">Remote harness</span><select className="input" aria-label="Remote harness" value={harness} disabled={busy || Boolean(threadID)} onChange={e => changeConfig({harness: e.target.value})}>
            <option value="">Agent's configured harness</option>{harnesses.map(h => <option key={h.metadata.name} value={h.metadata.name}>{h.metadata.name} ({backendKindFromComposition(h) || 'custom'})</option>)}
          </select></label>
          <label className="field"><span className="label">Role</span><select className="input" aria-label="Role" value={mode} disabled={busy || Boolean(threadID)} onChange={e => changeConfig({mode: e.target.value as 'persona' | 'fleet'})}>
            <option value="persona">Agent</option><option value="fleet">Manager</option>
          </select></label>
          <fieldset className="remote-coordination" disabled={busy || Boolean(threadID)}>
            <label><input type="checkbox" checked={coordinate} onChange={e => changeConfig({coordinate: e.target.checked})}/> Allow this agent to delegate messages</label>
            {coordinate && <div className="remote-peer-options"><span>Allowed peers</span>{profiles.filter(p => p.metadata.name !== profile).map(p => <label key={p.metadata.name}><input type="checkbox" checked={peers.includes(p.metadata.name)} onChange={e => changeConfig({peers: e.target.checked ? [...peers, p.metadata.name] : peers.filter(n => n !== p.metadata.name)})}/>{p.metadata.name}</label>)}</div>}
          </fieldset>
          <p className="remote-chat-caption">{backend ? `Harness: ${backend}. ` : ''}Messages and replies are saved. Each turn starts a remote runner with this conversation's history.</p>
        </div>
  );

  return <div className="human-page">
    <div className="page-header"><div><h1 className="page-title">Chat</h1>
      <p className="page-sub">Talk to a remote agent using its configured harness, or choose another harness for a new conversation.</p>
    </div></div>
    {!enabled && <div className="banner banner-error">Remote chat is not enabled on this server.</div>}
    {error && <div className="banner banner-error" role="alert">{error}</div>}
    <div className="remote-chat-layout">
      <aside className="panel remote-chat-sidebar" aria-label="Conversations">
        <label className="field"><span className="label">Namespace</span><select className="input" aria-label="Namespace" value={namespace} disabled={busy} onChange={e => {saveNamespace(e.target.value); setThreadID(''); setDetail(null); setInitializing(true); setNamespace(e.target.value);}}>
          {namespaces.map(ns => <option key={ns}>{ns}</option>)}
        </select></label>
        <button className="btn" onClick={newChat} disabled={busy}>New conversation</button>
        {threads.map(thread => <button className={`remote-thread ${thread.id === threadID ? 'remote-thread-selected' : ''}`} key={thread.id} onClick={() => openThread(thread)} disabled={busy}>
          <strong>{thread.title || thread.profileName}</strong><small>{thread.profileName}{threadHarness(thread) ? ` · ${threadHarness(thread)}` : ''}</small>
        </button>)}
        {!threads.length && <p className="muted">{loading ? 'Loading conversations…' : 'Your conversations will appear here.'}</p>}
      </aside>
      <section className="panel entity-chat-main">
        {threadID ? <details className="remote-chat-settings" key={threadID}>
          <summary>{profile || 'Remote harness'} · {backend || effectiveHarness || 'Configured harness'} · {mode === 'fleet' ? 'Manager' : 'Agent'}</summary>
          {configuration}
        </details> : configuration}
        <div className="chat-messages" aria-live="polite">
          {!detail?.messages.length && !optimistic && <div className="empty">{unavailable ? 'This conversation is unavailable. Your draft is preserved; you can keep editing it or start a new conversation.' : threadID ? 'Loading conversation…' : 'Choose an agent and send a message.'}</div>}
          {detail?.messages.filter(m => m.role !== 'system').map(message => <article key={message.id} className={`chat-bubble ${message.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}`}>
            <header className="chat-bubble-header"><span className="chat-bubble-role">{message.role === 'user' ? ((message.metadata as {authorProfile?: string} | undefined)?.authorProfile || 'You') : message.role === 'tool' ? 'Coordination' : detail.profileName || 'Agent'}</span></header>
            <pre className="chat-bubble-body">{message.content}</pre>
          </article>)}
          {optimisticVisible && <article className="chat-bubble chat-bubble-user" aria-label="Your pending message">
            <header className="chat-bubble-header"><span className="chat-bubble-role">You</span></header>
            <pre className="chat-bubble-body">{optimistic.content}</pre>
          </article>}
          {optimistic && <div className="remote-turn-status" role="status">{busy ? (sendPhase === 'saving' ? 'Saving conversation…' : 'Sending message…') : 'Message not confirmed. Retry to check delivery.'}</div>}
          <RemoteTurnActivity token={token} namespace={namespace} turn={active ?? (optimistic ? undefined : detail?.turns?.at(-1))} agentLabel={detail?.profileName || profile || harness}/>

          {detail?.turns?.flatMap(t => t.delegates ?? []).map(delivery => <div className="remote-turn-status" key={delivery.turnId}>{delivery.status === 'succeeded' ? `${delivery.profileName} replied.` : delivery.status === 'running' ? `${delivery.profileName} is working.` : delivery.status === 'failed' ? `${delivery.profileName} could not finish.` : `Message saved for ${delivery.profileName}.`} <button className="btn btn-ghost" disabled={busy} onClick={() => {const thread = threads.find(t => t.id === delivery.threadId); openThread(thread ?? {id: delivery.threadId, namespace, profileName: delivery.profileName, mode: 'persona', title: delivery.profileName, createdAt: '', updatedAt: '', createdBy: ''});}}>Open peer conversation</button></div>)}
          {detail?.turns?.filter(t => t.status === 'failed').map(t => <div key={t.id} className="banner banner-error">Turn failed: {t.error || t.runName}</div>)}
          <div ref={end}/>
        </div>
        <form className="chat-composer" onSubmit={e => void submit(e)}>
          <label className="field"><span className="label">Message</span><textarea className="textarea chat-composer-input" rows={3} aria-label="Message" value={draft} disabled={busy || !enabled || initializing} onChange={e => changeDraft(e.target.value)} onKeyDown={e => {if(e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {e.preventDefault(); void submit();}}} placeholder="Message this agent…"/></label>
          <div className="chat-composer-actions"><button className="btn btn-primary" disabled={busy || Boolean(active) || unavailable || initializing || !draft.trim() || (!profile && !harness) || !enabled || (coordinate && !peers.length)}>{busy ? 'Sending…' : active ? 'Waiting for reply…' : 'Send'}</button></div>
        </form>
        {active?.runName && <details className="remote-run-details" open={rawActivityOpen} onToggle={e => setRawActivityOpen(e.currentTarget.open)}><summary>Runner activity</summary>{rawActivityOpen && <LiveStream token={token} namespace={namespace} name={active.runName}/>}</details>}
      </section>
    </div>
  </div>;
}
