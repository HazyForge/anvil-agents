import { useEffect, useRef, useState, type FormEvent } from 'react';
import type { Snapshot } from '../api/types';
import { activityFromLog } from '../api/runActivity';
import { localReply, streamLocalChat } from '../api/localChat';

type Message = {role: 'user' | 'assistant'; content: string};
type Conversation = {id: string; title: string; harness: string; workdir: string; messages: Message[]; draft: string; pending?: boolean; lastError?: string};
const storageKey = 'anvil-agents-desktop.local-chat.v1';
const selectedKey = `${storageKey}.selected`;
function restore(): Conversation[] {
  try {
    const value = JSON.parse(localStorage.getItem(storageKey) || '[]');
    return Array.isArray(value) ? value.filter(c => typeof c?.id === 'string' && typeof c.title === 'string' && typeof c.harness === 'string' && typeof c.workdir === 'string' && typeof c.draft === 'string' && (c.lastError === undefined || typeof c.lastError === 'string') && Array.isArray(c.messages) && c.messages.every((m: Message) => m && ['user', 'assistant'].includes(m.role) && typeof m.content === 'string')) : [];
  } catch { return []; }
}
function savedSelection() { try { return localStorage.getItem(selectedKey) || ''; } catch { return ''; } }

export function LocalChatPage({snapshot, onBusyChange}: {snapshot: Snapshot; onBusyChange?: (busy: boolean) => void}) {
  const [conversations, setConversations] = useState(restore);
  const [selected, setSelected] = useState(savedSelection);
  const choices = snapshot.harnesses.filter(h => h.present && h.delegatable);
  const [harness, setHarness] = useState('prime');
  const [workdir, setWorkdir] = useState('');
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [activity, setActivity] = useState<string[]>([]);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  const end = useRef<HTMLDivElement>(null);
  const thread = conversations.find(c => c.id === selected);
  const selectedHarness = thread?.harness || harness;
  const selectedWorkdir = thread?.workdir ?? workdir;
  const text = thread?.draft ?? draft;
  const location = snapshot.harnessTarget === 'wsl' || snapshot.wsl.insideWSL ? `Local WSL${snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro ? ` · ${snapshot.prefs.wslDistro || snapshot.wsl.defaultDistro}` : ''}` : 'This computer';
  useEffect(() => { try { localStorage.setItem(storageKey, JSON.stringify(conversations)); } catch { setError('Browser storage is full or unavailable. Keep this tab open to retain the conversation.'); } }, [conversations]);
  useEffect(() => { try { localStorage.setItem(selectedKey, selected); } catch { /* Conversation state remains. */ } }, [selected]);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => { onBusyChange?.(busy); return () => onBusyChange?.(false); }, [busy, onBusyChange]);
  useEffect(() => { end.current?.scrollIntoView({block: 'nearest'}); }, [thread?.messages.length, activity.length, busy]);
  function update(id: string, transform: (c: Conversation) => Conversation) { setConversations(previous => previous.map(c => c.id === id ? transform(c) : c)); }
  function changeDraft(value: string) { if (thread) update(thread.id, c => ({...c, draft: value})); else setDraft(value); }
  async function send(event?: FormEvent) {
    event?.preventDefault();
    const content = text.trim();
    if (!content || busy || controller.current || !choices.some(h => h.id === selectedHarness)) return;
    const history = thread?.messages || [];
    const prompt = history.length ? `Continue this conversation. Prior messages below are context; respond to the latest user message. Use your tools for requested work.\n${JSON.stringify([...history, {role: 'user', content}])}` : content;
    if (new TextEncoder().encode(prompt).length > 64000) { setError('This conversation exceeds the local prompt limit. Start a new conversation with a summary.'); return; }
    const id = thread?.id || crypto.randomUUID();
    if (!thread) {
      setConversations(previous => [{id, title: content.slice(0, 60), harness: selectedHarness, workdir: selectedWorkdir, messages: [{role: 'user', content}], draft: '', pending: true}, ...previous]);
      setSelected(id); setDraft('');
    } else update(id, c => ({...c, messages: [...c.messages, {role: 'user', content}], draft: '', pending: true, lastError: undefined}));
    setBusy(true); setError(''); setActivity([]); setStatus(`Starting ${choices.find(h => h.id === selectedHarness)?.displayName || selectedHarness} on ${location}…`);
    const abort = new AbortController(); controller.current = abort;
    const seen = new Set<string>();
    // The final HTTP result has a bounded capture of the beginning of stdout.
    // Keep the streamed tail so long tool runs still retain their final reply.
    let streamedOutput = '';
    const failTurn = (message: string, nextStatus: string) => {
      setError(message); setStatus(nextStatus);
      update(id, c => ({...c, pending: false, lastError: message}));
    };
    try {
      await streamLocalChat({harness: selectedHarness, prompt, workdir: selectedWorkdir || undefined}, abort.signal, (kind, data) => {
        if (kind === 'started') {
          setStatus('Local harness is running');
          if (data.workdir) update(id, c => ({...c, workdir: data.workdir!}));
        } else if (kind === 'stdout') {
          streamedOutput = `${streamedOutput}${data.line || ''}\n`.slice(-2 * 1024 * 1024);
          const next = activityFromLog(data.line || '');
          if (next && !seen.has(next.key)) { seen.add(next.key); setActivity(previous => [...previous, next.label].slice(-8)); setStatus(next.label); }
        } else if (kind === 'result') {
          if (data.timedOut || data.exitCode !== 0) { failTurn(data.timedOut ? 'The local harness reached its time limit. Check its work before retrying.' : `The local harness exited with code ${data.exitCode}. Check its local configuration and provider login.`, 'Local turn stopped'); return; }
          const reply = localReply(streamedOutput || data.stdout || '', selectedHarness);
          if (!reply) { failTurn('The harness exited without an identifiable assistant reply. Check its local session before retrying.', 'No reply received'); return; }
          update(id, c => ({...c, pending: false, lastError: undefined, messages: [...c.messages, {role: 'assistant', content: reply}]})); setStatus('Reply received');
        } else if (kind === 'error') { failTurn(data.message || 'The local harness could not start.', 'Local turn stopped'); }
      });
    } catch (err) { failTurn(err instanceof Error ? err.message : String(err), 'Local connection stopped'); }
    finally { update(id, c => ({...c, pending: false})); setBusy(false); controller.current = null; }
  }
  return <div className="human-page">
    <div className="page-header"><div><h1 className="page-title">Chat</h1><p className="page-sub">{location} · Uses the selected harness's local model and login. History is saved on this device.</p></div></div>
    {(error || thread?.lastError) && <div className="banner banner-error" role="alert">{error || thread?.lastError}</div>}
    {thread?.pending && !busy && <div className="banner banner-error" role="alert">Completion of the previous local turn was not confirmed. Check the working folder and harness before sending more work. Nothing has been resumed or resent.</div>}
    <div className="remote-chat-layout">
      <aside className="panel remote-chat-sidebar" aria-label="Local conversations">
        <button className="btn" disabled={busy} onClick={() => {setSelected(''); setStatus(''); setActivity([]); setError('');}}>New local conversation</button>
        {conversations.map(c => <button key={c.id} className={`remote-thread ${selected === c.id ? 'remote-thread-selected' : ''}`} disabled={busy} onClick={() => {setSelected(c.id); setStatus(''); setActivity([]); setError('');}}><strong>{c.title}</strong><small>{c.harness}</small></button>)}
      </aside>
      <section className="panel entity-chat-main">
        <div className="remote-chat-config">
          <label className="field"><span className="label">Local harness</span><select aria-label="Local harness" className="input" value={selectedHarness} disabled={busy || Boolean(thread)} onChange={e => setHarness(e.target.value)}>
            {!choices.some(h => h.id === selectedHarness) && <option value={selectedHarness}>{selectedHarness} (not found)</option>}
            {choices.map(h => <option key={h.id} value={h.id}>{h.displayName}</option>)}
          </select></label>
          <label className="field"><span className="label">Working folder</span><input aria-label="Working folder" className="input" value={selectedWorkdir} disabled={busy || Boolean(thread)} onChange={e => setWorkdir(e.target.value)} placeholder="Default local workspace"/></label>
          <p className="remote-chat-caption">Files stay in this local folder across turns. Choose an absolute folder path to work in an existing project. This conversation runs on {location}.</p>
        </div>
        <div className="chat-messages" aria-live="polite">
          {!thread?.messages.length && <div className="empty">Talk to Prime Agent or another installed local harness.</div>}
          {thread?.messages.map((m, i) => <article key={i} className={`chat-bubble ${m.role === 'user' ? 'chat-bubble-user' : 'chat-bubble-run'}`}><header className="chat-bubble-header"><span className="chat-bubble-role">{m.role === 'user' ? 'You' : selectedHarness}</span></header><pre className="chat-bubble-body">{m.content}</pre></article>)}
          {status && <section className="turn-activity" aria-label="Local agent activity"><p role="status">{status}</p><ol className="turn-activity-events">{activity.map((a, i) => <li key={i}>{a}</li>)}</ol></section>}
          <div ref={end}/>
        </div>
        <form className="chat-composer" onSubmit={e => void send(e)}><label className="field"><span className="label">Message</span><textarea aria-label="Message" className="textarea chat-composer-input" value={text} onChange={e => changeDraft(e.target.value)} onKeyDown={e => {if(e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {e.preventDefault(); void send();}}} placeholder="Ask your local assistant…"/></label><div className="chat-composer-actions"><button className="btn btn-primary" disabled={busy || !text.trim() || !choices.some(h => h.id === selectedHarness)}>{busy ? 'Working locally…' : 'Send'}</button></div></form>
      </section>
    </div>
  </div>;
}
