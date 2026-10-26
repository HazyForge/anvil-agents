import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import type { Snapshot } from '../api/types';
import type { UIConfig } from '../auth/config';
import { answerManagerDecision, managerAdmissionRejected, submitManagerWork, type LoopResolution } from '../api/localManagerLoop';
import { useLocalManagerLoop } from '../state/localManagerLoop';
import { LocalManagerLoop, loopStateLabel } from '../components/LocalManagerLoop';
import { routeLocalMessage, type LocalDescriptor, type LocalManagerRequest, type LocalRouteEvent, type LocalRouteResult } from '../api/localManager';
import { AgentAvatar } from '../components/AgentAvatar';
import { MarkdownBody } from '../components/MarkdownBody';
import { LocalRoutingDecision, actionLabel, reasonLabel } from '../components/LocalRoutingDecision';
import { LocalRouteFeedback } from '../components/LocalRouteFeedback';
import { acknowledgeLocalConversation, localHarnessName, localRuntimeIdentity, loopConversation, prepareManagedTurn, rejectManagedTurn, requiresBrowserRecovery, readLocalConversations, setLocalConversations, useLocalConversations, type LocalConversation } from '../state/localConversations';

type Receipt = { id: string; content: string; at: string; request: LocalManagerRequest; runtime: Record<string, unknown>; events: LocalRouteEvent[]; result?: LocalRouteResult; targetId?: string; status: 'routing' | 'starting' | 'running' | 'complete' | 'unsent' | 'unconfirmed' | 'failed' | 'queued' | 'verifying' | 'needs_decision' | 'paused' | 'cancelled'; reply?: string; error?: string; activity: string[]; decisionId?: string; decisionVersion?: number; answerResolution?: LoopResolution };
const receiptPrefix = 'anvil-agents-desktop.local-manager.receipt.v1.';
function loadReceipts(): Receipt[] {
  try { return Object.keys(localStorage).filter(key => key.startsWith(receiptPrefix)).map(key => JSON.parse(localStorage.getItem(key) || 'null')).filter((value): value is Receipt => value && typeof value.id === 'string' && Array.isArray(value.events) && value.request && value.runtime).sort((a,b) => a.at.localeCompare(b.at)).slice(-60).map(receipt => receipt.decisionId && receipt.status==='needs_decision' && !receipt.answerResolution ? {...receipt,error:receipt.error||'The saved decision response was interrupted. Recover it using the original request.'} : ['routing','starting','running'].includes(receipt.status) ? {...receipt, status: receipt.status === 'routing' ? 'unsent' : 'unconfirmed', error: receipt.status === 'routing' ? 'This routing view was interrupted. Check the conversation before routing again.' : 'The saved receipt did not confirm completion. Check the conversation and harness.'} : receipt); } catch { return []; }
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
const eventLabels: Record<string,string> = { detect:'Detected work state', ask:'Decision needed', act:'Runtime action', verify:'Outcome verified', inventory: 'Conversation inventory received', classifying: 'Classifier requested', decision: 'Target and intent observed', action: 'Action selected', dispatch: 'Native harness started', completion: 'Harness completion observed' };
export function LocalManagerPage({ snapshot, onBusyChange, signedIn = false }: {snapshot: Snapshot; onBusyChange?: (busy: boolean) => void; signedIn?: boolean; config?: UIConfig | null; getAccessToken?: () => Promise<string | null>}) {
  const {conversations, running, storageError} = useLocalConversations(snapshot);
  const managerLoop = useLocalManagerLoop(snapshot,true);
  const [receipts, setReceipts] = useState(loadReceipts), [inspected, setInspected] = useState(''), [selected, setSelected] = useState('');
  const [draft, setDraft] = useState(''), [routing, setRouting] = useState(false), [executing, setExecuting] = useState(false), [error, setError] = useState('');
  const available = snapshot.harnesses.filter(h => h.present && h.delegatable && h.id !== 'grok');
  const [harness, setHarness] = useState(() => available.find(h => h.id === 'prime')?.id || available[0]?.id || '');
  const [workdir, setWorkdir] = useState('');
  const [acknowledging, setAcknowledging] = useState(false);
  const routeController = useRef<AbortController | null>(null);
  const messages = useRef<HTMLDivElement>(null);
  const latest = receipts.find(receipt => receipt.id === inspected) || receipts.at(-1);
  const selectedConversation = conversations.find(c => c.id === selected);
  function conversationStatus(conversation: LocalConversation) {
    const task = managerLoop.loop?.tasks.find(task => task.id === conversation.managerTaskId);
    return task ? loopStateLabel(task.state) : conversation.managerRequestId ? 'Checking submission' : running.has(conversation.id) ? 'Running' : conversation.pending ? 'Completion unconfirmed' : 'Ready';
  }
  useEffect(() => { onBusyChange?.(routing || executing); return () => onBusyChange?.(false); }, [routing,executing,onBusyChange]);
  useEffect(() => () => {routeController.current?.abort();}, []);
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
  useEffect(() => {
    if (!managerLoop.loop) return;
    for (const receipt of receipts) {
      const task = managerLoop.loop.tasks.find(task => task.requestId === receipt.id);
      if (!task) continue;
      const events = managerLoop.loop.events.filter(event => event.taskId === task.id).map(event => ({step:event.stage,at:event.at,data:{message:event.message}}));
      const next: Receipt = {...receipt,targetId:task.conversation.id,status:task.state === 'completed' ? 'complete' : task.state,reply:task.reply,error:task.error,events:[...receipt.events.filter(event => !['detect','ask','act','verify'].includes(event.step)),...events]};
      if (JSON.stringify(next) !== JSON.stringify(receipt)) record(next);
    }
  },[managerLoop.loop,receipts]);
  async function execute(receipt: Receipt, expected: LocalConversation) {
    if (executing || managerLoop.loading || managerLoop.error || managerLoop.projectionError) return;
    if (expected.connectAnvil === true || (expected.connectAnvil !== false && signedIn)) {
      record({...receipt,status:'unsent',error:'This conversation uses browser-bound Anvil tools. Open it in Chat to send with your current sign-in, or turn those tools off there before using host-managed work.'});
      return;
    }
    setExecuting(true);
    let before: LocalConversation | undefined;
    try {
      before = await prepareManagedTurn(snapshot,expected,receipt.content,receipt.id);
      record({...receipt,status:'starting',targetId:expected.id});
      const saved = await submitManagerWork({requestId:receipt.id,conversation:loopConversation(before),content:receipt.content});
      await managerLoop.accept(saved);
    } catch(err) {
      if (managerAdmissionRejected(err) && before) await rejectManagedTurn(before,receipt.id);
      record({...receipt,targetId:expected.id,status:before && !managerAdmissionRejected(err) ? 'unconfirmed' : 'unsent',error:err instanceof Error ? err.message : String(err)});
      await managerLoop.refresh();
    } finally {setExecuting(false);}
  }
  async function recoverSubmission(conversation: LocalConversation) {
    if (executing || !conversation.managerSubmission || managerLoop.loading || managerLoop.error) return;
    setExecuting(true);setError('');
    const submission = conversation.managerSubmission;
    try {
      // The identical frozen body and request ID are safe to recover even if
      // the first POST reached the host and only its response was lost.
      await managerLoop.accept(await submitManagerWork(submission));
    } catch(err) {
      if (managerAdmissionRejected(err)) await rejectManagedTurn({...conversation,...submission.conversation,pending:false,lastError:undefined,managerRequestId:undefined,managerTaskId:undefined,managerSubmission:undefined},submission.requestId);
      setError(err instanceof Error ? err.message : String(err));
      await managerLoop.refresh();
    } finally {setExecuting(false);}
  }
  async function recoverDecision(receipt: Receipt) {
    if (routing || executing || !receipt.decisionId || !receipt.decisionVersion) return;
    setRouting(true);
    try {
      const answer = await answerManagerDecision({id:receipt.decisionId,version:receipt.decisionVersion},receipt.content,receipt.id);
      if (answer.snapshot) await managerLoop.accept(answer.snapshot);
      record({...receipt,answerResolution:answer.resolution,error:undefined,status:answer.resolution.source==='clarify'?'needs_decision':'complete',reply:answer.resolution.source==='clarify'?'That answer still needs clarification. Choose an option in the manager loop.':`Decision saved: ${answer.resolution.choice}. Follow its progress in the manager loop.`});
    } catch(err) {record({...receipt,status:'needs_decision',error:err instanceof Error?err.message:String(err)});await managerLoop.refresh();}
    finally {setRouting(false);}
  }
  async function route(event?: FormEvent, previous?: Receipt, targetId?: string) {
    event?.preventDefault();
    const content = (previous?.content || draft).trim();
    if (!content || routing || managerLoop.loading || managerLoop.error || managerLoop.projectionError) return;
    if ([...content].length > 12000) {setError('This message exceeds 12,000 characters. Shorten it before routing.'); return;}
    const inventory = readLocalConversations(snapshot);
    if (inventory.length > 200) {setError('This manager supports up to 200 local conversations. Open the intended conversation directly in Chat.'); return;}
    const context: LocalManagerRequest['context'] = receipts.filter(r => r.status==='complete' && r.targetId).slice(-6).map(r => ({role:'user',content:bounded(r.content,1000),conversationId:r.targetId,at:r.at}));
    for (const decision of managerLoop.loop?.decisions.filter(decision => decision.state === 'pending') || []) {
      const task = managerLoop.loop?.tasks.find(task => task.id === decision.taskId);
      if (task) context.push({role:'assistant',content:bounded(`${decision.message}\nAvailable answers: ${decision.choices.map(choice=>choice.label).join('; ')}`,1000),conversationId:task.conversation.id,at:task.updatedAt});
    }
    const request: LocalManagerRequest = {content,conversations:inventory.filter(hasBinding).map(c => descriptor(c,running)),context:context.sort((a,b)=>(a.at||'').localeCompare(b.at||'')).slice(-6),defaultHarness:harness,...(targetId?{targetId}:{})};
    const runtime = {...localRuntimeIdentity(snapshot),availableHarnesses:available.map(h => h.id),busy:inventory.some(c => c.pending),now:new Date().toISOString()};
    let receipt: Receipt = {id:crypto.randomUUID(),content,at:new Date().toISOString(),request,runtime,events:[],status:'routing',activity:[]};
    record(receipt); setInspected(receipt.id); setDraft(''); setError(''); setRouting(true);
    const controller = new AbortController(); routeController.current=controller;
    try {
      const result = await routeLocalMessage(request,controller.signal,event => {receipt={...receipt,events:[...receipt.events,event]};record(receipt);});
      receipt={...receipt,result,request:result.snapshot||receipt.request,runtime:result.runtime||receipt.runtime,targetId:result.action.targetId,status:'unsent'}; record(receipt);
      if (controller.signal.aborted) return;
      const routedTarget = result.action.targetId || result.decision.subjectChoice;
      const pendingDecisions = managerLoop.loop?.decisions.filter(decision => decision.state === 'pending' && managerLoop.loop?.tasks.some(task=>task.id===decision.taskId&&task.conversation.id===routedTarget)) || [];
      const routedAnswer = (result.decision.intent === 'decide' && result.decision.outcome === 'route' && result.action.reason !== 'intent_uncertain') || (!!targetId && targetId === routedTarget);
      if (routedAnswer && pendingDecisions.length === 1) {
        const decision = pendingDecisions[0];
        receipt={...receipt,targetId:routedTarget,decisionId:decision.id,decisionVersion:decision.version,status:'needs_decision'};
        record(receipt);
        const answer = await answerManagerDecision(decision,content,receipt.id);
        if (answer.snapshot) await managerLoop.accept(answer.snapshot);
        const choice = decision.choices.find(choice=>choice.id===answer.resolution.choice)?.label || answer.resolution.choice;
        receipt={...receipt,targetId:routedTarget,decisionId:decision.id,answerResolution:answer.resolution,status:answer.resolution.source==='clarify'?'needs_decision':'complete',reply:answer.resolution.source==='clarify'?'That answer did not select a clear action. The saved question is still waiting; choose an option in the manager loop.':`Decision saved: ${choice}. Follow its progress in the manager loop.`,events:[...receipt.events,{step:'ask',at:new Date().toISOString(),data:{message:answer.resolution.source==='clarify'?'Answer needs clarification.':`Recorded answer: ${choice}.`,source:answer.resolution.source}}]};
        record(receipt);
        return;
      }
      if (result.action.kind === 'read_status') {
        const current=readLocalConversations(snapshot).find(c => c.id === result.action.targetId);
        if (!current) throw new Error('The selected conversation is no longer available. No harness was started.');
        receipt={...receipt,status:'complete',reply:`${current.name}: ${conversationStatus(current)}. ${current.lastError || (current.messages.at(-1)?.role==='assistant'?`Latest saved reply:\n\n${current.messages.at(-1)!.content}`:'No completed reply is saved yet.')}\n\nRead from saved local state. No harness was started.`};record(receipt);
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
    } catch (err) {if (!controller.signal.aborted) {receipt={...receipt,status:receipt.decisionId?'needs_decision':'unsent',error:err instanceof Error?err.message:String(err)};record(receipt);if(receipt.decisionId)await managerLoop.refresh();}}
    finally {if(routeController.current===controller) {routeController.current=null;setRouting(false);}}
  }
  return <div className="manager-page local-manager-page">
    <header className="manager-header"><div><span className="manager-eyebrow">Anvil conversations · on this device</span><h1>Local Manager</h1></div><div className="manager-context-controls"><Link className="btn btn-ghost" aria-disabled={routing||executing} onClick={e => {if(routing||executing)e.preventDefault();}} to="/local-manager/scenarios">Routing scenarios</Link><details className="local-new-settings"><summary>New conversation settings</summary><label>Harness<select aria-label="New conversation harness" value={harness} onChange={e => setHarness(e.target.value)}>{!available.length&&<option value="">No harness available</option>}{available.map(h => <option key={h.id} value={h.id}>{h.displayName}</option>)}</select></label><label>Working folder<input className="input" aria-label="New conversation working folder" placeholder="Default workspace" value={workdir} onChange={e => setWorkdir(e.target.value)}/></label><p>New chats use local tools. Enable Anvil tools in Chat settings if needed.</p></details></div></header>
    {(error||storageError||managerLoop.projectionError)&&<div className="banner banner-error" role="alert">{error||storageError||managerLoop.projectionError}</div>}
    {!snapshot.localManager?.available&&<p className="local-manager-availability">Classifier unavailable: {snapshot.localManager?.reason||'No provider configured.'} You can still select a conversation explicitly.</p>}
    <div className="manager-workspace">
      <aside className="manager-subjects" aria-label="Local conversations"><div className="manager-panel-heading"><h2>Local conversations</h2><span>{conversations.length}</span></div><p className="manager-panel-note">The same chats you use in Chat.</p>{conversations.map(c => <button className={`manager-subject ${c.id===selected?'is-selected':''}`} key={c.id} aria-label={`Inspect ${c.name}`} aria-pressed={c.id===selected} onClick={() => setSelected(c.id)}><AgentAvatar name={c.name} identity={c.id}/><span><strong>{c.name}</strong><small>{localHarnessName(c.harness)} · {conversationStatus(c)}</small></span></button>)}{selectedConversation&&<section className="manager-subject-detail"><h3>{selectedConversation.name}</h3><span className="manager-target-label">Anvil wrapper · {selectedConversation.id}</span><p>{localHarnessName(selectedConversation.harness)} · {selectedConversation.target||'runtime not confirmed'}{selectedConversation.wslDistro?` · ${selectedConversation.wslDistro}`:''}</p><p>{conversationStatus(selectedConversation)} · {selectedConversation.workdir||'Default workspace'}</p><p>Each turn starts a native process with this chat’s saved history.</p>{selectedConversation.nativeSession&&<p>Observed native session: <span className="manager-target-label">{selectedConversation.nativeSession.id}</span>Recorded from {selectedConversation.nativeSession.harness}. Native resume is not used.</p>}{!hasBinding(selectedConversation)&&<p>Confirm this conversation’s runtime and folder in Chat settings before routing to it.</p>}<details><summary>Saved messages ({selectedConversation.messages.length})</summary>{selectedConversation.messages.map((m,i)=><div className="local-saved-message" key={i}><strong>{m.role==='user'?'You':selectedConversation.name}</strong><MarkdownBody content={m.content}/></div>)}</details>{selectedConversation.pending&&!running.has(selectedConversation.id)&&!selectedConversation.managerTaskId&&!selectedConversation.managerRequestId&&<div className="manager-clarification"><p>Completion is unconfirmed. Check the harness and folder before clearing this guard.</p><button className="btn btn-ghost" disabled={acknowledging} onClick={() => void acknowledge(selectedConversation.id)}>{acknowledging?'Saving acknowledgment…':'I checked that this work has stopped'}</button></div>}</section>}</aside>
      <section className="manager-conversation" aria-label="Manager conversation"><div className="manager-conversation-heading"><h2>One place to direct your work.</h2><p>The manager chooses a conversation and an action. You can inspect every observed step.</p></div><div className="manager-messages" ref={messages}>{conversations.filter(c=>(c.pending||c.lastError)&&!c.managerTaskId&&!c.managerRequestId&&requiresBrowserRecovery(c)).map(c=><p key={c.id} className="manager-interrupted">{c.name} used browser-bound Anvil tools. <Link to="/chat">Resolve this turn in Chat</Link> with your current sign-in.</p>)}{conversations.filter(c=>c.managerRequestId&&!c.managerTaskId).map(c=><section key={c.id} className="manager-clarification" aria-label={`Unconfirmed submission for ${c.name}`}><p>The host has not confirmed admission for {c.name}. The original message and request ID are saved.</p><button className="btn btn-ghost" disabled={executing||managerLoop.loading||!!managerLoop.error||!c.managerSubmission} onClick={()=>void recoverSubmission(c)}>Recover saved submission</button></section>)}<LocalManagerLoop loop={managerLoop.loop} error={managerLoop.error} observedAt={managerLoop.observedAt} loading={managerLoop.loading} onSnapshot={managerLoop.accept} onRefresh={managerLoop.refresh}/>{!receipts.length&&<div className="manager-welcome"><h3>Pick up any conversation.</h3><p>“Make the launch plan shorter.” “How is the camera work going?” Your existing chats remain the home for their work.</p></div>}{receipts.map(receipt => <article className={`manager-message ${receipt.id===latest?.id?'is-inspected':''}`} key={receipt.id}><div className="manager-user-message"><span>You<time>{new Date(receipt.at).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'})}</time></span><p>{receipt.content}</p></div><button className="manager-route-receipt" onClick={() => setInspected(receipt.id)}><span>↳</span><span><strong>{receipt.targetId?conversations.find(c=>c.id===receipt.targetId)?.name||actionLabel(receipt.result?.action.kind||'Routing'):receipt.result?actionLabel(receipt.result.action.kind):'Routing message…'}</strong><small>{receipt.decisionId ? 'Answer to a saved decision' : receipt.result?actionLabel(receipt.result.action.kind):'Waiting for the local router'}</small></span><span className={`manager-receipt-state ${receipt.status==='complete'?'state-accepted':receipt.status==='failed'?'state-failed':''}`}>{receipt.status==='unsent'?'not sent':receipt.status}</span></button>{receipt.error&&<p className="manager-interrupted" role="alert">{receipt.error}</p>}{receipt.status==='unsent'&&receipt.result?.action.kind==='wait'&&<p className="manager-interrupted">{reasonLabel(receipt.result.action.reason)}</p>}{receipt.status==='unsent'&&receipt.result?.action.kind==='clarify'&&<div className="manager-clarification"><p>{reasonLabel(receipt.result.action.reason)} Choose where to send this message.</p><div className="manager-clarification-options">{conversations.filter(hasBinding).map(c=><button className="btn btn-ghost" key={c.id} disabled={routing||executing} onClick={() => void route(undefined,receipt,c.id)}>Send to {c.name}</button>)}<button className="btn btn-ghost" disabled={routing||executing||!harness} onClick={() => void route(undefined,receipt,'new_subject')}>Start new conversation</button><button className="btn btn-ghost" disabled={routing||executing||!harness} onClick={() => void route(undefined,receipt,'none')}>General conversation</button></div></div>}{receipt.status==='unsent'&&receipt.result?.action.kind==='wait'&&conversations.some(c=>c.id===receipt.result?.action.targetId&&!c.managerTaskId&&!c.pending)&&<button className="btn btn-primary" disabled={executing||managerLoop.loading||!!managerLoop.error} onClick={()=>{const target=conversations.find(c=>c.id===receipt.result?.action.targetId);if(target)void execute(receipt,target);}}>Queue this message</button>}{receipt.status==='unsent'&&receipt.result?.action.kind!=='clarify'&&<button className="btn btn-ghost" disabled={routing||executing} onClick={()=>void route(undefined,receipt)}>Route again</button>}{receipt.decisionId&&receipt.status==='needs_decision'&&receipt.error&&<button className="btn btn-ghost" disabled={routing||executing||!!managerLoop.error} onClick={()=>void recoverDecision(receipt)}>Recover saved decision</button>}{receipt.reply&&<div className="manager-assistant-message"><span className="manager-eyebrow">{receipt.result?.action.kind==='read_status'?'Saved status':conversations.find(c=>c.id===receipt.targetId)?.name||'Assistant'}</span><div className="chat-bubble-body"><MarkdownBody content={receipt.reply}/></div></div>}{receipt.status==='starting'&&<p className="manager-turn-status" role="status">Starting local harness…</p>}{receipt.status==='running'&&<p className="manager-turn-status" role="status">Local harness is running{receipt.activity.at(-1)?` · ${receipt.activity.at(-1)}`:''}</p>}</article>)}</div><form className="manager-composer" onSubmit={event=>void route(event)}><label className="sr-only" htmlFor="local-manager-message">Message the manager</label><textarea id="local-manager-message" value={draft} onChange={e=>setDraft(e.target.value)} placeholder="What should we work on?"/><div className="manager-composer-footer"><span>Host tracks admitted work · status checks never start work</span><button className="btn btn-primary" disabled={routing||managerLoop.loading||!!managerLoop.error||!!managerLoop.projectionError||!draft.trim()}>{routing?'Choosing…':'Route message'}</button></div></form></section>
      <aside className="manager-inspection" aria-label="Routing activity"><div className="manager-panel-heading"><span className={`manager-signal ${routing?'is-live':''}`}/><h2>Routing activity</h2></div>{!latest?<div className="manager-routing-empty"><p>Choices appear as the local router reports them.</p><span>Observed events, candidate scores, and permitted actions.</span></div>:<><ol className="manager-timeline">{latest.events.map((event,index)=><li className="routing-step routing-step-completed" key={`${index}-${event.step}`}><span className="routing-step-marker">✓</span><div><div className="routing-step-title"><strong>{event.step==='classifying'&&event.data.manual?'Manual target selected':eventLabels[event.step]||event.step}</strong><time>{new Date(event.at).toLocaleTimeString()}</time></div>{event.step==='inventory'&&<p>{String(event.data.count)} local conversations considered</p>}{event.step==='dispatch'&&<p>{localHarnessName(String(event.data.harness))} · saved history replay</p>}{typeof event.data.message==='string'&&<p>{event.data.message}</p>}</div></li>)}</ol>{latest.result&&<><LocalRoutingDecision result={latest.result}/><LocalRouteFeedback key={latest.id} request={latest.request} runtime={latest.runtime} result={latest.result}/></>}{latest.error&&<p className="manager-interrupted">{latest.error}</p>}</>}</aside>
    </div>
  </div>;
}
