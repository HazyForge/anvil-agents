import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { UIConfig } from '../auth/config';
import { APIError, backendKindFromComposition, harnessRefFromRunProfile, listRunProfiles, type CompositionDocument } from '../api/client';
import { ensureRemoteStandingThread, createRemoteThread, getRemoteThread, listRemoteHarnesses, listRemoteThreads, sendRemoteMessage, threadHarness, type RemoteThread, type RemoteThreadDetail, type RemoteTurn } from '../api/remoteChat';
import { loadNamespace, saveNamespace } from '../state/namespace';
import { readChatDraft, saveChatDraft, readSelectedChat, saveSelectedChat, readNewChatConfig, saveNewChatConfig, type NewChatConfig } from '../state/chatWorkspace';
import { RemoteTurnActivity } from '../components/RemoteTurnActivity';
import { AgentAvatar } from '../components/AgentAvatar';
import { ProjectSwitcher, remoteChatProjects } from '../components/ProjectSwitcher';
import { LiveStream } from '../components/LiveStream';
import { ensureAccessToken } from '../auth/oidc';
import { chatStartupDeadlineError } from '../api/turnWait';
import { type PendingChatSend, readPendingSend, rememberPendingSend, clearPendingSend } from '../api/pendingChat';
import { formatTurnError } from '../wrapper/turn';

interface Props { token: string; config: UIConfig }
const agentName = (name: string) => name.replace(/[-_]+/g, ' ').replace(/\bprimaris\b/gi, 'Primaris').replace(/\bagy\b/gi, 'AGY').replace(/\bprime\b/gi, 'Prime').replace(/^./, letter => letter.toUpperCase());

const turnProgress = (turn: RemoteTurn) => ({waiting: 0, queued: 1, running: 2, succeeded: 3, failed: 3})[turn.status];
const sameTurn = (a: RemoteTurn, b: RemoteTurn) => a.id === b.id || Boolean(a.requestId && a.requestId === b.requestId);
const latestSequence = (detail: RemoteThreadDetail) => Math.max(0, ...detail.messages.map(message => message.sequence));
// Reads and accepted POST receipts can arrive out of order. The append-only
// transcript and monotonic turn states must never move the visible thread back.
function regresses(next: RemoteThreadDetail, previous: RemoteThreadDetail | null) {
  if (!previous || previous.id !== next.id) return false;
  if (latestSequence(next) < latestSequence(previous)) return true;
  const nextTurns = [...(next.turns ?? []), ...(next.activeTurn ? [next.activeTurn] : [])];
  if (previous.activeTurn && !nextTurns.some(turn => sameTurn(turn, previous.activeTurn!))) return true;
  return [...(previous.turns ?? []), ...(previous.activeTurn ? [previous.activeTurn] : [])].some(before =>
    nextTurns.some(after => sameTurn(before, after) && turnProgress(after) < turnProgress(before)));
}

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
  const [historicalOutput, setHistoricalOutput] = useState<{runName: string; backend: string} | undefined>();
  const navigation = useRef(0);
  const agentRequest = useRef<AbortController | null>(null);
  const [standingIDs, setStandingIDs] = useState<Record<string, string>>({});
  const restoredNamespace = useRef('');
  const loadedNamespace = useRef('');
  const pending = useRef<PendingChatSend | null>(null);
  const submitting = useRef(false);
  const end = useRef<HTMLDivElement>(null);
  const enabled = Boolean(config.chat?.enabled);
  const lastTurn = detail?.turns?.at(-1);
  const latestFailed = !optimistic && lastTurn?.status === 'failed' ? lastTurn : undefined;
  const retryMessage = latestFailed?.error === chatStartupDeadlineError ? detail?.messages.find(message => message.id === latestFailed.userMessageId && message.role === 'user') : undefined;
  const retryPending = Boolean(optimistic && pending.current?.preserveDraft);
  const earlierFailures = detail?.turns?.filter(t => t.status === 'failed' && t.id !== latestFailed?.id) ?? [];
  const active = detail?.activeTurn ?? detail?.turns?.find(t => t.status === 'waiting' || t.status === 'queued' || t.status === 'running');
  const roster = [...profiles].sort((a, b) => Number(b.metadata.name === 'desktop-assistant') - Number(a.metadata.name === 'desktop-assistant') || a.metadata.name.localeCompare(b.metadata.name));
  const selectedProfile = profiles.find(p => p.metadata.name === profile);
  const effectiveHarness = harness || harnessRefFromRunProfile(selectedProfile);
  const selectedHarness = harnesses.find(h => h.metadata.name === effectiveHarness);
  const backend = backendKindFromComposition(selectedHarness) || backendKindFromComposition(selectedProfile);
  const currentOutputRun = active?.runName || lastTurn?.runName;
  const outputRun = historicalOutput?.runName || currentOutputRun;
  const outputMessage = detail?.messages.find(message => (message.metadata as {runName?: string} | undefined)?.runName === outputRun);
  const outputBackend = historicalOutput?.backend || (outputMessage?.metadata as {backend?: string} | undefined)?.backend || backend;
  // Opening a different thread/turn must not carry an expanded raw log panel
  // into new work. Completed turns keep the same run and remain inspectable.
  useEffect(() => { setRawActivityOpen(false); setHistoricalOutput(undefined); }, [namespace, threadID, currentOutputRun]);
  const projects = remoteChatProjects(config.defaultNamespaces, namespace);
  const selectedProject = projects.find(project => project.id === namespace);
  const optimisticVisible = optimistic && !detail?.turns?.some(turn => optimistic.requestId && turn.requestId === optimistic.requestId);

  useEffect(() => {
    const controller = new AbortController();
    const namespaceChanged = loadedNamespace.current !== namespace;
    loadedNamespace.current = namespace;
    if (namespaceChanged) {
      navigation.current++; agentRequest.current?.abort(); setStandingIDs({});
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
        } else {
          const saved = readNewChatConfig(namespace);
          const preferred = ps.find(p => p.metadata.name === saved?.profile) ?? ps.find(p => p.metadata.name === 'desktop-assistant') ?? ps[0];
          if (preferred && !saved) { void openAgent(preferred.metadata.name); return; }
          restoreNewChat(ps);
        }
      }
      setInitializing(false);
    }).catch(err => {
      if (!controller.signal.aborted) { setError(formatTurnError(err)); setInitializing(false); }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [token, namespace, enabled]);

  useEffect(() => {
    if (!threadID || initializing) return;
    const controller = new AbortController();
    const epoch = navigation.current;
    let timer: ReturnType<typeof setTimeout>;
    let stopped = false;
    let pollToken = token;
    let refreshed = false;
    const poll = async () => {
      try {
        const next = await getRemoteThread(pollToken, namespace, threadID, controller.signal);
        if (controller.signal.aborted || navigation.current !== epoch) return;
        setDetail(previous => regresses(next, previous) ? previous : next); applyThreadIdentity(next); setUnavailable(false);
        const unresolved = readPendingSend(namespace, threadID);
        if (unresolved && next.turns?.some(t => t.requestId === unresolved.id)) {
          clearPendingSend(namespace, threadID); pending.current = null; setError('');
          setOptimistic(previous => previous?.requestId === unresolved.id ? null : previous);
          setDraft(previous => {
            if (unresolved.preserveDraft || previous.trim() !== unresolved.content) return previous;
            saveChatDraft(namespace, threadID, ''); return '';
          });
        }
        setThreads(previous => [next, ...previous.filter(t => t.id !== next.id)]);
      } catch (err) {
        if (!controller.signal.aborted && navigation.current === epoch) {
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
  }, [token, namespace, threadID, initializing]);

  useEffect(() => { end.current?.scrollIntoView({block: 'nearest'}); }, [detail?.messages.length, active?.status, optimistic?.content]);

  function applyThreadIdentity(thread: RemoteThread) {
    setProfile(thread.profileName || ''); setHarness(threadHarness(thread));
    setMode(thread.mode === 'fleet' ? 'fleet' : 'persona');
    const coordination = (thread.metadata as {coordination?: {enabled?: boolean; allowedProfiles?: string[]}} | undefined)?.coordination;
    setCoordinate(Boolean(coordination?.enabled)); setPeers(coordination?.allowedProfiles ?? []);
  }
  function openThread(thread: RemoteThread, force = false) {
    if (!force && thread.id === threadID) return;
    navigation.current++; agentRequest.current?.abort(); setInitializing(false);
    saveSelectedChat(namespace, thread.id);
    setThreadID(thread.id); setDetail(null); setUnavailable(false); setRawActivityOpen(false); applyThreadIdentity(thread);
    pending.current = readPendingSend(namespace, thread.id);
    setOptimistic(pending.current ? {content: pending.current.content, requestId: pending.current.id} : null);
    setSendPhase('unconfirmed'); setError('');
    setDraft(readChatDraft(namespace, thread.id) ?? pending.current?.content ?? '');
  }
  async function openAgent(name: string) {
    if (busy) return;
    const epoch = ++navigation.current;
    agentRequest.current?.abort();
    const controller = new AbortController(); agentRequest.current = controller;
    // Keep the current workspace intact until the destination is confirmed.
    setInitializing(true); setError('');
    try {
      const thread = await ensureRemoteStandingThread(token, namespace, name, controller.signal);
      if (controller.signal.aborted || navigation.current !== epoch) return;
      setStandingIDs(previous => ({...previous, [name]: thread.id}));
      setThreads(previous => [thread, ...previous.filter(item => item.id !== thread.id)]);
      openThread(thread, true);
      const selectedEpoch = navigation.current;
      // Fetch this agent's history independently of the namespace's latest200.
      void listRemoteThreads(token, namespace, undefined, name).then(history => {
        if (navigation.current !== selectedEpoch) return;
        setThreads(previous => [...previous.filter(item => item.profileName !== name || item.id === thread.id), ...history.filter(item => item.id !== thread.id)]);
      }).catch(() => { /* Opening the standing conversation succeeded; existing history remains accessible. */ });
    } catch (err) {
      if (!controller.signal.aborted && navigation.current === epoch) {
        setError(formatTurnError(err)); setInitializing(false);
      }
    }
  }
  useEffect(() => () => { navigation.current++; agentRequest.current?.abort(); }, []);
  function restoreNewChat(choices = profiles) {
    const saved = readNewChatConfig(namespace);
    setProfile(saved ? (choices.some(p => p.metadata.name === saved.profile) ? saved.profile : '') : (choices.some(p => p.metadata.name === 'desktop-assistant') ? 'desktop-assistant' : ''));
    setHarness(saved?.harness ?? ''); setMode(saved?.mode ?? 'persona');
    setCoordinate(saved?.coordinate ?? false); setPeers((saved?.peers ?? []).filter(peer => choices.some(p => p.metadata.name === peer)));
    setDraft(readChatDraft(namespace, '') ?? '');
  }
  function newChat(harnessOnly = false) {
    navigation.current++; agentRequest.current?.abort(); setInitializing(false);
    saveSelectedChat(namespace, '');
    setThreadID(''); setDetail(null); setError(''); setUnavailable(false); setOptimistic(null); setRawActivityOpen(false);
    pending.current = null; restoreNewChat();
    if (!harnessOnly && profile) { setProfile(profile); setHarness(harness); setMode(mode); setCoordinate(coordinate); setPeers(peers); }
    if (harnessOnly) { setProfile(''); setCoordinate(false); setPeers([]); }
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
  async function submit(event?: FormEvent, retry?: {content: string; forceNew?: boolean}) {
    event?.preventDefault();
    const content = (retry?.content ?? draft).trim();
    if (!content || busy || submitting.current || (retryPending && !retry) || active || unavailable || initializing || (!profile && !harness) || !enabled || (coordinate && !peers.length)) return;
    submitting.current = true;
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
      if (retry?.forceNew || !pending.current || pending.current.content !== content || pending.current.threadID !== target) {
        pending.current = rememberPendingSend(namespace, target, content, {preserveDraft: Boolean(retry), forceNew: retry?.forceNew});
      }
      setOptimistic({content, requestId: pending.current.id}); setSendPhase('sending');
      const result = await sendRemoteMessage(token, namespace, target, content, pending.current.id);
      if (!retry) { setDraft(''); saveChatDraft(namespace, target, ''); }
      clearPendingSend(namespace, target); pending.current = null; setOptimistic(null);
      setDetail(previous => {
        const prior = previous?.id === result.thread.id ? previous : null;
        const messages = prior?.messages ?? [];
        const observed = [...(prior?.turns ?? []), ...(prior?.activeTurn ? [prior.activeTurn] : [])].find(turn => sameTurn(turn, result.turn));
        const accepted = observed && turnProgress(observed) >= turnProgress(result.turn) ? observed : result.turn;
        const turns = (prior?.turns ?? []).map(turn => sameTurn(turn, accepted) ? accepted : turn);
        if (!turns.some(turn => sameTurn(turn, accepted))) turns.push(accepted);
        return {...prior, ...result.thread,
          messages: messages.some(message => message.id === result.user.id) ? messages : [...messages, result.user].sort((a, b) => a.sequence - b.sequence),
          turns,
          activeTurn: turns.find(turn => ['waiting', 'queued', 'running'].includes(turn.status)),
        };
      });
    } catch (err) { setError(formatTurnError(err)); setSendPhase('unconfirmed'); }
    finally { submitting.current = false; setBusy(false); }
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
      <p className="page-sub">Your agents, their work, and your ongoing conversations.</p>
    </div></div>
    {!enabled && <div className="banner banner-error">Remote chat is not enabled on this server.</div>}
    {error && <div className="banner banner-error" role="alert">{error}</div>}
    <div className="remote-chat-layout">
      <aside className="panel remote-chat-sidebar agent-roster" aria-label="Agents">
        <ProjectSwitcher projects={projects} selected={namespace} disabled={busy} onChange={next => {
          if (next === namespace) return;
          navigation.current++; agentRequest.current?.abort(); saveNamespace(next);
          setProfiles([]); setHarnesses([]); setThreads([]); setProfile(''); setHarness('');
          setThreadID(''); setDetail(null); setDraft(''); setOptimistic(null); setError('');
          setInitializing(true); setNamespace(next);
        }}/>
        <h2 className="agent-roster-heading">Your agents</h2>
        {roster.map(agent => {
          const name = agent.metadata.name;
          return <button type="button" className={`agent-row ${profile === name ? 'agent-row-selected' : ''}`} key={name} title={name} aria-pressed={profile === name} onClick={() => void openAgent(name)} disabled={busy || !enabled}>
            <AgentAvatar name={agentName(name)} identity={`${namespace}/${name}`}/>
            <span className="agent-row-copy"><span className="agent-name">{agentName(name)}</span><span className="agent-meta">{profile === name && initializing ? 'Opening conversation…' : 'Standing conversation'}</span></span>
          </button>;
        })}
        {!profiles.length && <p className="agent-empty">{loading ? 'Loading agents…' : `No agents are available in ${selectedProject?.name || 'this project'}.`}</p>}
        <details className="agent-settings"><summary>Other chats</summary>
          <button type="button" className="btn btn-ghost" onClick={() => newChat(true)} disabled={busy || initializing}>Chat with a harness</button>
        </details>
      </aside>
      <section className="panel entity-chat-main">
        <header className="agent-chat-header">
          <div className="agent-chat-identity"><AgentAvatar name={agentName(profile || harness || 'Harness')} identity={`${namespace}/${profile || harness || 'harness'}`} size="lg"/>
            <div><h2 className="agent-name" title={profile || harness}>{profile || harness ? agentName(profile || harness) : 'Chat with a harness'}</h2><p className="agent-meta">{initializing ? 'Opening your conversation…' : profile ? (standingIDs[profile] === threadID ? 'Standing conversation' : 'Saved conversation') : 'Choose a remote harness below'}</p></div>
          </div>
        </header>
        <details className="agent-settings" key={threadID || 'new'} open={!threadID && !profile}>
          <summary>Conversation details</summary>
          <p className="remote-chat-caption">Project: {selectedProject?.name}. Namespace: <code>{namespace}</code></p>
          {profile && <button type="button" className="btn btn-ghost" disabled={busy || initializing || standingIDs[profile] === threadID} onClick={() => void openAgent(profile)}>Open standing conversation</button>}
          {threads.some(thread => profile ? thread.profileName === profile : !thread.profileName) && <label className="field"><span className="label">Conversation history</span><select className="input" aria-label="Conversation history" value={threadID} disabled={busy || initializing} onChange={e => {const thread = threads.find(item => item.id === e.target.value); if (thread) openThread(thread);}}>
            {!threadID && <option value="">Choose a previous conversation</option>}
            {threads.filter(thread => profile ? thread.profileName === profile : !thread.profileName).map(thread => <option value={thread.id} key={thread.id}>{standingIDs[profile] === thread.id ? 'Standing · ' : ''}{thread.title || thread.profileName || threadHarness(thread)}</option>)}
          </select></label>}
          {configuration}
          {threadID && <button type="button" className="btn btn-ghost" disabled={busy} onClick={() => newChat()}>Start a separate conversation</button>}
        </details>
        <div className="chat-messages" aria-live="polite">
          {!detail?.messages.length && !optimistic && <div className="agent-empty">
            <AgentAvatar name={agentName(profile || harness || 'Agent')} identity={`${namespace}/${profile || harness || 'harness'}`} size="lg"/>
            <h3>{unavailable ? 'Conversation unavailable' : initializing || (threadID && !detail) ? 'Opening conversation…' : profile ? `Talk with ${agentName(profile)}` : 'Chat with a harness'}</h3>
            <p>{unavailable ? 'Your draft is preserved. Select the agent to try again.' : profile ? 'Your messages and replies stay here as you work together.' : 'Choose a harness in Conversation details to begin.'}</p>
          </div>}
          {detail?.messages.map(message => {
            if (message.role === 'system') {
              const metadata = message.metadata as {kind?: string; backend?: string; runName?: string} | undefined;
              if (metadata?.kind !== 'legacy_output_unavailable' || metadata.backend !== 'hermesAgent') return null;
              return <aside key={message.id} className="remote-chat-caption" aria-label="Earlier reply unavailable"><strong>Earlier reply unavailable</strong><p>An older Hermes runner did not separate its final answer from internal output. The original message has been preserved. Reasoning and original output can be inspected in the runner output while its logs are retained.</p>{metadata.runName && /^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(metadata.runName) && <button type="button" className="btn btn-ghost" onClick={() => {setHistoricalOutput({runName: metadata.runName!, backend: 'hermesAgent'}); setRawActivityOpen(true);}}>View original runner output</button>}</aside>;
            }
            return <article key={message.id} className={`chat-bubble ${message.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}`}>
            <header className="chat-bubble-header"><span className="chat-bubble-role">{message.role === 'user' ? ((message.metadata as {authorProfile?: string} | undefined)?.authorProfile || 'You') : message.role === 'tool' ? 'Coordination' : detail.profileName || 'Agent'}</span></header>
            <pre className="chat-bubble-body">{message.content}</pre>
          </article>;
          })}
          {optimisticVisible && <article className="chat-bubble chat-bubble-user" aria-label="Your pending message">
            <header className="chat-bubble-header"><span className="chat-bubble-role">You</span></header>
            <pre className="chat-bubble-body">{optimistic.content}</pre>
          </article>}
          {optimistic && <div className="remote-turn-status" role="status">{busy ? (sendPhase === 'saving' ? 'Saving conversation…' : 'Sending message…') : 'Message not confirmed. Retry to check delivery.'}{retryPending && !busy && <button type="button" className="btn btn-ghost" disabled={Boolean(active) || unavailable || initializing} onClick={() => {if (pending.current) void submit(undefined, {content: pending.current.content});}}>Check retry delivery</button>}</div>}
          <RemoteTurnActivity token={token} namespace={namespace} turn={active ?? (optimistic ? undefined : detail?.turns?.at(-1))} recoveryPending={detail?.recoveryPending} agentLabel={detail?.profileName || profile || harness}/>

          {detail?.turns?.flatMap(t => t.delegates ?? []).map(delivery => <div className="remote-turn-status" key={delivery.turnId}>{delivery.status === 'succeeded' ? `${delivery.profileName} replied.` : delivery.status === 'running' ? `${delivery.profileName} is working.` : delivery.status === 'failed' ? `${delivery.profileName} could not finish.` : `Message saved for ${delivery.profileName}.`} <button className="btn btn-ghost" disabled={busy} onClick={() => {const thread = threads.find(t => t.id === delivery.threadId); openThread(thread ?? {id: delivery.threadId, namespace, profileName: delivery.profileName, mode: 'persona', title: delivery.profileName, createdAt: '', updatedAt: '', createdBy: ''});}}>Open peer conversation</button></div>)}
          {latestFailed && <div className="banner banner-error">Turn failed: {latestFailed.error || latestFailed.runName}{retryMessage && <button type="button" className="btn btn-ghost" disabled={busy || Boolean(active) || unavailable || initializing || !enabled} onClick={() => void submit(undefined, {content: retryMessage.content, forceNew: true})}>Retry last message</button>}</div>}
          {earlierFailures.length > 0 && <details className="remote-chat-caption"><summary>Earlier failed turns ({earlierFailures.length})</summary>{earlierFailures.map(t => <p key={t.id}>Turn failed: {t.error || t.runName}</p>)}</details>}
          <div ref={end}/>
        </div>
        <form className="chat-composer" onSubmit={e => void submit(e)}>
          <label className="field"><span className="label">Message</span><textarea className="textarea chat-composer-input" rows={3} aria-label="Message" value={draft} disabled={busy || !enabled || initializing} onChange={e => changeDraft(e.target.value)} onKeyDown={e => {if(e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {e.preventDefault(); void submit();}}} placeholder="Message this agent…"/></label>
          <div className="chat-composer-actions"><span className="chat-composer-hint">Enter to send · Shift + Enter for a new line</span><button className="btn btn-primary" disabled={busy || retryPending || Boolean(active) || unavailable || initializing || !draft.trim() || (!profile && !harness) || !enabled || (coordinate && !peers.length)}>{busy ? 'Sending…' : active ? 'Waiting for reply…' : 'Send'}</button></div>
        </form>
        {outputRun && <details className="remote-run-details" open={rawActivityOpen} onToggle={e => setRawActivityOpen(e.currentTarget.open)}><summary>{outputBackend === 'hermesAgent' ? 'Reasoning and runner output' : 'Runner activity'}</summary>{rawActivityOpen && <><p className="remote-chat-caption">Original runner output for {outputRun}. Access uses your current run-read permissions; older logs may no longer be retained.</p>{historicalOutput && currentOutputRun && currentOutputRun !== outputRun && <button type="button" className="btn btn-ghost" onClick={() => {setHistoricalOutput(undefined); setRawActivityOpen(false);}}>Back to latest turn output</button>}<LiveStream token={token} namespace={namespace} name={outputRun}/></>}</details>}
      </section>
    </div>
  </div>;
}
