import type { LocalRouteResult } from '../api/localManager';
export const actionLabel = (kind: string) => ({continue: 'Continue conversation', read_status: 'Read saved status', new_conversation: 'Create conversation', general: 'General conversation', wait: 'Wait · message unsent', clarify: 'Choose a conversation'}[kind] || kind);
export const intentLabel = (intent: string) => ({question: 'Question', amend_running: 'Amend work', new_work: 'New work', status: 'Status check', decide: 'Decision', unclear: 'Unclear', unclassified: 'Agent will interpret the request'}[intent] || intent);
export const reasonLabel = (reason: string) => ({user_selected: 'You selected the conversation.', local_turn_unfinished: 'Local work is running or its completion is unconfirmed. This message has not been sent or queued.', read_saved_state: 'Read the saved conversation without starting a harness.', continue_saved_conversation: 'Continue the saved Anvil conversation using its existing folder and history.', runtime_changed: 'The saved runtime differs from the current runtime. Check its folder in Chat settings.', harness_unavailable: 'The selected harness is unavailable.', choose_available_harness: 'Choose an available harness for the new conversation.', intent_uncertain: 'The requested action is ambiguous. Choose a conversation to send explicitly.', provider_unavailable: 'The classifier is unavailable. Choose the conversation yourself.', low_confidence: 'The classifier could not choose confidently.', ambiguous: 'Several conversations are plausible.', status_target_required: 'Choose which conversation to check.', new_subject: 'Create a new Anvil conversation.', general_conversation: 'Create a general Anvil conversation.'}[reason] || (reason || 'No action was selected').replaceAll('_', ' '));
export function LocalRoutingDecision({ result }: { result: LocalRouteResult }) {
  const decision = result.decision;
  const labels = new Map((result.candidates || []).map(candidate => [candidate.id, candidate.label]));
  labels.set('new_subject', 'New conversation'); labels.set('none', 'General conversation');
  const manual = decision.reason === 'user_selected';
  const probabilities = Object.entries(decision.subjectProbabilities || {}).sort((a,b) => b[1]-a[1]);
  return <>
    <div className="routing-verdict">
      <div><strong>{actionLabel(result.action.kind)}</strong><span>{result.latencyMs} ms</span></div>
      <p>{reasonLabel(result.action.reason)}</p>
      {result.action.targetId && <p>Target: <strong>{labels.get(result.action.targetId) || result.action.targetId}</strong></p>}
      <p>Intent: {intentLabel(decision.intent)}{decision.intent !== 'unclassified' && ` · ${((decision.intentConfidence || 0) * 100).toFixed(0)}%`}</p>
      <small>{manual ? 'Human choice · no model probability' : decision.model || 'Model not reported'}</small>
    </div>
    {!!probabilities.length && <div className="routing-choices"><h3>Candidate probabilities</h3>{probabilities.map(([id, probability]) => <div className={`routing-choice ${id === decision.subjectChoice ? 'is-chosen' : ''}`} key={id}><div><span>{labels.get(id) || id}</span><strong>{(probability*100).toFixed(1)}%</strong></div><meter min={0} max={1} value={probability} aria-label={`${labels.get(id) || id} probability`}/></div>)}</div>}
    <details className="local-route-json"><summary>Exact candidates and decision</summary><pre>{JSON.stringify(result, null, 2)}</pre></details>
  </>;
}
