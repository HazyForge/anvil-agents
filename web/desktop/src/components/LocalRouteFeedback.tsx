import { useState } from 'react';
import type { LocalManagerRequest, LocalRouteResult, Scenario } from '../api/localManager';
import { actionLabel, intentLabel } from './LocalRoutingDecision';
export const regressionKey = 'anvil-agents-desktop.local-routing-regressions.v1';
export type Regression = { scenario: Scenario; observed: LocalRouteResult; savedAt: string };
export function savedRegressions(): Regression[] { try { const data = JSON.parse(localStorage.getItem(regressionKey) || '[]'); return Array.isArray(data) ? data.filter(item => item?.scenario?.turns && item?.observed).slice(-30) : []; } catch { return []; } }
export function LocalRouteFeedback({ request, runtime, result }: { request: LocalManagerRequest; runtime: Record<string, unknown>; result: LocalRouteResult }) {
  const [open, setOpen] = useState(false), [target, setTarget] = useState((request.conversations || []).some(c=>c.id===result.action.targetId) || ['new_subject','none','unclear'].includes(result.action.targetId || '') ? result.action.targetId! : 'none');
  const [intent, setIntent] = useState(['question','amend_running','new_work','status','decide','unclear'].includes(result.decision.intent) ? result.decision.intent : 'question'), [action, setAction] = useState(['continue','read_status','new_conversation','general','wait','clarify'].includes(result.action.kind) ? result.action.kind : 'clarify');
  const [notice, setNotice] = useState('');
  const create = (): Regression => {
    const id = `correction-${crypto.randomUUID()}`;
    return { savedAt: new Date().toISOString(), observed: result, scenario: { id, name: `Correction: ${request.content.slice(0, 60)}`, runtime, defaultHarness: request.defaultHarness || '', conversations: request.conversations || [], context: request.context, turns: [{ id: 'corrected-turn', content: request.content, expect: { targets: [target], intents: [intent], actions: [action] } }] } };
  };
  function save(download: boolean) {
    const regression = create();
    try { localStorage.setItem(regressionKey, JSON.stringify([...savedRegressions(), regression].slice(-30))); window.dispatchEvent(new Event('local-routing-regressions')); setNotice('Regression draft saved on this browser. It can be rerun with the live classifier in Routing scenarios.'); }
    catch { setNotice('Browser storage is unavailable. Export the regression to keep it.'); }
    if (download) { const url = URL.createObjectURL(new Blob([JSON.stringify([regression.scenario], null, 2)], {type: 'application/json'})); const anchor = document.createElement('a'); anchor.href = url; anchor.download = `${regression.scenario.id}.json`; anchor.click(); URL.revokeObjectURL(url); }
  }
  return <div className="local-route-feedback"><button className="btn btn-ghost" onClick={() => setOpen(!open)}>Report wrong route</button>{open && <form onSubmit={event => {event.preventDefault(); save(false);}}>
    <p>Label the intended target and action. This saves a local regression draft with the message and candidate descriptions.</p>
    <label>Expected conversation<select className="input" aria-label="Expected conversation" value={target} onChange={e => setTarget(e.target.value)}>{(request.conversations || []).map(c => <option key={c.id} value={c.id}>{c.label}</option>)}<option value="new_subject">New conversation</option><option value="none">General conversation</option><option value="unclear">Ask for clarification</option></select></label>
    <label>Expected intent<select className="input" aria-label="Expected intent" value={intent} onChange={e => setIntent(e.target.value)}>{['question','amend_running','new_work','status','decide','unclear'].map(value => <option key={value} value={value}>{intentLabel(value)}</option>)}</select></label>
    <label>Expected action<select className="input" aria-label="Expected action" value={action} onChange={e => setAction(e.target.value as typeof action)}>{['continue','read_status','new_conversation','general','wait','clarify'].map(value => <option key={value} value={value}>{actionLabel(value)}</option>)}</select></label>
    <div className="manager-actions"><button className="btn btn-primary">Save regression draft</button><button type="button" className="btn btn-ghost" onClick={() => save(true)}>Export regression JSON</button></div>
  </form>}{notice && <p role="status">{notice}</p>}</div>;
}
