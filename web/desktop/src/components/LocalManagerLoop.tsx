import {useRef, useState, type FormEvent} from 'react';
import {answerManagerDecision, chooseManagerDecision, type LoopDecision, type LoopResolution, type LoopSnapshot, type LoopTask} from '../api/localManagerLoop';
import {MarkdownBody} from './MarkdownBody';

export const loopStateLabel = (state: LoopTask['state']) => ({queued:'Waiting for workspace',running:'Harness running',verifying:'Verifying outcome',needs_decision:'Your decision needed',completed:'Reply verified',paused:'Paused',cancelled:'Closed'})[state];
const stages = ['detect','ask','act','verify'] as const;
const stageLabels = {detect:'Detect',ask:'Ask',act:'Act',verify:'Verify'};

function DecisionCard({decision, task, disabled, onSnapshot, onRefresh}: {decision: LoopDecision; task: LoopTask; disabled: boolean; onSnapshot: (snapshot: LoopSnapshot) => Promise<void>; onRefresh: () => Promise<void>}) {
  const [answer,setAnswer] = useState(''),[busy,setBusy] = useState(false),[error,setError] = useState('');
  const [resolution,setResolution] = useState<LoopResolution | null>(null);
  const requests = useRef(new Map<string,string>());
  async function send(choice?: string,event?: FormEvent) {
    event?.preventDefault();
    if (busy || disabled || (!choice && !answer.trim())) return;
    const fingerprint = JSON.stringify([decision.version,choice || '',choice ? '' : answer.trim()]);
    const requestId = requests.current.get(fingerprint) || crypto.randomUUID(); requests.current.set(fingerprint,requestId);
    setBusy(true);setError('');
    try {
      if (choice) {await onSnapshot(await chooseManagerDecision(decision,choice,requestId));setResolution(null);}
      else {
        const response = await answerManagerDecision(decision,answer.trim(),requestId);
        if (response.snapshot) await onSnapshot(response.snapshot);
        setResolution(response.resolution);
        if (response.resolution.source !== 'clarify') setAnswer('');
      }
    } catch(err) {setError(err instanceof Error ? err.message : String(err));await onRefresh();}
    finally {setBusy(false);}
  }
  return <section className="loop-decision" aria-label={`Decision for ${task.conversation.name}`} data-decision-id={decision.id}>
    <p>{decision.message}</p>
    <div className="loop-decision-options">{decision.choices.map(choice => <button key={choice.id} type="button" className="btn btn-ghost" disabled={busy || disabled} onClick={() => void send(choice.id)}>{choice.label}</button>)}</div>
    <form onSubmit={event => void send(undefined,event)}>
      <label className="sr-only" htmlFor={`answer-${decision.id}`}>Answer decision for {task.conversation.name}</label>
      <textarea id={`answer-${decision.id}`} value={answer} maxLength={2000} onChange={event => setAnswer(event.target.value)} placeholder="Or tell the manager what to do…" disabled={busy || disabled}/>
      <button className="btn btn-primary" disabled={busy || disabled || !answer.trim()}>{busy ? 'Saving decision…' : 'Send decision'}</button>
    </form>
    {resolution && <p className="loop-answer-result" role="status">{resolution.source === 'clarify' ? 'That answer did not select a clear action. Choose an option above or say it another way.' : `Saved choice: ${resolution.choice}. ${resolution.source === 'classifier' ? `Classifier${resolution.confidence !== undefined ? ` · ${(resolution.confidence * 100).toFixed(0)}%` : ''}` : resolution.source === 'recorded' ? 'Previously recorded answer.' : 'Explicit answer.'}`}</p>}
    {error && <p className="manager-interrupted" role="alert">{error}</p>}
  </section>;
}

export function LocalManagerLoop({loop,error,observedAt,loading,onSnapshot,onRefresh}: {loop: LoopSnapshot | null; error: string; observedAt: string; loading: boolean; onSnapshot: (snapshot: LoopSnapshot) => Promise<void>; onRefresh: () => Promise<void>}) {
  const tasks = [...(loop?.tasks || [])].sort((a,b) => b.createdAt.localeCompare(a.createdAt));
  const pending = loop?.decisions.filter(decision => decision.state === 'pending') || [];
  const visible = tasks.filter(task => !['completed','cancelled'].includes(task.state)).concat(tasks.filter(task => ['completed','cancelled'].includes(task.state)).slice(0,5));
  return <section className="local-manager-loop" aria-label="Manager loop">
    <div className="loop-heading"><div><span className={`manager-signal ${!error && !loading ? 'is-live' : ''}`}/><h2>Manager loop</h2><span className="loop-decision-count">{pending.length ? `${pending.length} decision${pending.length === 1 ? '' : 's'}` : 'Watching saved work'}</span></div><button className="btn btn-ghost" type="button" onClick={() => void onRefresh()}>Check now</button></div>
    <p className="loop-summary">{loading ? 'Connecting to the host…' : error ? 'Host tracking needs attention. New work is paused.' : `Tracking continues while this tab is closed. Last checked ${new Date(observedAt).toLocaleTimeString()}.`}</p>
    {error && <p className="manager-interrupted" role="alert">{error}</p>}
    {!visible.length && !loading && <div className="loop-empty"><ol className="loop-phases">{stages.map(stage => <li key={stage}>{stageLabels[stage]}</li>)}</ol><p>The manager watches admitted work and brings failures or uncertain completion back as a saved decision.</p></div>}
    {visible.map(task => {
      const events = loop!.events.filter(event => event.taskId === task.id);
      const decision = pending.find(item => item.taskId === task.id);
      const currentStage = decision ? 'ask' : task.state === 'running' || task.state === 'queued' ? 'act' : task.state === 'completed' || task.state === 'verifying' ? 'verify' : events.at(-1)?.stage || 'detect';
      return <article className={`loop-task loop-task-${task.state}`} key={task.id} data-task-id={task.id} aria-label={`Work for ${task.conversation.name}`}>
        <header><div><strong>{task.conversation.name}</strong><span>{loopStateLabel(task.state)}</span></div><time dateTime={task.updatedAt}>{new Date(task.updatedAt).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'})}</time></header>
        <p className="loop-task-content">{task.content || 'Observed conversation recovery'}</p>
        <ol className="loop-phases" aria-label={`Loop phases for ${task.conversation.name}`}>{stages.map(stage => <li key={stage} aria-current={currentStage === stage ? 'step' : undefined} className={`${currentStage === stage ? 'is-current' : ''} ${events.some(event => event.stage === stage) ? 'is-observed' : ''}`}><span>{events.some(event => event.stage === stage) ? '•' : '○'}</span>{stageLabels[stage]}</li>)}</ol>
        {decision && <DecisionCard key={`${decision.id}:${decision.version}`} decision={decision} task={task} disabled={!!error} onSnapshot={onSnapshot} onRefresh={onRefresh}/>}
        {!decision && task.error && <p className="manager-interrupted">{task.error}</p>}
        {task.verification && <p className="loop-verification">{task.verification}</p>}
        <details className="loop-evidence"><summary>Observed steps · attempt {task.attempt}</summary><ol>{events.map(event => <li key={event.id}><span>{stageLabels[event.stage]}</span><p>{event.message}</p><time dateTime={event.at}>{new Date(event.at).toLocaleTimeString()}</time></li>)}</ol>{task.reply && <div className="loop-saved-reply"><strong>Saved reply</strong><MarkdownBody content={task.reply}/></div>}</details>
      </article>;
    })}
  </section>;
}
