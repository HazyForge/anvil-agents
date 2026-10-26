import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import type { Snapshot } from '../api/types';
import type { UIConfig } from '../auth/config';
import { completedLocalReply, LocalChatRequestError, streamLocalChat } from '../api/localChat';
import { activityFromLog } from '../api/runActivity';
import { routeLocalMessage, type LocalDescriptor, type LocalManagerRequest, type LocalRouteEvent, type LocalRouteResult } from '../api/localManager';
import { AgentAvatar } from '../components/AgentAvatar';
import { MarkdownBody } from '../components/MarkdownBody';
import { LocalRoutingDecision, actionLabel, reasonLabel } from '../components/LocalRoutingDecision';
import { LocalRouteFeedback } from '../components/LocalRouteFeedback';
import { acknowledgeLocalConversation, admitLocalTurn, localHarnessName, localRuntimeIdentity, observeLocalNativeSession, readLocalConversations, setLocalConversationRunning, setLocalConversations, updateLocalConversation, useLocalConversations, type LocalConversation } from '../state/localConversations';

type Receipt = { id: string; content: string; at: string; request: LocalManagerRequest; runtime: Record<string, unknown>; events: LocalRouteEvent[]; result?: LocalRouteResult; targetId?: string; status: 'routing' | 'starting' | 'running' | 'complete' | 'unsent' | 'unconfirmed' | 'failed'; reply?: string; error?: string; activity: string[] };
const receiptPrefix = 'anvil-agents-desktop.local-manager.receipt.v1.';
function loadReceipts(): Receipt[] {
  try { return Object.keys(localStorage).filter(key => key.startsWith(receiptPrefix)).map(key => JSON.parse(localStorage.getItem(key) || 'null')).filter((value): value is Receipt => value && typeof value.id === 'string' && Array.isArray(value.events) && value.request && value.runtime).sort((a,b) => a.at.localeCompare(b.at)).slice(-60).map(receipt => ['routing','starting','running'].includes(receipt.status) ? {...receipt, status: receipt.status === 'routing' ? 'unsent' : 'unconfirmed', error: receipt.status === 'routing' ? 'This routing view was interrupted. Check the conversation before routing again.' : 'The saved receipt did not confirm completion. Check the conversation and harness.'} : receipt); } catch { return []; }
}
const bounded = (text: string, count: number) => [...text].slice(0,count).join('');
function descriptor(conversation: LocalConversation, running: ReadonlySet<string>): LocalDescriptor {
  const firstTopic=conversation.messages.find(message=>message.role==='user')?.content || '';
  const latestUser=conversation.messages.slice().reverse().find(message=>message.role==='user')?.content || '';
  const latestReply=conversation.messages.slice().reverse().find(message=>message.role==='assistant')?.content || '';
  const summary=bounded(`Topic: ${bounded(firstTopic,300)}\nLatest request: ${bounded(latestUser,350)}\nLatest reply: ${bounded(latestReply,300)}`,1000);
  return { id: conversation.id, label: bounded(conversation.name,120), summary, harness: conversation.harness, target: conversation.target!, wslDistro: conversation.wslDistro || undefined, status: running.has(conversation.id) ? 'running' : conversation.pending ? 'unconfirmed' : 'ready', touchedAt: conversation.touchedAt };
}
const hasBinding = (conversation: LocalConversation) => conversation.target === 'native' || (conversation.target === 'wsl' && Boolean(conversation.wslDistro));
const eventLabels: Record<string,string> = { inventory: 'Conversation inventory received', classifying: 'Classifier requested', decision: 'Target and intent observed', action: 'Action selected', dispatch: 'Native harness started', completion: 'Harness completion observed' };
export function LocalManagerPage({ snapshot, onBusyChange, signedIn = false, config, getAccessToken }: {snapshot: Snapshot; onBusyChange?: (busy: boolean) => void; signedIn?: boolean; config?: UIConfig | null; getAccessToken?: () => Promise<string | null>}) {
  const {conversations, running, storageError} = useLocalConversations(snapshot);
  const [receipts, setReceipts] = useState(loadReceipts), [inspected, setInspected] = useState(''), [selected, setSelected] = useState('');
  const [draft, setDraft] = useState(''), [routing, setRouting] = useState(false), [executing, setExecuting] = useState(false), [error, setError] = useState('');
  const available = snapshot.harnesses.filter(h => h.present && h.delegatable && h.id !== 'grok');
  const [harness, setHarness] = useState(() => available.find(h => h.id === 'prime')?.id || available[0]?.id || '');
  const [workdir, setWorkdir] = useState('');
  const [acknowledging, setAcknowledging] = useState(false);
  const routeController = useRef<AbortController | null>(null), nativeController = useRef<AbortController | null>(null);
  const messages = useRef<HTMLDivElement>(null);
  const latest = receipts.find(receipt => receipt.id === inspected) || receipts.at(-1);
  const selectedConversation = conversations.find(c => c.id === selected);
  useEffect(() => { onBusyChange?.(routing || executing); return () => onBusyChange?.(false); }, [routing,executing,onBusyChange]);
  useEffect(() => () => {routeController.current?.abort(); nativeController.current?.abort();}, []);
  useEffect(() => {const frame=requestAnimationFrame(()=>{if(messages.current) messages.current.scrollTop=messages.current.scrollHeight;});return()=>cancelAnimationFrame(frame);}, [receipts.length,receipts.at(-1)?.status]);
  useEffect(() => {const refresh = (event: StorageEvent) => {if (event.key?.startsWith(receiptPrefix)) setReceipts(loadReceipts());}; window.addEventListener('storage',refresh); return () => window.removeEventListener('storage',refresh);}, []);
  function record(receipt: Receipt) {
    try { localStorage.setItem(receiptPrefix+receipt.id,JSON.stringify(receipt)); }
    catch { setError('The routing receipt could not be saved. Local conversation state is still shown in the inventory.'); }
    setReceipts(previous => [...previous.filter(value => value.id !== receipt.id),receipt].sort((a,b) => a.at.localeCompare(b.at)).slice(-60));
  }
  async function acknowledge(id: string) {
    setAcknowledging(true);
    try {await acknowledgeLocalConversation(id);setError('');}
    catch(err) {setError(err instanceof Error?err.message:String(err));}
    finally {setAcknowledging(false);}
  }
  async function execute(receipt: Receipt, expected: LocalConversation) {
    if (nativeController.current) {record({...receipt,status:'unsent',error:'Another local turn is starting or running. This message has not been sent. Route it again when the current turn finishes.'}); return;}
    const controller = new AbortController(); nativeController.current = controller; setExecuting(true);
    let admitted = false;
    let current = receipt;
    const patch = (changes: Partial<Receipt>) => { current = {...current,...changes}; record(current); };
    const writes: Promise<void>[] = [];
    try {
      const namespaces = config?.defaultNamespaces || [], namespace = expected.remoteNamespace || (namespaces.includes('anvilhub') ? 'anvilhub' : namespaces[0]);
      const wantsConnection = expected.connectAnvil !== false && signedIn;
      let token: string | undefined;
      if (wantsConnection) {
        if (!snapshot.prefs.apiOrigin || !config || !getAccessToken || !namespace || !namespaces.includes(namespace)) throw new Error('This conversation uses Anvil tools, but the connection is unavailable. Update its Chat settings before sending.');
        token = await getAccessToken() || undefined;
        if (!token) throw new Error('Sign in again to connect this conversation to Anvil tools. Your message was not sent.');
      }
      if (controller.signal.aborted) throw new Error('Dispatch was cancelled before starting.');
      const admission = await admitLocalTurn(snapshot, expected, receipt.content); admitted = true;
      patch({status:'starting',targetId:expected.id});
      let streamed = '';
      const fail = (message: string, confirmed: boolean) => {writes.push(updateLocalConversation(expected.id,c => ({...c,pending:!confirmed,lastError:message}))); patch({status:confirmed?'failed':'unconfirmed',error:message});};
      try {
        await streamLocalChat({harness:admission.conversation.harness,prompt:admission.prompt,workdir:admission.conversation.workdir || undefined,expectedRuntime:{target:admission.conversation.target!,wslDistro:admission.conversation.wslDistro},...(wantsConnection?{remoteNamespace:namespace}:{})},controller.signal,(kind,data) => {
          if (kind === 'started') {
            if (data.workdir) writes.push(updateLocalConversation(expected.id,c => ({...c,workdir:data.workdir!,target:data.target || c.target,wslDistro:data.wslDistro ?? c.wslDistro})));
            patch({status:'running',events:[...current.events,{step:'dispatch',at:new Date().toISOString(),data:{harness:expected.harness,conversationId:expected.id}}]});
          } else if (kind === 'stdout') {
            observeLocalNativeSession(expected.id,expected.harness,data.line || '');
            streamed = `${streamed}${data.line || ''}\n`.slice(-2*1024*1024);
            const activity = activityFromLog(data.line || '');
            if (activity && !current.activity.includes(activity.label)) patch({activity:[...current.activity,activity.label].slice(-8)});
          } else if (kind === 'result') {
            if (data.timedOut || data.exitCode !== 0) {fail(data.timedOut?'The local harness reached its time limit. Check its work before retrying.':`The local harness exited with code ${data.exitCode}. Check its local configuration.`,true); return;}
            const output = completedLocalReply(streamed,data,expected.harness);
            if (output.error || !output.reply) {fail(output.error || 'The harness exited without an identifiable public reply. Check its work before retrying.',true); return;}
            writes.push(updateLocalConversation(expected.id,c => ({...c,pending:false,lastError:undefined,messages:[...c.messages,{role:'assistant',content:output.reply}]})).then(() => {patch({status:'complete',reply:output.reply,events:[...current.events,{step:'completion',at:new Date().toISOString(),data:{exitCode:data.exitCode}}]});}));
          } else if (kind === 'error') fail(data.message || 'The harness could not start.',data.code !== 'cleanup_unconfirmed');
        },token);
      } catch (err) { fail(err instanceof Error?err.message:String(err),err instanceof LocalChatRequestError); }
    } catch (err) { patch({status:'unsent',error:err instanceof Error?err.message:String(err)}); }
    finally {try {await Promise.all(writes);} finally {if(admitted) setLocalConversationRunning(expected.id,false); nativeController.current=null; setExecuting(false);}}
  }
  async function route(event?: FormEvent, previous?: Receipt, targetId?: string) {
    event?.preventDefault();
    const content = (previous?.content || draft).trim();
    if (!content || routing) return;
    if ([...content].length > 12000) {setError('This message exceeds 12,000 characters. Shorten it before routing.'); return;}
    const inventory = readLocalConversations(snapshot);
    if (inventory.length > 200) {setError('This manager supports up to 200 local conversations. Open the intended conversation directly in Chat.'); return;}
    const request: LocalManagerRequest = {content,conversations:inventory.filter(hasBinding).map(c => descriptor(c,running)),context:receipts.filter(r => r.status==='complete' && r.targetId).slice(-6).map(r => ({role:'user',content:bounded(r.content,1000),conversationId:r.targetId,at:r.at})),defaultHarness:harness,...(targetId?{targetId}:{})};
    const runtime = {...localRuntimeIdentity(snapshot),availableHarnesses:available.map(h => h.id),busy:inventory.some(c => c.pending),now:new Date().toISOString()};
    let receipt: Receipt = {id:crypto.randomUUID(),content,at:new Date().toISOString(),request,runtime,events:[],status:'routing',activity:[]};
    record(receipt); setInspected(receipt.id); setDraft(''); setError(''); setRouting(true);
    const controller = new AbortController(); routeController.current=controller;
    try {
      const result = await routeLocalMessage(request,controller.signal,event => {receipt={...receipt,events:[...receipt.events,event]};record(receipt);});
      receipt={...receipt,result,request:result.snapshot||receipt.request,runtime:result.runtime||receipt.runtime,targetId:result.action.targetId,status:'unsent'}; record(receipt);
      if (controller.signal.aborted) return;
      if (result.action.kind === 'read_status') {
        const current=readLocalConversations(snapshot).find(c => c.id === result.action.targetId);
        if (!current) throw new Error('The selected conversation is no longer available. No harness was started.');
        receipt={...receipt,status:'complete',reply:`${current.name}: ${running.has(current.id)?'running':current.pending?'completion unconfirmed':'ready'}. ${current.lastError || (current.messages.at(-1)?.role==='assistant'?`Latest saved reply:\n\n${current.messages.at(-1)!.content}`:'No completed reply is saved yet.')}\n\nRead from saved local state. No harness was started.`};record(receipt);
      } else if (['continue','new_conversation','general'].includes(result.action.kind)) {
        let expected = inventory.find(c => c.id === result.action.targetId);
        if (result.action.kind === 'general') {
          const identity=localRuntimeIdentity(snapshot);
          expected=inventory.find(c=>c.managerKind==='general' && c.harness===harness && c.target===identity.target && (c.wslDistro||'')===identity.wslDistro && c.managerDefaultWorkdir===workdir.trim());
        }
        if (result.action.kind !== 'continue' && !expected) {
          const id=crypto.randomUUID(),name=result.action.kind==='general'?'General conversation':bounded(content.replace(/\s+/g,' '),60);
          expected={id,name,title:name,harness,workdir:workdir.trim(),messages:[],draft:'',connectAnvil:false,...(result.action.kind==='general'?{managerKind:'general' as const,managerDefaultWorkdir:workdir.trim()}:{}),...localRuntimeIdentity(snapshot)};
          await setLocalConversations(prior => [...prior,expected!]);
          receipt={...receipt,targetId:id};record(receipt);
        }
        if (!expected) throw new Error('The selected conversation is no longer available. No local message was sent.');
        setRouting(false); routeController.current=null;
        await execute(receipt,expected);
      }
    } catch (err) {if (!controller.signal.aborted) {receipt={...receipt,status:'unsent',error:err instanceof Error?err.message:String(err)};record(receipt);}}
    finally {if(routeController.current===controller) {routeController.current=null;setRouting(false);}}
  }
  return <div className="manager-page local-manager-page">
    <header className="manager-header"><div><span className="manager-eyebrow">Anvil conversations · on this device</span><h1>Local Manager</h1></div><div className="manager-context-controls"><Link className="btn btn-ghost" aria-disabled={routing||executing} onClick={e => {if(routing||executing)e.preventDefault();}} to="/local-manager/scenarios">Routing scenarios</Link><details className="local-new-settings"><summary>New conversation settings</summary><label>Harness<select aria-label="New conversation harness" value={harness} onChange={e => setHarness(e.target.value)}>{!available.length&&<option value="">No harness available</option>}{available.map(h => <option key={h.id} value={h.id}>{h.displayName}</option>)}</select></label><label>Working folder<input className="input" aria-label="New conversation working folder" placeholder="Default workspace" value={workdir} onChange={e => setWorkdir(e.target.value)}/></label><p>New chats use local tools. Enable Anvil tools in Chat settings if needed.</p></details></div></header>
    {(error||storageError)&&<div className="banner banner-error" role="alert">{error||storageError}</div>}
    {!snapshot.localManager?.available&&<p className="local-manager-availability">Classifier unavailable: {snapshot.localManager?.reason||'No provider configured.'} You can still select a conversation explicitly.</p>}
    <div className="manager-workspace">
      <aside className="manager-subjects" aria-label="Local conversations"><div className="manager-panel-heading"><h2>Local conversations</h2><span>{conversations.length}</span></div><p className="manager-panel-note">The same chats you use in Chat.</p>{conversations.map(c => <button className={`manager-subject ${c.id===selected?'is-selected':''}`} key={c.id} aria-label={`Inspect ${c.name}`} aria-pressed={c.id===selected} onClick={() => setSelected(c.id)}><AgentAvatar name={c.name} identity={c.id}/><span><strong>{c.name}</strong><small>{localHarnessName(c.harness)} · {running.has(c.id)?'running':c.pending?'unconfirmed':'ready'}</small></span></button>)}{selectedConversation&&<section className="manager-subject-detail"><h3>{selectedConversation.name}</h3><span className="manager-target-label">Anvil wrapper · {selectedConversation.id}</span><p>{localHarnessName(selectedConversation.harness)} · {selectedConversation.target||'runtime not confirmed'}{selectedConversation.wslDistro?` · ${selectedConversation.wslDistro}`:''}</p><p>{selectedConversation.workdir||'Default workspace'}</p><p>Each turn starts a native process with this chat’s saved history.</p>{selectedConversation.nativeSession&&<p>Observed native session: <span className="manager-target-label">{selectedConversation.nativeSession.id}</span>Recorded from {selectedConversation.nativeSession.harness}. Native resume is not used.</p>}{!hasBinding(selectedConversation)&&<p>Confirm this conversation’s runtime and folder in Chat settings before routing to it.</p>}<details><summary>Saved messages ({selectedConversation.messages.length})</summary>{selectedConversation.messages.map((m,i)=><div className="local-saved-message" key={i}><strong>{m.role==='user'?'You':selectedConversation.name}</strong><MarkdownBody content={m.content}/></div>)}</details>{selectedConversation.pending&&!running.has(selectedConversation.id)&&<div className="manager-clarification"><p>Completion is unconfirmed. Check the harness and folder before clearing this guard.</p><button className="btn btn-ghost" disabled={acknowledging} onClick={() => void acknowledge(selectedConversation.id)}>{acknowledging?'Saving acknowledgment…':'I checked that this work has stopped'}</button></div>}</section>}</aside>
      <section className="manager-conversation" aria-label="Manager conversation"><div className="manager-conversation-heading"><h2>One place to direct your work.</h2><p>The manager chooses a conversation and an action. You can inspect every observed step.</p></div><div className="manager-messages" ref={messages} aria-live="polite">{!receipts.length&&<div className="manager-welcome"><h3>Pick up any conversation.</h3><p>“Make the launch plan shorter.” “How is the camera work going?” Your existing chats remain the home for their work.</p></div>}{receipts.map(receipt => <article className={`manager-message ${receipt.id===latest?.id?'is-inspected':''}`} key={receipt.id}><div className="manager-user-message"><span>You<time>{new Date(receipt.at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'})}</time></span><p>{receipt.content}</p></div><button className="manager-route-receipt" onClick={() => setInspected(receipt.id)}><span>↳</span><span><strong>{receipt.targetId?conversations.find(c=>c.id===receipt.targetId)?.name||actionLabel(receipt.result?.action.kind||'Routing'):receipt.result?actionLabel(receipt.result.action.kind):'Routing message…'}</strong><small>{receipt.result?actionLabel(receipt.result.action.kind):'Waiting for the local router'}</small></span><span className={`manager-receipt-state ${receipt.status==='complete'?'state-accepted':receipt.status==='failed'?'state-failed':''}`}>{receipt.status==='unsent'?'not sent':receipt.status}</span></button>{receipt.error&&<p className="manager-interrupted" role="alert">{receipt.error}</p>}{receipt.result?.action.kind==='wait'&&<p className="manager-interrupted">{reasonLabel(receipt.result.action.reason)}</p>}{receipt.status==='unsent'&&receipt.result?.action.kind==='clarify'&&<div className="manager-clarification"><p>{reasonLabel(receipt.result.action.reason)} Choose where to send this message.</p><div className="manager-clarification-options">{conversations.filter(hasBinding).map(c=><button className="btn btn-ghost" key={c.id} disabled={routing||executing} onClick={() => void route(undefined,receipt,c.id)}>Send to {c.name}</button>)}<button className="btn btn-ghost" disabled={routing||executing||!harness} onClick={() => void route(undefined,receipt,'new_subject')}>Start new conversation</button><button className="btn btn-ghost" disabled={routing||executing||!harness} onClick={() => void route(undefined,receipt,'none')}>General conversation</button></div></div>}{receipt.status==='unsent'&&receipt.result?.action.kind!=='clarify'&&<button className="btn btn-ghost" disabled={routing||executing} onClick={()=>void route(undefined,receipt)}>Route again</button>}{receipt.reply&&<div className="manager-assistant-message"><span className="manager-eyebrow">{receipt.result?.action.kind==='read_status'?'Saved status':conversations.find(c=>c.id===receipt.targetId)?.name||'Assistant'}</span><div className="chat-bubble-body"><MarkdownBody content={receipt.reply}/></div></div>}{receipt.status==='starting'&&<p className="manager-turn-status" role="status">Starting local harness…</p>}{receipt.status==='running'&&<p className="manager-turn-status" role="status">Local harness is running{receipt.activity.at(-1)?` · ${receipt.activity.at(-1)}`:''}</p>}</article>)}</div><form className="manager-composer" onSubmit={event=>void route(event)}><label className="sr-only" htmlFor="local-manager-message">Message the manager</label><textarea id="local-manager-message" value={draft} onChange={e=>setDraft(e.target.value)} placeholder="What should we work on?"/><div className="manager-composer-footer"><span>Saved locally · status checks never start work</span><button className="btn btn-primary" disabled={routing||!draft.trim()}>{routing?'Choosing…':'Route message'}</button></div></form></section>
      <aside className="manager-inspection" aria-label="Routing activity"><div className="manager-panel-heading"><span className={`manager-signal ${routing?'is-live':''}`}/><h2>Routing activity</h2></div>{!latest?<div className="manager-routing-empty"><p>Choices appear as the local router reports them.</p><span>Observed events, candidate scores, and permitted actions.</span></div>:<><ol className="manager-timeline">{latest.events.map((event,index)=><li className="routing-step routing-step-completed" key={`${index}-${event.step}`}><span className="routing-step-marker">✓</span><div><div className="routing-step-title"><strong>{event.step==='classifying'&&event.data.manual?'Manual target selected':eventLabels[event.step]||event.step}</strong><time>{new Date(event.at).toLocaleTimeString()}</time></div>{event.step==='inventory'&&<p>{String(event.data.count)} local conversations considered</p>}{event.step==='dispatch'&&<p>{localHarnessName(String(event.data.harness))} · saved history replay</p>}</div></li>)}</ol>{latest.result&&<><LocalRoutingDecision result={latest.result}/><LocalRouteFeedback key={latest.id} request={latest.request} runtime={latest.runtime} result={latest.result}/></>}{latest.error&&<p className="manager-interrupted">{latest.error}</p>}</>}</aside>
    </div>
  </div>;
}
