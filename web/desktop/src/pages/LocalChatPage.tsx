import { useEffect, useRef, useState, type FormEvent } from 'react';
import { AgentAvatar } from '../components/AgentAvatar';
import type { UIConfig } from '../auth/config';
import type { Snapshot } from '../api/types';
import { activityFromLog } from '../api/runActivity';
import { completedLocalReply, LocalChatRequestError, streamLocalChat } from '../api/localChat';

type Message = {role: 'user' | 'assistant'; content: string};
type Conversation = {id: string; title: string; name: string; harness: string; workdir: string; messages: Message[]; draft: string; pending?: boolean; lastError?: string; target?: string; wslDistro?: string; connectAnvil?: boolean; remoteNamespace?: string};
const storageKey = 'anvil-agents-desktop.local-chat.v1';
const selectedKey = `${storageKey}.selected`;
const harnessNames: Record<string, string> = {prime: 'Prime Agent', codex: 'Codex', opencode: 'OpenCode', agy: 'Antigravity', grok: 'Grok', pi: 'Pi'};
const harnessName = (id: string) => harnessNames[id] || id;
function runtimeIdentity(snapshot: Snapshot) {
  const target = snapshot.harnessTarget === 'wsl' || snapshot.wsl.insideWSL ? 'wsl' : 'native';
  const wslDistro = target === 'wsl' ? (snapshot.wsl.insideWSL ? snapshot.wsl.defaultDistro : snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro) || '' : '';
  return {target, wslDistro};
}
function restore(snapshot: Snapshot): Conversation[] {
  let records: Conversation[] = [];
  try {
    const value = JSON.parse(localStorage.getItem(storageKey) || '[]');
    if (Array.isArray(value)) records = value.filter(c => typeof c?.id === 'string' && typeof c.harness === 'string' && typeof c.workdir === 'string' && typeof c.draft === 'string' && (c.lastError === undefined || typeof c.lastError === 'string') && Array.isArray(c.messages) && c.messages.every((m: Message) => m && ['user', 'assistant'].includes(m.role) && typeof m.content === 'string'));
  } catch { /* The default agent remains available without browser storage. */ }
  const used = new Set(records.map(c => typeof c.name === 'string' ? c.name.trim().toLowerCase() : '').filter(Boolean));
  records = records.map(c => {
    if (typeof c.name === 'string' && c.name.trim()) return {...c, name: c.name.trim()};
    const base = harnessName(c.harness); let name = base, number = 2;
    while (used.has(name.toLowerCase())) name = `${base} ${number++}`;
    used.add(name.toLowerCase());
    return {...c, name}; // Keep legacy titles, IDs, messages and drafts intact.
  });
  if (!records.some(c => c.harness === 'prime')) records.unshift({id: 'local-prime-agent', name: 'Prime Agent', title: 'Prime Agent', harness: 'prime', workdir: '', messages: [], draft: '', ...runtimeIdentity(snapshot)});
  return records;
}
function savedSelection() { try { return localStorage.getItem(selectedKey) || ''; } catch { return ''; } }

export function LocalChatPage({snapshot, onBusyChange, signedIn = false, config, getAccessToken}: {snapshot: Snapshot; onBusyChange?: (busy: boolean) => void; signedIn?: boolean; config?: UIConfig | null; getAccessToken?: () => Promise<string | null>}) {
  const [conversations, setConversations] = useState(() => restore(snapshot));
  const [selected, setSelected] = useState(savedSelection);
  const choices = snapshot.harnesses.filter(h => h.present && h.delegatable);
  const [harness, setHarness] = useState('prime');
  const [workdir, setWorkdir] = useState('');
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [activity, setActivity] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [elapsed, setElapsed] = useState(0);
  const [anvilConnected, setAnvilConnected] = useState(false);
  const startedAt = useRef(0);
  const reportedAt = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const end = useRef<HTMLDivElement>(null);
  const thread = conversations.find(c => c.id === selected) || conversations[0];
  const selectedHarness = creating ? harness : thread?.harness || 'prime';
  const selectedWorkdir = creating ? workdir : thread?.workdir ?? '';
  const text = thread?.draft || '';
  const {target: currentTarget, wslDistro: currentDistro} = runtimeIdentity(snapshot);
  const unconfirmed = !busy ? conversations.filter(c => c.pending) : [];
  const targetChanged = Boolean(!creating && thread && (thread.target !== currentTarget || (thread.wslDistro || '') !== currentDistro));
  const location = 'Local';
  const runtimeLocation = currentTarget === 'wsl' ? `WSL${currentDistro ? ` · ${currentDistro}` : ''}` : 'Native';
  const namespaces = [...new Set((config?.defaultNamespaces || []).filter(ns => typeof ns === 'string' && ns.trim()))];
  const defaultNamespace = namespaces.includes('anvilhub') ? 'anvilhub' : namespaces[0] || '';
  const remoteNamespace = thread?.remoteNamespace || defaultNamespace;
  const canConnectAnvil = Boolean(signedIn && snapshot.prefs.apiOrigin && config && getAccessToken && namespaces.length);
  const wantsAnvil = thread?.connectAnvil !== false;
  const connectionRequested = canConnectAnvil && wantsAnvil;
  const connectionLabel = anvilConnected ? 'Anvil connected' : !wantsAnvil ? 'Anvil tools off' : !signedIn ? 'Sign in to Anvil' : canConnectAnvil ? 'Anvil tools enabled' : 'Anvil unavailable';
  useEffect(() => { try { localStorage.setItem(storageKey, JSON.stringify(conversations)); } catch { setError('Browser storage is full or unavailable. Keep this tab open to retain the conversation.'); } }, [conversations]);
  useEffect(() => { try { localStorage.setItem(selectedKey, selected); } catch { /* Conversation state remains. */ } }, [selected]);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => { onBusyChange?.(busy); return () => onBusyChange?.(false); }, [busy, onBusyChange]);
  useEffect(() => { if (!busy) return; const timer = setInterval(() => setElapsed(Math.floor((Date.now() - startedAt.current) / 1000)), 1000); return () => clearInterval(timer); }, [busy]);
  useEffect(() => { end.current?.scrollIntoView({block: 'nearest'}); }, [thread?.messages.length, activity.length, busy]);
  function update(id: string, transform: (c: Conversation) => Conversation) { setConversations(previous => previous.map(c => c.id === id ? transform(c) : c)); }
  function changeDraft(value: string) { if (thread) update(thread.id, c => ({...c, draft: value})); }
  function createAgent(event: FormEvent) {
    event.preventDefault();
    const name = newName.trim();
    if (!name || busy || !choices.some(h => h.id === harness)) return;
    if (conversations.some(c => c.name.toLowerCase() === name.toLowerCase())) { setError('Choose a different name so each agent is easy to find.'); return; }
    const id = crypto.randomUUID();
    setConversations(previous => [...previous, {id, name, title: name, harness, workdir: workdir.trim(), messages: [], draft: '', target: currentTarget, wslDistro: currentDistro}]);
    setSelected(id); setCreating(false); setError(''); setStatus(''); setActivity([]);
  }
  async function send(event?: FormEvent) {
    event?.preventDefault();
    const content = text.trim();
    if (!thread || creating || !content || busy || controller.current || conversations.some(c => c.pending) || targetChanged || !choices.some(h => h.id === selectedHarness)) return;
    if (wantsAnvil && signedIn && !canConnectAnvil) { setError('Anvil tools are enabled, but the Anvil connection is unavailable or still loading. Refresh the connection, or turn off Anvil tools in Agent settings to send local-only work. Your message has not been sent.'); return; }
    const history = thread?.messages || [];
    const prompt = history.length ? `Continue this conversation. Prior messages below are context; respond to the latest user message. Use your tools for requested work.\n${JSON.stringify([...history, {role: 'user', content}])}` : content;
    if (new TextEncoder().encode(prompt).length > 64000) { setError('This conversation exceeds the local prompt limit. This agent’s saved history is unchanged. Use a new agent with a summary to continue.'); return; }
    const id = thread.id;
    if (connectionRequested && !namespaces.includes(remoteNamespace)) { setError('Choose an available Anvil namespace in Agent settings before sending.'); return; }
    const abort = new AbortController(); controller.current = abort;
    startedAt.current = Date.now(); reportedAt.current = Date.now(); setElapsed(0);
    setBusy(true); setAnvilConnected(false); setError('');
    let accessToken: string | undefined;
    if (connectionRequested) {
      setStatus('Connecting Anvil tools…');
      try {
        accessToken = await getAccessToken!() || undefined;
        if (!accessToken) throw new Error('Your Anvil sign-in expired. Sign in again before using Anvil tools, or turn them off in Agent settings for local work.');
        if (abort.signal.aborted) throw new Error('The local turn was cancelled before starting.');
      } catch (err) { setError(err instanceof Error ? err.message : String(err)); setStatus(''); setBusy(false); controller.current = null; return; }
    }
    update(id, c => ({...c, messages: [...c.messages, {role: 'user', content}], draft: '', pending: true, lastError: undefined}));
    startedAt.current = Date.now(); reportedAt.current = Date.now(); setElapsed(0);
    setBusy(true); setError(''); setActivity([]); setStatus(`Starting ${choices.find(h => h.id === selectedHarness)?.displayName || selectedHarness} on ${location}…`);
    const seen = new Set<string>();
    // The final HTTP result has a bounded capture of the beginning of stdout.
    // Keep the streamed tail so long tool runs still retain their final reply.
    let streamedOutput = '';
    const failTurn = (message: string, nextStatus: string, confirmed = true) => {
      setError(message); setStatus(nextStatus);
      update(id, c => ({...c, pending: !confirmed, lastError: message}));
    };
    try {
      await streamLocalChat({harness: selectedHarness, prompt, workdir: selectedWorkdir || undefined, ...(connectionRequested ? {remoteNamespace} : {})}, abort.signal, (kind, data) => {
        if (kind === 'started') {
          setStatus('Local harness is running');
          setAnvilConnected(connectionRequested && data.anvilConnected === true && data.remoteNamespace === remoteNamespace);
          if (data.workdir) update(id, c => ({...c, workdir: data.workdir!, target: data.target || currentTarget, wslDistro: data.wslDistro ?? currentDistro}));
        } else if (kind === 'anvil_tool') {
          if (!connectionRequested) return;
          if (!['running', 'succeeded', 'failed'].includes(data.status || '')) return;
          const actionLabels: Record<string, string> = {list_agents: 'Listing agents', get_agent: 'Inspecting an agent', list_harnesses: 'Listing harnesses', list_runs: 'Checking agent work', get_run: 'Checking a run', get_thread: 'Reading an agent conversation', send_message: 'Sending an agent message', start_run: 'Starting agent work'};
          if (!data.action || !Object.hasOwn(actionLabels, data.action)) return;
          const finishedLabels: Record<string, string> = {list_agents: 'Agents listed', get_agent: 'Agent inspected', list_harnesses: 'Harnesses listed', list_runs: 'Agent work checked', get_run: 'Run checked', get_thread: 'Agent conversation read', send_message: 'Agent message accepted', start_run: 'Agent work accepted'};
          const label = data.status === 'running' ? actionLabels[data.action] : data.status === 'succeeded' ? finishedLabels[data.action] : `${actionLabels[data.action]} failed`;
          reportedAt.current = Date.now(); setActivity(previous => [...previous, label].slice(-8)); setStatus(label);
        } else if (kind === 'stdout') {
          streamedOutput = `${streamedOutput}${data.line || ''}\n`.slice(-2 * 1024 * 1024);
          const next = activityFromLog(data.line || '');
          if (next && !seen.has(next.key)) { seen.add(next.key); reportedAt.current = Date.now(); setActivity(previous => [...previous, next.label].slice(-8)); setStatus(next.label); }
        } else if (kind === 'result') {
          if (data.timedOut || data.exitCode !== 0) { failTurn(data.timedOut ? 'The local harness reached its time limit. Check its work before retrying.' : `The local harness exited with code ${data.exitCode}. Check its local configuration and provider login.`, 'Local turn stopped'); return; }
          const {reply, error: replyError} = completedLocalReply(streamedOutput, data, selectedHarness);
          if (replyError) { failTurn(replyError, 'Incomplete harness output'); return; }
          if (!reply) { failTurn('The harness exited without an identifiable assistant reply. Check its local session before retrying.', 'No reply received'); return; }
          update(id, c => ({...c, pending: false, lastError: undefined, messages: [...c.messages, {role: 'assistant', content: reply}]})); setStatus('Reply received');
        } else if (kind === 'error') { failTurn(data.message || 'The local harness could not start.', 'Local turn stopped'); }
      }, accessToken);
    } catch (err) { failTurn(err instanceof Error ? err.message : String(err), 'Local connection stopped', err instanceof LocalChatRequestError); }
    finally { setBusy(false); setAnvilConnected(false); controller.current = null; }
  }
  return <div className="human-page">
    {(error || thread?.lastError) && <div className="banner banner-error" role="alert">{error || thread?.lastError}</div>}
    {unconfirmed.map(c => <div key={c.id} className="banner banner-error" role="alert">Completion was not confirmed for “{c.name}”. Check its harness and working folder ({c.workdir || 'default workspace'}) before sending more work. Nothing has been resumed or resent. <button type="button" className="btn btn-ghost" onClick={() => {update(c.id, saved => ({...saved, pending: false, lastError: undefined})); setError('');}}>I checked that this work has stopped</button></div>)}
    <div className="remote-chat-layout">
      <aside className="panel agent-roster" aria-label="Local agents">
        <div className="agent-roster-heading"><h2>Your agents</h2><button type="button" className="btn btn-ghost" disabled={busy} onClick={() => {setCreating(true); setNewName(''); setHarness('prime'); setWorkdir(''); setStatus(''); setActivity([]); setError('');}}>New agent</button></div>
        {conversations.map(c => <button key={c.id} aria-label={`Open ${c.name}`} aria-pressed={!creating && thread?.id === c.id} className={`agent-row ${!creating && thread?.id === c.id ? 'agent-row-selected' : ''}`} disabled={busy} onClick={() => {setSelected(c.id); setCreating(false); setStatus(''); setActivity([]); setError('');}}><AgentAvatar name={c.name} identity={c.id}/><span className="agent-row-copy"><span className="agent-name">{c.name}</span><span className="agent-meta">{harnessName(c.harness)}</span></span></button>)}
      </aside>
      <section className="panel entity-chat-main">
        {creating ? <form className="agent-settings" onSubmit={createAgent}>
          <h1 className="page-title">Create an agent</h1><p className="muted">Give your agent a name and a place to work. Its messages stay together here.</p>
          <label className="field"><span className="label">Agent name</span><input autoFocus className="input" aria-label="Agent name" value={newName} maxLength={80} onChange={e => setNewName(e.target.value)} placeholder="Research partner"/></label>
          <label className="field"><span className="label">Local harness</span><select aria-label="Local harness" className="input" value={harness} onChange={e => setHarness(e.target.value)}>{!choices.some(h => h.id === harness) && <option value={harness}>{harnessName(harness)} (not found)</option>}{choices.map(h => <option key={h.id} value={h.id}>{h.displayName}</option>)}</select></label>
          <label className="field"><span className="label">Working folder</span><input aria-label="Working folder" className="input" value={workdir} onChange={e => setWorkdir(e.target.value)} placeholder="Default local workspace"/></label>
          <p className="agent-compose-hint">Runs on {location} with the harness’s local model and login.</p>
          <div className="chat-composer-actions"><button type="button" className="btn btn-ghost" onClick={() => setCreating(false)}>Cancel</button><button className="btn btn-primary" disabled={!newName.trim() || !choices.some(h => h.id === harness)}>Create agent</button></div>
        </form> : <>
        <header className="agent-chat-header"><div className="agent-chat-identity"><AgentAvatar name={thread.name} identity={thread.id} size="lg"/><div><h1 className="page-title">{thread.name}</h1><p className="agent-meta">{location} · {harnessName(selectedHarness)}</p><span className={`pill ${anvilConnected ? 'pill-ok' : 'pill-mute'}`}>{connectionLabel}</span></div></div></header>
        <details key={thread.id} className="agent-settings" open={targetChanged}>
          <summary>Agent settings</summary>
          <div className="remote-chat-config">
            <p className="agent-meta">{currentTarget === 'wsl' && (snapshot.wsl.insideWSL || snapshot.wsl.available) ? '✓ ' : ''}{runtimeLocation}{currentTarget === 'wsl' && !snapshot.wsl.insideWSL && !snapshot.wsl.available ? ' · unavailable' : ''}</p>
            <label className="field"><span className="label"><input type="checkbox" checked={wantsAnvil} disabled={busy || unconfirmed.length > 0} onChange={e => update(thread.id, c => ({...c, connectAnvil: e.target.checked}))}/> Use Anvil tools</span></label>
            {canConnectAnvil && wantsAnvil && <label className="field"><span className="label">Anvil namespace</span><select className="input" aria-label="Anvil namespace" disabled={busy || unconfirmed.length > 0} value={remoteNamespace} onChange={e => update(thread.id, c => ({...c, remoteNamespace: e.target.value}))}>{!namespaces.includes(remoteNamespace) && <option value={remoteNamespace}>{remoteNamespace} (unavailable)</option>}{namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}</select></label>}
            <p className="remote-chat-caption">{!wantsAnvil ? 'Anvil tools are off for this agent. Local workspace tools remain available.' : canConnectAnvil ? 'Connects to Anvil for each turn using your signed-in permissions. The harness can inspect and manage agents through Anvil tools.' : !signedIn ? 'Sign in to connect this agent to Primaris. Local workspace tools work without signing in.' : 'Configure a reachable Anvil API with an available namespace to connect this agent to Primaris.'}</p>
            <label className="field"><span className="label">Local harness</span><select aria-label="Local harness" className="input" value={selectedHarness} disabled><option value={selectedHarness}>{harnessName(selectedHarness)}{!choices.some(h => h.id === selectedHarness) ? ' (not found)' : ''}</option></select></label>
            <label className="field"><span className="label">Working folder</span><input aria-label="Working folder" className="input" value={selectedWorkdir} disabled={busy || unconfirmed.length > 0} onChange={e => update(thread.id, c => ({...c, workdir: e.target.value}))} placeholder="Default local workspace"/></label>
            {targetChanged && <div className="banner banner-error" role="alert">This agent was saved for {thread.target ? `${thread.target}${thread.wslDistro ? ` · ${thread.wslDistro}` : ''}` : 'an unrecorded location'}. Check its working folder before using {runtimeLocation}. <button type="button" className="btn btn-ghost" disabled={busy || unconfirmed.length > 0} onClick={() => update(thread.id, c => ({...c, target: currentTarget, wslDistro: currentDistro}))}>Use this folder on {runtimeLocation}</button></div>}
            <p className="remote-chat-caption">Files stay in this folder across turns. Changing folders does not move earlier files. Messages are saved on this device.</p>
          </div>
        </details>
        <div className="chat-messages" aria-live="polite">
          {!thread.messages.length && <div className="agent-empty"><AgentAvatar name={thread.name} identity={thread.id} size="lg"/><h2>What should we work on?</h2><p>Messages with {thread.name} stay together here. Ask a question or give your agent something to do.</p></div>}
          {thread?.messages.map((m, i) => <article key={i} className={`chat-bubble ${m.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}`}><header className="chat-bubble-header"><span className="chat-bubble-role">{m.role === 'user' ? 'You' : thread.name}</span></header><pre className="chat-bubble-body">{m.content}</pre></article>)}
          {status && <section className="turn-activity" aria-label="Local agent activity"><div className="turn-activity-heading"><span className="turn-activity-indicator" aria-hidden="true"/><div><p role="status">{status}</p></div>{busy && <span className="turn-activity-elapsed" aria-hidden="true">{elapsed}s</span>}</div><ol className="turn-activity-events">{activity.map((a, i) => <li key={i}>{a}</li>)}</ol>{busy && Date.now() - reportedAt.current > 15000 && <p className="turn-activity-note">No new activity reported recently. Waiting for the next harness update.</p>}</section>}
          <div ref={end}/>
        </div>
        <form className="chat-composer" onSubmit={e => void send(e)}><label className="field"><span className="label">Message</span><textarea aria-label="Message" className="textarea chat-composer-input" value={text} onChange={e => changeDraft(e.target.value)} onKeyDown={e => {if(e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {e.preventDefault(); void send();}}} placeholder={`Message ${thread.name}…`}/></label><div className="chat-composer-actions"><span className="agent-compose-hint">Enter to send · Shift+Enter for a new line</span><button className="btn btn-primary" disabled={busy || unconfirmed.length > 0 || targetChanged || !text.trim() || !choices.some(h => h.id === selectedHarness)}>{busy ? 'Working locally…' : 'Send'}</button></div></form>
        </>}
      </section>
    </div>
  </div>;
}
