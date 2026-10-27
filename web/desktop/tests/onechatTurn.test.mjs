import test from 'node:test';
import assert from 'node:assert/strict';
import {actionLabel, applyTurnEvent, stopReason, turnFromStatus, turnFromTrace} from '../src/wrapper/onechatTurn.ts';

const event = (name, seq, body = {}, turnId = 't1') => ({type: 'event', event: name, turnId, seq, at: `2026-10-26T23:00:0${seq}Z`, body});

test('a turn folds from started through answer in order', () => {
  let view;
  view = applyTurnEvent(view, event('turn.started', 1, {text: 'add a --json flag', requestId: 'r1', by: {kind: 'desktop'}}));
  view = applyTurnEvent(view, event('turn.inventory', 2, {sessions: [{id: 'a'}, {id: 'b'}]}));
  view = applyTurnEvent(view, event('turn.decided', 3, {decision: {reasoning_summary: 'The invoice session owns this.', actions: [{type: 'send', session_id: 'a'}]}, validationErrors: []}));
  view = applyTurnEvent(view, event('action.started', 4, {index: 0, action: {type: 'send', session_id: 'a'}, sessionTitle: 'Invoice CLI', harness: 'claudeCode', location: 'local'}));
  assert.equal(view.actions[0].status, 'running');
  view = applyTurnEvent(view, event('action.finished', 5, {index: 0, result: {action: {type: 'send'}, sessionId: 'a', reply: 'Added --json; go test passes.', state: 'idle'}}));
  view = applyTurnEvent(view, event('turn.answered', 6, {answer: 'Done: the invoice tool now has --json.'}));
  view = applyTurnEvent(view, event('turn.finished', 7, {answer: 'Done: the invoice tool now has --json.'}));
  assert.equal(view.text, 'add a --json flag');
  assert.equal(view.sessionCount, 2);
  assert.equal(view.decision.reasoning_summary, 'The invoice session owns this.');
  assert.deepEqual(view.actions.map(item => [item.status, item.sessionTitle, item.result.reply]), [['done', 'Invoice CLI', 'Added --json; go test passes.']]);
  assert.equal(view.finished, true);
  assert.equal(view.answer, 'Done: the invoice tool now has --json.');
  assert.equal(view.lastSeq, 7);
});

test('replayed or older events change nothing and other turns start fresh', () => {
  let view = applyTurnEvent(undefined, event('turn.started', 1, {text: 'first'}));
  view = applyTurnEvent(view, event('turn.answered', 2, {answer: 'one'}));
  const same = applyTurnEvent(view, event('turn.answered', 2, {answer: 'changed'}));
  assert.equal(same, view);
  const other = applyTurnEvent(view, event('turn.started', 1, {text: 'second'}, 't2'));
  assert.equal(other.turnId, 't2');
  assert.equal(other.text, 'second');
  assert.equal(other.answer, undefined);
});

test('failed actions and turn errors are visible', () => {
  let view = applyTurnEvent(undefined, event('action.started', 1, {index: 0, action: {type: 'send'}}));
  view = applyTurnEvent(view, event('action.finished', 2, {index: 0, result: {action: {type: 'send'}, sessionId: 'a', error: 'cannot send to a running session', state: 'running'}}));
  view = applyTurnEvent(view, event('turn.finished', 3, {error: 'context canceled'}));
  assert.equal(view.actions[0].status, 'failed');
  assert.equal(view.error, 'context canceled');
});

test('a turn that ends while an action is out marks that action failed', () => {
  let view = applyTurnEvent(undefined, event('action.started', 1, {index: 0, action: {type: 'send'}, sessionTitle: 'Invoice CLI'}));
  view = applyTurnEvent(view, event('turn.finished', 2, {error: 'send: context canceled'}));
  assert.equal(view.actions[0].status, 'failed');
  assert.equal(stopReason(view.error), 'Cancelled before it finished.');
  assert.equal(stopReason('context deadline exceeded'), 'Ran out of time before it finished.');
  assert.equal(stopReason('decide: model unavailable'), 'Stopped: decide: model unavailable');
});

test('a status snapshot rebuilds the running turn for a late joiner', () => {
  const turn = {turnId: 't9', text: 'which project?', startedAt: 'x', requestId: 'r9', by: {kind: 'ios'}, events: [
    event('turn.started', 1, {text: 'which project?'}, 't9'),
    event('turn.decided', 2, {decision: {actions: [{type: 'ask_user', text: 'Weather or invoice?'}]}}, 't9'),
  ]};
  const view = turnFromStatus(turn);
  assert.equal(view.turnId, 't9');
  assert.equal(view.by.kind, 'ios');
  assert.equal(view.decision.actions[0].type, 'ask_user');
  assert.equal(turnFromStatus(null), undefined);
});

test('action labels speak about the work, not the protocol', () => {
  assert.equal(actionLabel({action: {type: 'send'}, sessionTitle: 'Invoice CLI'}), 'Sent work to Invoice CLI');
  assert.equal(actionLabel({action: {type: 'create', title: 'Lisbon research'}}), 'Started Lisbon research');
  assert.equal(actionLabel({action: {type: 'ask_user'}}), 'Asked you a question');
});

test('a saved trace becomes the same view, with session titles from its inventory', () => {
  const view = turnFromTrace({id: 't3', at: 'x', message: 'review it', inventory: [{id: 's1', title: 'Invoice CLI', harness: 'codex', location: 'local'}],
    decision: {reasoning_summary: 'Codex reviews read-only.'}, actions: [{action: {type: 'send', session_id: 's1'}, sessionId: 's1', reply: 'Two findings.', state: 'idle'}], answer: 'Codex found two issues.'});
  assert.equal(view.finished, true);
  assert.deepEqual(view.actions.map(item => [item.sessionTitle, item.harness, item.status]), [['Invoice CLI', 'codex', 'done']]);
  assert.equal(view.answer, 'Codex found two issues.');
});
